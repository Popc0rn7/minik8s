package natslite

import (
	"testing"

	"github.com/stretchr/testify/assert"
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
