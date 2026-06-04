package natslite

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPublishFrame(t *testing.T) {
	frame := PublishFrame("minik8s.echo", []byte("hello"))

	assert.Equal(t, "PUB minik8s.echo 5\r\nhello\r\n", string(frame))
}
