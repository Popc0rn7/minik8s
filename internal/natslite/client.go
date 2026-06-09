package natslite

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
)

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
	if _, err := fmt.Fprintf(conn, "SUB %s 1\r\nPING\r\n", subject); err != nil {
		return fmt.Errorf("sending NATS SUB: %w", err)
	}
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("reading NATS message: %w", err)
		}
		parts := strings.Fields(strings.TrimSpace(line))
		if len(parts) == 0 || parts[0] != "MSG" {
			continue
		}
		size, err := strconv.Atoi(parts[len(parts)-1])
		if err != nil {
			return fmt.Errorf("parsing NATS message size: %w", err)
		}
		payload := make([]byte, size+2)
		if _, err := io.ReadFull(reader, payload); err != nil {
			return fmt.Errorf("reading NATS payload: %w", err)
		}
		handler(payload[:size])
	}
}

func PublishFrame(subject string, payload []byte) []byte {
	return []byte(fmt.Sprintf("PUB %s %d\r\n%s\r\n", subject, len(payload), payload))
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
