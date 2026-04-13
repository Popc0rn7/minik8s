package podtui

import (
	"fmt"

	"github.com/charmbracelet/bubbletea"
	"minik8s/internal/pod"
	"minik8s/internal/store"
)

// Model is the Bubble Tea model for the Pod TUI.
type Model struct {
	store    store.PodStore
	pods     []*pod.Pod
	selected int
	quitting bool
	err      error
}

// NewModel creates a new Pod TUI model.
func NewModel(s store.PodStore) Model {
	return Model{
		store:    s,
		pods:     []*pod.Pod{},
		selected: 0,
	}
}

// Init implements tea.Model. It runs once at startup.
func (m Model) Init() tea.Cmd {
	return m.listPodsCmd("")
}

// Update implements tea.Model. It's called on every message.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		case "up", "k":
			if len(m.pods) > 0 && m.selected > 0 {
				m.selected--
			}
		case "down", "j":
			if len(m.pods) > 0 && m.selected < len(m.pods)-1 {
				m.selected++
			}
		case "r":
			return m, m.listPodsCmd("")
		case "d":
			if len(m.pods) > 0 {
				p := m.pods[m.selected]
				return m, m.deletePodCmd(p.Name, p.Namespace)
			}
		}
	case []*pod.Pod:
		m.pods = msg
		if m.selected >= len(m.pods) && len(m.pods) > 0 {
			m.selected = len(m.pods) - 1
		}
	case error:
		m.err = msg
	case refreshMsg:
		return m, m.listPodsCmd("")
	}
	return m, nil
}

// listPodsCmd returns a command to list pods from the store.
func (m Model) listPodsCmd(namespace string) tea.Cmd {
	return func() tea.Msg {
		pods, err := m.store.List(namespace, nil)
		if err != nil {
			return err
		}
		return pods
	}
}

// deletePodCmd returns a command to delete a pod and refresh the list.
func (m Model) deletePodCmd(name, namespace string) tea.Cmd {
	return func() tea.Msg {
		err := m.store.Delete(name, namespace)
		if err != nil {
			return fmt.Errorf("delete pod %s: %w", name, err)
		}
		return refreshMsg{}
	}
}

// refreshMsg signals that the pod list should be refreshed.
type refreshMsg struct{}
