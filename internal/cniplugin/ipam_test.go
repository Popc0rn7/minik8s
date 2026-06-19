package cniplugin

import (
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIPAMAllocatesStableAddressesAndReusesReleasedIP(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ipam.json")
	ipam := NewIPAM(path, "10.244.0.0/24", "10.244.0.1")

	first, err := ipam.Allocate("default/pod-a")
	require.NoError(t, err)
	assert.Equal(t, "10.244.0.2", first.String())

	again, err := ipam.Allocate("default/pod-a")
	require.NoError(t, err)
	assert.Equal(t, first.String(), again.String())

	second, err := ipam.Allocate("default/pod-b")
	require.NoError(t, err)
	assert.Equal(t, "10.244.0.3", second.String())

	reloaded := NewIPAM(path, "10.244.0.0/24", "10.244.0.1")
	stable, err := reloaded.Allocate("default/pod-a")
	require.NoError(t, err)
	assert.Equal(t, "10.244.0.2", stable.String())

	require.NoError(t, reloaded.Release("default/pod-a"))
	reused, err := reloaded.Allocate("default/pod-c")
	require.NoError(t, err)
	assert.Equal(t, "10.244.0.2", reused.String())
}

func TestIPAMAllocatesUniqueAddressesAcrossConcurrentInstances(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ipam.json")
	const workers = 5
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make(chan string, workers)
	errs := make(chan error, workers)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			ipam := NewIPAM(path, "10.244.0.0/29", "10.244.0.1")
			ip, err := ipam.Allocate("default/pod-" + string(rune('a'+index)))
			if err != nil {
				errs <- err
				return
			}
			results <- ip.String()
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)

	for err := range errs {
		require.NoError(t, err)
	}
	seen := map[string]struct{}{}
	for ip := range results {
		if _, ok := seen[ip]; ok {
			t.Fatalf("duplicate IP allocated: %s", ip)
		}
		seen[ip] = struct{}{}
	}
	require.Len(t, seen, workers)
}
