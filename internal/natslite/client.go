package natslite

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Message struct {
	Subject string
	Reply   string
	Payload []byte
}

type MSGLine struct {
	Subject string
	SID     int
	Reply   string
	Size    int
}

func Publish(ctx context.Context, rawURL, subject string, payload []byte) error {
	if strings.TrimSpace(subject) == "" {
		return fmt.Errorf("subject is required")
	}
	address, err := addressFromURL(rawURL)
	if err != nil {
		return err
	}
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return fmt.Errorf("connecting to NATS: %w", err)
	}
	defer func() { _ = conn.Close() }()
	reader := bufio.NewReader(conn)
	_, _ = reader.ReadString('\n')
	if _, err := conn.Write([]byte("CONNECT {\"verbose\":false}\r\n")); err != nil {
		return fmt.Errorf("sending NATS CONNECT: %w", err)
	}
	if _, err := conn.Write(PublishFrame(subject, payload)); err != nil {
		return fmt.Errorf("sending NATS PUB: %w", err)
	}
	if _, err := conn.Write([]byte("PING\r\n")); err != nil {
		return fmt.Errorf("sending NATS PING: %w", err)
	}
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("reading NATS PONG: %w", err)
		}
		if strings.TrimSpace(line) == "PONG" {
			return nil
		}
	}
}

func Request(ctx context.Context, rawURL, subject string, payload []byte, timeout time.Duration) ([]byte, error) {
	if strings.TrimSpace(subject) == "" {
		return nil, fmt.Errorf("subject is required")
	}
	address, err := addressFromURL(rawURL)
	if err != nil {
		return nil, err
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var dialer net.Dialer
	conn, err := dialer.DialContext(reqCtx, "tcp", address)
	if err != nil {
		return nil, fmt.Errorf("connecting to NATS: %w", err)
	}
	defer func() { _ = conn.Close() }()
	go func() {
		<-reqCtx.Done()
		_ = conn.Close()
	}()
	reader := bufio.NewReader(conn)
	_, _ = reader.ReadString('\n')
	inbox := "_INBOX." + randomHex(12)
	if _, err := conn.Write([]byte("CONNECT {\"verbose\":false}\r\n")); err != nil {
		return nil, fmt.Errorf("sending NATS CONNECT: %w", err)
	}
	if _, err := conn.Write(SubscribeFrame(inbox, "", 1)); err != nil {
		return nil, fmt.Errorf("sending NATS SUB: %w", err)
	}
	if _, err := conn.Write(PublishRequestFrame(subject, inbox, payload)); err != nil {
		return nil, fmt.Errorf("sending NATS request PUB: %w", err)
	}
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if reqCtx.Err() != nil {
				return nil, reqCtx.Err()
			}
			return nil, fmt.Errorf("reading NATS response: %w", err)
		}
		msg, ok, err := parseMessageHeader(line)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		payload := make([]byte, msg.Size+2)
		if _, err := io.ReadFull(reader, payload); err != nil {
			return nil, fmt.Errorf("reading NATS response payload: %w", err)
		}
		return payload[:msg.Size], nil
	}
}

func Probe(ctx context.Context, rawURL string) error {
	address, err := addressFromURL(rawURL)
	if err != nil {
		return err
	}
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return fmt.Errorf("connecting to NATS: %w", err)
	}
	return conn.Close()
}

func Subscribe(ctx context.Context, rawURL, subject string, handler func([]byte)) error {
	return subscribe(ctx, rawURL, subject, "", func(msg Message) {
		handler(msg.Payload)
	})
}

func SubscribeQueue(ctx context.Context, rawURL, subject, queue string, handler func(Message)) error {
	return subscribe(ctx, rawURL, subject, queue, handler)
}

func subscribe(ctx context.Context, rawURL, subject, queue string, handler func(Message)) error {
	if strings.TrimSpace(subject) == "" {
		return fmt.Errorf("subject is required")
	}
	address, err := addressFromURL(rawURL)
	if err != nil {
		return err
	}
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return fmt.Errorf("connecting to NATS: %w", err)
	}
	defer func() { _ = conn.Close() }()
	go func() {
		<-ctx.Done()
		_ = conn.Close()
	}()
	reader := bufio.NewReader(conn)
	_, _ = reader.ReadString('\n')
	if _, err := conn.Write([]byte("CONNECT {\"verbose\":false}\r\n")); err != nil {
		return fmt.Errorf("sending NATS CONNECT: %w", err)
	}
	if _, err := conn.Write(SubscribeFrame(subject, queue, 1)); err != nil {
		return fmt.Errorf("sending NATS SUB: %w", err)
	}
	if _, err := conn.Write([]byte("PING\r\n")); err != nil {
		return fmt.Errorf("sending NATS PING: %w", err)
	}
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("reading NATS message: %w", err)
		}
		msg, ok, err := parseMessageHeader(line)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		payload := make([]byte, msg.Size+2)
		if _, err := io.ReadFull(reader, payload); err != nil {
			return fmt.Errorf("reading NATS payload: %w", err)
		}
		handler(Message{Subject: msg.Subject, Reply: msg.Reply, Payload: payload[:msg.Size]})
	}
}

func PublishFrame(subject string, payload []byte) []byte {
	return []byte(fmt.Sprintf("PUB %s %d\r\n%s\r\n", subject, len(payload), payload))
}

func PublishRequestFrame(subject, reply string, payload []byte) []byte {
	return []byte(fmt.Sprintf("PUB %s %s %d\r\n%s\r\n", subject, reply, len(payload), payload))
}

func SubscribeFrame(subject, queue string, sid int) []byte {
	if strings.TrimSpace(queue) == "" {
		return []byte(fmt.Sprintf("SUB %s %d\r\n", subject, sid))
	}
	return []byte(fmt.Sprintf("SUB %s %s %d\r\n", subject, queue, sid))
}

func ParseMSGLine(line string) (MSGLine, error) {
	parts := strings.Fields(strings.TrimSpace(line))
	if len(parts) != 4 && len(parts) != 5 {
		return MSGLine{}, fmt.Errorf("invalid NATS MSG line %q", strings.TrimSpace(line))
	}
	if parts[0] != "MSG" {
		return MSGLine{}, fmt.Errorf("not a NATS MSG line %q", strings.TrimSpace(line))
	}
	sid, err := strconv.Atoi(parts[2])
	if err != nil {
		return MSGLine{}, fmt.Errorf("parsing NATS sid: %w", err)
	}
	sizeIndex := len(parts) - 1
	size, err := strconv.Atoi(parts[sizeIndex])
	if err != nil {
		return MSGLine{}, fmt.Errorf("parsing NATS message size: %w", err)
	}
	msg := MSGLine{Subject: parts[1], SID: sid, Size: size}
	if len(parts) == 5 {
		msg.Reply = parts[3]
	}
	return msg, nil
}

func parseMessageHeader(line string) (MSGLine, bool, error) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || !strings.HasPrefix(trimmed, "MSG ") {
		return MSGLine{}, false, nil
	}
	msg, err := ParseMSGLine(trimmed)
	return msg, true, err
}

func addressFromURL(rawURL string) (string, error) {
	if strings.TrimSpace(rawURL) == "" {
		return "", fmt.Errorf("MINIK8S_NATS_URL is required")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("invalid NATS URL %q: %w", rawURL, err)
	}
	if parsed.Scheme != "nats" {
		return "", fmt.Errorf("NATS URL scheme must be nats")
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("NATS URL host is required")
	}
	return parsed.Host, nil
}

func randomHex(bytes int) string {
	buf := make([]byte, bytes)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}
