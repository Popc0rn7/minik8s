package cliui

import (
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
	"minik8s/internal/pod"
)

const (
	IconInfo      = "󰋽"
	IconDocker    = ""
	IconPod       = "󱃾"
	IconDelete    = "󰆴"
	IconSuccess   = "󰄬"
	IconWarning   = "󱈸"
	IconContainer = ""
)

const (
	ColorCyan   = "\x1b[36m"
	ColorGreen  = "\x1b[32m"
	ColorYellow = "\x1b[33m"
	ColorRed    = "\x1b[31m"
	ColorDim    = "\x1b[2m"
	ColorReset  = "\x1b[0m"
)

// Plain reports whether output should avoid Nerd Font and Unicode tree glyphs.
func Plain() bool {
	return os.Getenv("MINIK8S_PLAIN") != ""
}

// Color reports whether ANSI color should be emitted.
func Color() bool {
	return os.Getenv("NO_COLOR") == ""
}

func Paint(color, text string) string {
	if !Color() || color == "" {
		return text
	}
	return color + text + ColorReset
}

func Icon(icon, fallback string) string {
	if Plain() {
		return fallback
	}
	return icon
}

func IconForStage(stage string) string {
	switch {
	case strings.Contains(stage, "docker"):
		return Icon(IconDocker, "[docker]")
	case strings.Contains(stage, "container"):
		return Icon(IconContainer, "[ctr]")
	case strings.Contains(stage, "pod-delete") || strings.Contains(stage, "delete"):
		return Icon(IconDelete, "[del]")
	case strings.Contains(stage, "pod"):
		return Icon(IconPod, "[pod]")
	case strings.Contains(stage, "success") || strings.Contains(stage, "running"):
		return Icon(IconSuccess, "[ok]")
	default:
		return Icon(IconInfo, "[i]")
	}
}

func TreeMiddle() string {
	if Plain() {
		return "|   +-"
	}
	return "│   ├─"
}

func TreeLast() string {
	if Plain() {
		return "|   `-"
	}
	return "│   └─"
}

func StatusIcon(phase pod.PodPhase) string {
	switch phase {
	case pod.PodRunning, pod.PodSucceeded:
		return Icon(IconSuccess, "[ok]")
	case pod.PodPending:
		return Icon(IconInfo, "[i]")
	case pod.PodFailed:
		return Icon(IconWarning, "[!]")
	default:
		return Icon(IconInfo, "[?]")
	}
}

func InfoLine(format string, args ...interface{}) string {
	return fmt.Sprintf("%s  %s\n", Icon(IconInfo, "[i]"), fmt.Sprintf(format, args...))
}

func SuccessLine(format string, args ...interface{}) string {
	return fmt.Sprintf("DONE  %s  %s\n", Icon(IconSuccess, "[ok]"), fmt.Sprintf(format, args...))
}

func WarnLine(format string, args ...interface{}) string {
	return fmt.Sprintf("WARN  %s  %s\n", Icon(IconWarning, "[!]"), fmt.Sprintf(format, args...))
}

func ErrorLine(format string, args ...interface{}) string {
	return fmt.Sprintf("ERROR %s  %s\n", Icon(IconWarning, "[!]"), fmt.Sprintf(format, args...))
}

func PadRight(s string, width int) string {
	padding := width - DisplayWidth(s)
	if padding <= 0 {
		return s
	}
	return s + strings.Repeat(" ", padding)
}

func DisplayWidth(s string) int {
	if Plain() || utf8.RuneCountInString(s) == len(s) {
		return len(s)
	}
	return runewidth.StringWidth(s)
}
