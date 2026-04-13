package minilog

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestInfoFormatsLevelAndColor(t *testing.T) {
	var buf bytes.Buffer
	restore := SetOutput(&buf)
	defer restore()
	t.Setenv("NO_COLOR", "")

	Info("pull", "image=%s", "nginx:alpine")

	got := buf.String()
	assert.True(t, strings.HasPrefix(got, "[Minik8s|"))
	assert.Contains(t, stripANSI(got), "] stage=pull level=INFO image=nginx:alpine")
	assert.Contains(t, got, "\x1b[36m")
	assert.Contains(t, got, "\x1b[0m")
}

func TestNoColorDisablesANSI(t *testing.T) {
	var buf bytes.Buffer
	restore := SetOutput(&buf)
	defer restore()
	t.Setenv("NO_COLOR", "1")

	Error("pull", "image=%s", "nginx:alpine")

	got := buf.String()
	assert.Contains(t, got, "] stage=pull level=ERROR image=nginx:alpine")
	assert.NotContains(t, got, "\x1b[")
}

func TestAllLevelsFormatConsistently(t *testing.T) {
	var buf bytes.Buffer
	restore := SetOutput(&buf)
	defer restore()
	t.Setenv("NO_COLOR", "1")

	Info("sync", "ok")
	Success("sync", "ok")
	Warn("sync", "careful")
	Error("sync", "failed")

	got := buf.String()
	assert.Contains(t, got, "level=INFO ok")
	assert.Contains(t, got, "level=SUCCESS ok")
	assert.Contains(t, got, "level=WARN careful")
	assert.Contains(t, got, "level=ERROR failed")
}

func stripANSI(s string) string {
	replacer := strings.NewReplacer(
		"\x1b[36m", "",
		"\x1b[32m", "",
		"\x1b[33m", "",
		"\x1b[31m", "",
		"\x1b[0m", "",
	)
	return replacer.Replace(s)
}
