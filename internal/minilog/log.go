package minilog

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

var (
	mu     sync.Mutex
	output io.Writer = os.Stderr
	now              = time.Now
)

type level struct {
	name  string
	color string
}

var (
	levelInfo    = level{name: "INFO", color: "\x1b[36m"}
	levelSuccess = level{name: "SUCCESS", color: "\x1b[32m"}
	levelWarn    = level{name: "WARN", color: "\x1b[33m"}
	levelError   = level{name: "ERROR", color: "\x1b[31m"}
)

// SetOutput changes the log destination and returns a restore function.
func SetOutput(w io.Writer) func() {
	mu.Lock()
	old := output
	output = w
	mu.Unlock()
	return func() {
		mu.Lock()
		output = old
		mu.Unlock()
	}
}

// Info prints an informational stage log line.
func Info(stage, format string, args ...interface{}) {
	write(levelInfo, stage, format, args...)
}

// Success prints a successful stage log line.
func Success(stage, format string, args ...interface{}) {
	write(levelSuccess, stage, format, args...)
}

// Warn prints a warning stage log line.
func Warn(stage, format string, args ...interface{}) {
	write(levelWarn, stage, format, args...)
}

// Error prints an error stage log line.
func Error(stage, format string, args ...interface{}) {
	write(levelError, stage, format, args...)
}

func write(l level, stage, format string, args ...interface{}) {
	mu.Lock()
	defer mu.Unlock()
	message := fmt.Sprintf(format, args...)
	timestamp := now().Format("15:04:05")
	if os.Getenv("NO_COLOR") != "" {
		_, _ = fmt.Fprintf(output, "[Minik8s|%s] stage=%s level=%s %s\n", timestamp, stage, l.name, message)
		return
	}
	_, _ = fmt.Fprintf(output, "[Minik8s|%s] stage=%s level=%s %s%s\x1b[0m\n", timestamp, stage, l.name, l.color, message)
}
