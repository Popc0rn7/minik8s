package natslite

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPublishFrame(t *testing.T) {
	frame := PublishFrame("minik8s.echo", []byte("hello"))

	assert.Equal(t, "PUB minik8s.echo 5\r\nhello\r\n", string(frame))
}

func TestPublishRequestFrame(t *testing.T) {
	frame := PublishRequestFrame("minik8s.serverless.invoke", "_INBOX.123", []byte("hello"))

	assert.Equal(t, "PUB minik8s.serverless.invoke _INBOX.123 5\r\nhello\r\n", string(frame))
}

func TestSubscribeQueueFrame(t *testing.T) {
	frame := SubscribeFrame("minik8s.serverless.invoke", "workers", 7)

	assert.Equal(t, "SUB minik8s.serverless.invoke workers 7\r\n", string(frame))
}

func TestParseMSGLineWithReplySubject(t *testing.T) {
	msg, err := ParseMSGLine("MSG minik8s.serverless.invoke 1 _INBOX.123 5")

	assert.NoError(t, err)
	assert.Equal(t, "minik8s.serverless.invoke", msg.Subject)
	assert.Equal(t, "_INBOX.123", msg.Reply)
	assert.Equal(t, 5, msg.Size)
}

func TestSubscribeQueueHandlesMessagesConcurrently(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = listener.Close() }()
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_, _ = conn.Write([]byte("INFO {}\r\n"))
		time.Sleep(20 * time.Millisecond)
		_, _ = conn.Write([]byte("MSG minik8s.serverless.invoke 1 _INBOX.1 5\r\nfirst\r\n"))
		_, _ = conn.Write([]byte("MSG minik8s.serverless.invoke 1 _INBOX.2 6\r\nsecond\r\n"))
		time.Sleep(200 * time.Millisecond)
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var current int32
	var maxSeen int32
	var handled int32
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		_ = SubscribeQueue(ctx, "nats://"+listener.Addr().String(), "minik8s.serverless.invoke", "workers", func(Message) {
			now := atomic.AddInt32(&current, 1)
			for {
				previous := atomic.LoadInt32(&maxSeen)
				if now <= previous || atomic.CompareAndSwapInt32(&maxSeen, previous, now) {
					break
				}
			}
			time.Sleep(75 * time.Millisecond)
			atomic.AddInt32(&current, -1)
			atomic.AddInt32(&handled, 1)
			wg.Done()
		})
	}()
	require.Eventually(t, func() bool {
		return atomic.LoadInt32(&handled) == 2
	}, time.Second, 10*time.Millisecond)
	cancel()
	wg.Wait()
	<-done

	assert.Equal(t, int32(2), atomic.LoadInt32(&maxSeen))
}
