package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadDotEnvSetsUnsetVariables(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	require.NoError(t, os.WriteFile(path, []byte("MINIK8S_HARBOR=http://127.0.0.1:18080\nMINIK8S_CNI_DISABLED=1\n"), 0o644))
	t.Setenv("MINIK8S_HARBOR", "")
	t.Setenv("MINIK8S_CNI_DISABLED", "")

	require.NoError(t, LoadDotEnv(path))

	assert.Equal(t, "http://127.0.0.1:18080", os.Getenv("MINIK8S_HARBOR"))
	assert.Equal(t, "1", os.Getenv("MINIK8S_CNI_DISABLED"))
}

func TestLoadDotEnvPreservesExistingEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	require.NoError(t, os.WriteFile(path, []byte("MINIK8S_HARBOR=http://from-file:18080\n"), 0o644))
	t.Setenv("MINIK8S_HARBOR", "http://from-shell:18080")

	require.NoError(t, LoadDotEnv(path))

	assert.Equal(t, "http://from-shell:18080", os.Getenv("MINIK8S_HARBOR"))
}

func TestLoadDotEnvIgnoresMissingFile(t *testing.T) {
	require.NoError(t, LoadDotEnv(filepath.Join(t.TempDir(), ".env")))
}
