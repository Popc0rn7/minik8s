package cni

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunnerAddInvokesPluginWithCNIEnvironmentAndConfig(t *testing.T) {
	root := t.TempDir()
	binDir := filepath.Join(root, "bin")
	confDir := filepath.Join(root, "net.d")
	require.NoError(t, os.MkdirAll(binDir, 0o755))
	require.NoError(t, os.MkdirAll(confDir, 0o755))

	logPath := filepath.Join(root, "plugin.log")
	pluginPath := filepath.Join(binDir, "minik8s-bridge")
	require.NoError(t, os.WriteFile(pluginPath, []byte(`#!/bin/sh
set -eu
{
  echo "command=$CNI_COMMAND"
  echo "container=$CNI_CONTAINERID"
  echo "netns=$CNI_NETNS"
  echo "ifname=$CNI_IFNAME"
  echo "path=$CNI_PATH"
  cat
} > "$MINIK8S_TEST_LOG"
cat <<'JSON'
{"cniVersion":"1.0.0","ips":[{"version":"4","address":"10.244.0.2/24","gateway":"10.244.0.1"}]}
JSON
`), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(confDir, "10-minik8s.conf"), []byte(`{
  "cniVersion": "1.0.0",
  "name": "minik8s",
  "type": "minik8s-bridge"
}`), 0o644))

	t.Setenv("MINIK8S_TEST_LOG", logPath)
	runner := NewRunner(Config{ConfDir: confDir, BinDir: binDir})

	result, err := runner.Add(context.Background(), PodNetwork{
		ContainerID: "sandbox-1",
		NetNS:       "/proc/123/ns/net",
		IfName:      "eth0",
	})
	require.NoError(t, err)
	assert.Equal(t, "10.244.0.2", result.PodIP)

	logData, err := os.ReadFile(logPath)
	require.NoError(t, err)
	log := string(logData)
	assert.Contains(t, log, "command=ADD")
	assert.Contains(t, log, "container=sandbox-1")
	assert.Contains(t, log, "netns=/proc/123/ns/net")
	assert.Contains(t, log, "ifname=eth0")
	assert.Contains(t, log, "path="+binDir)
	assert.Contains(t, log, `"type": "minik8s-bridge"`)
}

func TestRunnerDelIsIdempotentWhenNoConfigExists(t *testing.T) {
	runner := NewRunner(Config{
		ConfDir: filepath.Join(t.TempDir(), "missing-net.d"),
		BinDir:  filepath.Join(t.TempDir(), "missing-bin"),
	})

	err := runner.Del(context.Background(), PodNetwork{
		ContainerID: "sandbox-1",
		NetNS:       "/proc/123/ns/net",
		IfName:      "eth0",
	})

	require.NoError(t, err)
}
