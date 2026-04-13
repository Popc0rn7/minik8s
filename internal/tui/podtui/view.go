package podtui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"minik8s/internal/pod"
)

// Style definitions using Lipgloss.
var (
	// Header styles
	headerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("255")).
			Background(lipgloss.Color("21")).
			Bold(true)

	// Pod row styles
	selectedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("0")).
			Background(lipgloss.Color("81")) // light blue

	normalStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("15")) // white

	// Status badge styles - no Width(), use exact text
	runningStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("255")).
			Background(lipgloss.Color("34")) // green

	pendingStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("255")).
			Background(lipgloss.Color("220")) // yellow

	failedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("255")).
			Background(lipgloss.Color("196")) // red

	succeededStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("255")).
			Background(lipgloss.Color("245")) // gray

	unknownStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("255")).
			Background(lipgloss.Color("245")) // gray

	// Title and help styles
	titleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("255")).
			Background(lipgloss.Color("233")). // dark slate
			Bold(true)

	helpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("245")).
			MarginTop(1)

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("196"))
)

// Table column widths - fixed for alignment
const (
	colName      = 30
	colStatus    = 12
	colUptime    = 10
	colNamespace = 14
	colLabels    = 30
)

// statusBadge returns a styled badge for the pod phase.
func statusBadge(phase pod.PodPhase) string {
	var text string
	switch phase {
	case pod.PodRunning:
		text = "● Running"
	case pod.PodPending:
		text = "◐ Pending"
	case pod.PodFailed:
		text = "✗ Failed"
	case pod.PodSucceeded:
		text = "✓ Done"
	default:
		text = "? Unknown"
	}

	switch phase {
	case pod.PodRunning:
		return runningStyle.Render(text)
	case pod.PodPending:
		return pendingStyle.Render(text)
	case pod.PodFailed:
		return failedStyle.Render(text)
	case pod.PodSucceeded:
		return succeededStyle.Render(text)
	default:
		return unknownStyle.Render(text)
	}
}

// formatUptime returns a human-readable uptime string.
func formatUptime(d time.Duration) string {
	if d == 0 {
		return "-"
	}
	days := int(d.Hours() / 24)
	hours := int(d.Hours()) % 24
	minutes := int(d.Minutes()) % 60

	if days > 0 {
		return fmt.Sprintf("%dd%dh%dm", days, hours, minutes)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh%dm", hours, minutes)
	}
	return fmt.Sprintf("%dm", minutes)
}

// truncate truncates a string to maxLen, adding ellipsis if truncated.
func truncate(s string, maxLen int) string {
	if len(s) > maxLen {
		return s[:maxLen-3] + "..."
	}
	return s
}

// padRight pads a string to maxLen with spaces on the right.
func padRight(s string, maxLen int) string {
	if len(s) > maxLen {
		return truncate(s, maxLen)
	}
	return s + strings.Repeat(" ", maxLen-len(s))
}

// formatLabels formats labels as "k=v, k=v" truncated.
func formatLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return "-"
	}
	var parts []string
	for k, v := range labels {
		parts = append(parts, fmt.Sprintf("%s=%s", k, v))
	}
	result := strings.Join(parts, ", ")
	return truncate(result, colLabels)
}

// View returns the string representation of the TUI.
func (m Model) View() string {
	var sb strings.Builder

	// Title bar
	sb.WriteString(titleStyle.Render(" Minik8s Pod Viewer "))
	sb.WriteString("\n\n")

	// Build header as plain text, then style
	header := fmt.Sprintf("%s %s %s %s %s",
		padRight("NAME", colName),
		padRight("STATUS", colStatus),
		padRight("UPTIME", colUptime),
		padRight("NAMESPACE", colNamespace),
		padRight("LABELS", colLabels))
	sb.WriteString(headerStyle.Render(header))
	sb.WriteString("\n")

	// Pod rows
	if len(m.pods) == 0 {
		sb.WriteString(normalStyle.Render(" No pods found. Press 'r' to refresh. "))
		sb.WriteString("\n")
	} else {
		for i, p := range m.pods {
			rowStyle := normalStyle
			cursor := "  "
			if i == m.selected {
				rowStyle = selectedStyle
				cursor = "> "
			}

			// Build row as plain text with exact column widths
			status := statusBadge(p.Status.Phase)
			uptime := formatUptime(p.Status.GetUptime())
			ns := p.Namespace
			if ns == "" {
				ns = "default"
			}
			labels := formatLabels(p.Labels)

			row := fmt.Sprintf("%s%s %s %s %s %s",
				cursor,
				padRight(p.Name, colName-2),
				status,
				padRight(uptime, colUptime),
				padRight(ns, colNamespace),
				padRight(labels, colLabels))

			sb.WriteString(rowStyle.Render(row))
			sb.WriteString("\n")
		}
	}

	// Error display
	if m.err != nil {
		sb.WriteString(errorStyle.Render(fmt.Sprintf("Error: %v", m.err)))
		sb.WriteString("\n")
	}

	// Help footer
	sb.WriteString(helpStyle.Render("↑↓ navigate  r: refresh  d: delete  q: quit"))

	return sb.String()
}
