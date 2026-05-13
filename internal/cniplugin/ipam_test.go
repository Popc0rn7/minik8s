package cniplugin

import (
	"path/filepath"
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
