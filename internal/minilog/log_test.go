package minilog

import (
	"bytes"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestInfoFormatsLevelAndColor(t *testing.T) {
	var buf bytes.Buffer
	restore := SetOutput(&buf)
	defer restore()
	t.Setenv("NO_COLOR", "")
	t.Setenv("MINIK8S_PLAIN", "")

	Info("pull", "image=%s", "nginx:alpine")

	got := buf.String()
	assert.Regexp(t, regexp.MustCompile(`^\d\d:\d\d:\d\d INFO\s+`), stripANSI(got))
	assert.Contains(t, stripANSI(got), "󰋽  pull: image=nginx:alpine")
	assert.Contains(t, got, "\x1b[36m")
	assert.Contains(t, got, "\x1b[0m")
}

func TestNoColorDisablesANSI(t *testing.T) {
	var buf bytes.Buffer
	restore := SetOutput(&buf)
	defer restore()
	t.Setenv("NO_COLOR", "1")
	t.Setenv("MINIK8S_PLAIN", "")

	Error("pull", "image=%s", "nginx:alpine")

	got := buf.String()
	assert.Contains(t, got, "ERROR")
	assert.Contains(t, got, "󱈸  pull: image=nginx:alpine")
	assert.NotContains(t, got, "\x1b[")
}

func TestAllLevelsFormatConsistently(t *testing.T) {
	var buf bytes.Buffer
	restore := SetOutput(&buf)
	defer restore()
	t.Setenv("NO_COLOR", "1")
	t.Setenv("MINIK8S_PLAIN", "")

	Info("sync", "ok")
	Success("sync", "ok")
	Warn("sync", "careful")
	Error("sync", "failed")

	got := buf.String()
	assert.Contains(t, got, "INFO")
	assert.Contains(t, got, "DONE")
	assert.Contains(t, got, "WARN")
	assert.Contains(t, got, "ERROR")
	assert.Contains(t, got, "󰄬  sync: ok")
	assert.Contains(t, got, "󱈸  sync: careful")
	assert.Contains(t, got, "󱈸  sync: failed")
}

func TestPlainModeUsesASCIIFallback(t *testing.T) {
	var buf bytes.Buffer
	restore := SetOutput(&buf)
	defer restore()
	t.Setenv("NO_COLOR", "1")
	t.Setenv("MINIK8S_PLAIN", "1")

	Info("sync", "ok")
	LastStep("pod-delete", "removed pod=%s", "nginx")

	got := buf.String()
	assert.Contains(t, got, "[i]  sync: ok")
	assert.Contains(t, got, "|   `- [del]  pod-delete: removed pod=nginx")
	assert.NotContains(t, got, "󰋽")
	assert.NotContains(t, got, "└─")
}

func TestTreeStepsFormatWithGuides(t *testing.T) {
	var buf bytes.Buffer
	restore := SetOutput(&buf)
	defer restore()
	t.Setenv("NO_COLOR", "1")
	t.Setenv("MINIK8S_PLAIN", "")

	Step("docker-runtime", "stopping container [%s]", "default")
	LastStep("pod-delete", "reclaim pod %s", "busybox-client")

	got := buf.String()
	assert.Contains(t, got, "│   ├─   docker-runtime: stopping container [default]")
	assert.Contains(t, got, "│   └─ 󰆴  pod-delete: reclaim pod busybox-client")
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
