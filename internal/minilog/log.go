package minilog

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"minik8s/internal/cliui"
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
	levelInfo    = level{name: "INFO", color: cliui.ColorCyan}
	levelSuccess = level{name: "DONE", color: cliui.ColorGreen}
	levelWarn    = level{name: "WARN", color: cliui.ColorYellow}
	levelError   = level{name: "ERROR", color: cliui.ColorRed}
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

// Step prints an informational child stage with a tree guide.
func Step(stage, format string, args ...interface{}) {
	writeStep(levelInfo, cliui.TreeMiddle(), stage, format, args...)
}

// LastStep prints the final informational child stage with a tree guide.
func LastStep(stage, format string, args ...interface{}) {
	writeStep(levelInfo, cliui.TreeLast(), stage, format, args...)
}

func write(l level, stage, format string, args ...interface{}) {
	mu.Lock()
	defer mu.Unlock()
	message := fmt.Sprintf(format, args...)
	timestamp := now().Format("15:04:05")
	icon := iconForLevel(l, stage)
	line := fmt.Sprintf("%s %-5s %s  %s: %s", timestamp, l.name, icon, stage, message)
	_, _ = fmt.Fprintln(output, cliui.Paint(l.color, line))
}

func writeStep(l level, guide, stage, format string, args ...interface{}) {
	mu.Lock()
	defer mu.Unlock()
	message := fmt.Sprintf(format, args...)
	timestamp := now().Format("15:04:05")
	line := fmt.Sprintf("%s %-5s %s %s  %s: %s", timestamp, l.name, guide, cliui.IconForStage(stage), stage, message)
	_, _ = fmt.Fprintln(output, cliui.Paint(l.color, line))
}

func iconForLevel(l level, stage string) string {
	switch l.name {
	case levelSuccess.name:
		return cliui.Icon(cliui.IconSuccess, "[ok]")
	case levelWarn.name, levelError.name:
		return cliui.Icon(cliui.IconWarning, "[!]")
	default:
		if os.Getenv("MINIK8S_PLAIN") != "" {
			return cliui.Icon(cliui.IconInfo, "[i]")
		}
		if icon := cliui.IconForStage(stage); icon != "" {
			return icon
		}
		return cliui.Icon(cliui.IconInfo, "[i]")
	}
}
