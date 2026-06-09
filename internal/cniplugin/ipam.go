package cniplugin

import (
	"encoding/json"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// IPAM persists simple host-local Pod IP allocations for the bridge plugin.
type IPAM struct {
	path    string
	cidr    string
	gateway string
	mu      sync.Mutex
}

type ipamState struct {
	Allocations map[string]string `json:"allocations"`
}

// NewIPAM creates a host-local IPAM allocator.
func NewIPAM(path, cidr, gateway string) *IPAM {
	return &IPAM{path: path, cidr: cidr, gateway: gateway}
}

// Allocate returns a stable IP for key, allocating the lowest available address.
func (i *IPAM) Allocate(key string) (net.IP, error) {
	i.mu.Lock()
	defer i.mu.Unlock()

	state, err := i.load()
	if err != nil {
		return nil, err
	}
	if existing := state.Allocations[key]; existing != "" {
		ip := net.ParseIP(existing)
		if ip == nil {
			return nil, fmt.Errorf("stored invalid IP %q for %s", existing, key)
		}
		return ip, nil
	}

	ip, err := i.nextAvailable(state)
	if err != nil {
		return nil, err
	}
	state.Allocations[key] = ip.String()
	if err := i.save(state); err != nil {
		return nil, err
	}
	return ip, nil
}

// Release frees the address for key. It is idempotent.
func (i *IPAM) Release(key string) error {
	i.mu.Lock()
	defer i.mu.Unlock()

	state, err := i.load()
	if err != nil {
		return err
	}
	delete(state.Allocations, key)
	return i.save(state)
}

func (i *IPAM) load() (ipamState, error) {
	state := ipamState{Allocations: map[string]string{}}
	data, err := os.ReadFile(i.path)
	if err != nil {
		if os.IsNotExist(err) {
			return state, nil
		}
		return state, err
	}
	if len(data) == 0 {
		return state, nil
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return state, err
	}
	if state.Allocations == nil {
		state.Allocations = map[string]string{}
	}
	return state, nil
}

func (i *IPAM) save(state ipamState) error {
	dir := filepath.Dir(i.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".cni-ipam-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, i.path); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func (i *IPAM) nextAvailable(state ipamState) (net.IP, error) {
	_, network, err := net.ParseCIDR(i.cidr)
	if err != nil {
		return nil, err
	}
	gateway := net.ParseIP(i.gateway)
	if gateway == nil {
		return nil, fmt.Errorf("invalid gateway %q", i.gateway)
	}

	used := map[string]bool{gateway.String(): true}
	for _, ip := range state.Allocations {
		used[ip] = true
	}

	var candidates []string
	for ip := firstUsable(network); network.Contains(ip); ip = nextIP(ip) {
		if isNetworkOrBroadcast(ip, network) || used[ip.String()] {
			continue
		}
		candidates = append(candidates, ip.String())
	}
	sort.Slice(candidates, func(a, b int) bool {
		return ipLess(net.ParseIP(candidates[a]), net.ParseIP(candidates[b]))
	})
	if len(candidates) == 0 {
		return nil, fmt.Errorf("no available IPs in %s", i.cidr)
	}
	return net.ParseIP(candidates[0]), nil
}

func firstUsable(network *net.IPNet) net.IP {
	return nextIP(network.IP)
}

func nextIP(ip net.IP) net.IP {
	next := append(net.IP(nil), ip.To4()...)
	if next == nil {
		next = append(net.IP(nil), ip.To16()...)
	}
	for j := len(next) - 1; j >= 0; j-- {
		next[j]++
		if next[j] != 0 {
			break
		}
	}
	return next
}

func isNetworkOrBroadcast(ip net.IP, network *net.IPNet) bool {
	if ip.Equal(network.IP) {
		return true
	}
	ones, bits := network.Mask.Size()
	if bits != 32 || ones == 32 {
		return false
	}
	broadcast := append(net.IP(nil), network.IP.To4()...)
	for idx := range broadcast {
		broadcast[idx] |= ^network.Mask[idx]
	}
	return ip.Equal(broadcast)
}

func ipLess(a, b net.IP) bool {
	ai := big.NewInt(0).SetBytes(a.To16())
	bi := big.NewInt(0).SetBytes(b.To16())
	return ai.Cmp(bi) < 0
}
