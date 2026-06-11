package harbor

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"sync"
)

type nodeTokenRegistry struct {
	mu     sync.RWMutex
	tokens map[string]string
}

func newNodeTokenRegistry() *nodeTokenRegistry {
	return &nodeTokenRegistry{tokens: make(map[string]string)}
}

func (r *nodeTokenRegistry) Set(nodeName, token string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tokens[nodeName] = token
}

func (r *nodeTokenRegistry) Validate(nodeName, token string) bool {
	r.mu.RLock()
	want, ok := r.tokens[nodeName]
	r.mu.RUnlock()
	if !ok {
		return true
	}
	if token == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(want)) == 1
}

func generateNodeToken(nodeName string) (string, error) {
	var buf [24]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("generating node token: %w", err)
	}
	encoded := base64.RawURLEncoding.EncodeToString(buf[:])
	return "node_" + strings.ReplaceAll(nodeName, "-", "_") + "_" + encoded, nil
}

func bearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(auth, prefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(auth, prefix))
}
