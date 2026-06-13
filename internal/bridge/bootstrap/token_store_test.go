package bootstrap

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBootstrapTokenStoreSetStatusValidateAndClear(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bootstrap-token.json")
	now := time.Date(2026, 6, 11, 10, 0, 0, 0, time.UTC)

	require.NoError(t, SetBootstrapToken(path, "mks_bootstrap_secret", time.Hour, now))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "mks_bootstrap_secret")
	var raw map[string]any
	require.NoError(t, json.Unmarshal(data, &raw))
	assert.NotEmpty(t, raw["tokenHash"])

	status, err := BootstrapTokenStatus(path, now.Add(30*time.Minute))
	require.NoError(t, err)
	assert.True(t, status.Configured)
	assert.False(t, status.Expired)

	require.NoError(t, ValidateBootstrapToken(path, "mks_bootstrap_secret", now.Add(30*time.Minute)))
	require.Error(t, ValidateBootstrapToken(path, "wrong", now.Add(30*time.Minute)))
	require.Error(t, ValidateBootstrapToken(path, "mks_bootstrap_secret", now.Add(2*time.Hour)))

	require.NoError(t, ClearBootstrapToken(path))
	status, err = BootstrapTokenStatus(path, now)
	require.NoError(t, err)
	assert.False(t, status.Configured)
}
