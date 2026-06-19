package harbor

import (
	"strconv"
	"sync"

	"minik8s/internal/node"
)

type nodeWatchEvent struct {
	Type   string    `json:"type"`
	Object node.Node `json:"object"`
}

type nodeWatchHub struct {
	mu       sync.Mutex
	revision uint64
	nextID   uint64
	watchers map[uint64]chan nodeWatchEvent
}

func newNodeWatchHub() *nodeWatchHub {
	return &nodeWatchHub{
		revision: 1,
		watchers: make(map[uint64]chan nodeWatchEvent),
	}
}

func (h *nodeWatchHub) resourceVersion() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return strconv.FormatUint(h.revision, 10)
}

func (h *nodeWatchHub) stamp(n node.Node) node.Node {
	h.mu.Lock()
	defer h.mu.Unlock()
	return stampNodeResourceVersion(n, h.revision)
}

func (h *nodeWatchHub) publish(eventType string, n node.Node) {
	h.mu.Lock()
	h.revision++
	event := nodeWatchEvent{Type: eventType, Object: stampNodeResourceVersion(n, h.revision)}
	for id, ch := range h.watchers {
		select {
		case ch <- event:
		default:
			close(ch)
			delete(h.watchers, id)
		}
	}
	h.mu.Unlock()
}

func (h *nodeWatchHub) subscribe() (uint64, <-chan nodeWatchEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.nextID++
	id := h.nextID
	ch := make(chan nodeWatchEvent, 16)
	h.watchers[id] = ch
	return id, ch
}

func (h *nodeWatchHub) unsubscribe(id uint64) {
	h.mu.Lock()
	ch, ok := h.watchers[id]
	if ok {
		delete(h.watchers, id)
		close(ch)
	}
	h.mu.Unlock()
}

func stampNodeResourceVersion(n node.Node, revision uint64) node.Node {
	n.ResourceVersion = strconv.FormatUint(revision, 10)
	return n
}
