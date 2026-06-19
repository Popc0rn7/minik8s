package docker

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/go-connections/nat"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"minik8s/pkg/runtime"
)

func TestParsePortBindingsDefaultsTCPAndHostPort(t *testing.T) {
	bindings, exposed, err := parsePortBindings([]runtime.ContainerPort{{
		ContainerPort: 80,
		HostPort:      8080,
	}})

	require.NoError(t, err)
	assert.Contains(t, exposed, nat.Port("80/tcp"))
	assert.Equal(t, "8080", bindings["80/tcp"][0].HostPort)
}

func TestParsePortBindingsUsesHostIP(t *testing.T) {
	bindings, _, err := parsePortBindings([]runtime.ContainerPort{{
		ContainerPort: 53,
		HostIP:        "192.168.1.8",
		HostPort:      53,
		Protocol:      "UDP",
	}})

	require.NoError(t, err)
	assert.Equal(t, "192.168.1.8", bindings["53/udp"][0].HostIP)
	assert.Equal(t, "53", bindings["53/udp"][0].HostPort)
}

func TestSandboxImageDefaultsToDockerHubAlpine(t *testing.T) {
	t.Setenv("MINIK8S_PAUSE_IMAGE", "")

	assert.Equal(t, "alpine:3.20", pauseImage())
	assert.Equal(t, []string{"sh", "-c", "trap : TERM INT; sleep infinity & wait"}, sandboxCommand())
}

func TestSandboxImageCanBeOverridden(t *testing.T) {
	t.Setenv("MINIK8S_PAUSE_IMAGE", "registry.k8s.io/pause:3.9")

	assert.Equal(t, "registry.k8s.io/pause:3.9", pauseImage())
	assert.Empty(t, sandboxCommand())
}

func TestSandboxHostConfigDefaultsToNoNetwork(t *testing.T) {
	hostConfig := sandboxHostConfig(nil, "")

	assert.Equal(t, container.NetworkMode("none"), hostConfig.NetworkMode)
}

func TestSandboxHostConfigAllowsExplicitNetworkMode(t *testing.T) {
	hostConfig := sandboxHostConfig(nil, "host")

	assert.Equal(t, container.NetworkMode("host"), hostConfig.NetworkMode)
}

func TestSandboxHostConfigSetsClusterDNS(t *testing.T) {
	hostConfig := sandboxHostConfig(nil, "", []string{"10.244.0.1"})

	assert.Equal(t, []string{"10.244.0.1"}, hostConfig.DNS)
}

func TestSandboxHostConfigSetsDNSSearchDomains(t *testing.T) {
	hostConfig := sandboxHostConfig(nil, "", []string{"10.244.0.1"}, []string{"default.svc.cluster.local", "svc.cluster.local", "cluster.local"})

	assert.Equal(t, []string{"default.svc.cluster.local", "svc.cluster.local", "cluster.local"}, hostConfig.DNSSearch)
}

func TestResolveDockerEndpointUsesDockerHostFirst(t *testing.T) {
	t.Setenv("DOCKER_HOST", "unix:///explicit.sock")
	t.Setenv("DOCKER_CONTEXT", "desktop-linux")

	endpoint := ResolveDockerEndpoint()

	assert.Equal(t, "unix:///explicit.sock", endpoint.Host)
	assert.Equal(t, "DOCKER_HOST", endpoint.Source)
}

func TestResolveDockerEndpointUsesDockerContextEnv(t *testing.T) {
	home := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".docker", "contexts", "meta", "abc"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(home, ".docker", "config.json"), []byte(`{"currentContext":"default"}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(home, ".docker", "contexts", "meta", "abc", "meta.json"), []byte(`{
		"Name": "desktop-linux",
		"Endpoints": {"docker": {"Host": "unix:///tmp/docker.sock"}}
	}`), 0o644))
	t.Setenv("HOME", home)
	t.Setenv("DOCKER_HOST", "")
	t.Setenv("DOCKER_CONTEXT", "desktop-linux")

	endpoint := ResolveDockerEndpoint()

	assert.Equal(t, "unix:///tmp/docker.sock", endpoint.Host)
	assert.Equal(t, "DOCKER_CONTEXT", endpoint.Source)
}

func TestResolveDockerEndpointUsesDockerConfigDir(t *testing.T) {
	configDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(configDir, "contexts", "meta", "abc"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "config.json"), []byte(`{"currentContext":"desktop-linux"}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "contexts", "meta", "abc", "meta.json"), []byte(`{
		"Name": "desktop-linux",
		"Endpoints": {"docker": {"Host": "unix:///tmp/from-docker-config.sock"}}
	}`), 0o644))
	t.Setenv("DOCKER_CONFIG", configDir)
	t.Setenv("DOCKER_HOST", "")
	t.Setenv("DOCKER_CONTEXT", "")

	endpoint := ResolveDockerEndpoint()

	assert.Equal(t, "unix:///tmp/from-docker-config.sock", endpoint.Host)
	assert.Equal(t, "docker context desktop-linux", endpoint.Source)
}

func TestResolveDockerEndpointUsesCurrentContext(t *testing.T) {
	home := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".docker", "contexts", "meta", "abc"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(home, ".docker", "config.json"), []byte(`{"currentContext":"desktop-linux"}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(home, ".docker", "contexts", "meta", "abc", "meta.json"), []byte(`{
		"Name": "desktop-linux",
		"Endpoints": {"docker": {"Host": "unix:///tmp/docker.sock"}}
	}`), 0o644))
	t.Setenv("HOME", home)
	t.Setenv("DOCKER_HOST", "")
	t.Setenv("DOCKER_CONTEXT", "")

	endpoint := ResolveDockerEndpoint()

	assert.Equal(t, "unix:///tmp/docker.sock", endpoint.Host)
	assert.Equal(t, "docker context desktop-linux", endpoint.Source)
}

func TestResolveDockerEndpointFallsBackToDefault(t *testing.T) {
	home := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".docker"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(home, ".docker", "config.json"), []byte(`{"currentContext":"default"}`), 0o644))
	t.Setenv("HOME", home)
	t.Setenv("DOCKER_HOST", "")
	t.Setenv("DOCKER_CONTEXT", "")

	endpoint := ResolveDockerEndpoint()

	assert.Empty(t, endpoint.Host)
	assert.Equal(t, "docker default", endpoint.Source)
}

func TestApplyResourcesUsesDockerCPUAndMemoryLimits(t *testing.T) {
	hostConfig := &container.HostConfig{}

	applyResources(hostConfig, runtime.ResourceRequirements{
		Limits: runtime.ResourceList{
			CPU:    "0.5",
			Memory: "128Mi",
		},
	})

	assert.Equal(t, int64(500000000), hostConfig.NanoCPUs)
	assert.Equal(t, int64(134217728), hostConfig.Memory)
}

func TestContainerConfigLeavesImageDefaultsWhenCommandAndArgsEmpty(t *testing.T) {
	config := dockerContainerConfig(&runtime.ContainerConfig{
		Image:   "nats:2",
		Command: []string{},
		Args:    []string{},
	})

	assert.Nil(t, config.Entrypoint)
	assert.Nil(t, config.Cmd)
}

func TestContainerConfigSetsExplicitCommandAndArgs(t *testing.T) {
	config := dockerContainerConfig(&runtime.ContainerConfig{
		Image:   "quay.io/coreos/etcd:v3.5.15",
		Command: []string{"/usr/local/bin/etcd"},
		Args:    []string{"--name", "minik8s-etcd"},
	})

	assert.Equal(t, []string{"/usr/local/bin/etcd"}, []string(config.Entrypoint))
	assert.Equal(t, []string{"--name", "minik8s-etcd"}, []string(config.Cmd))
}

func TestProcessPullStreamReturnsDockerError(t *testing.T) {
	err := processPullStream(strings.NewReader(`{"status":"Pulling"}
{"errorDetail":{"message":"proxy connect failed"},"error":"proxy connect failed"}
`))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "proxy connect failed")
}

func TestProcessPullStreamAcceptsSuccessfulStream(t *testing.T) {
	err := processPullStream(strings.NewReader(`{"status":"Pulling fs layer"}
{"status":"Download complete"}
`))

	require.NoError(t, err)
}

func TestDockerPullCommandUsesDockerCLI(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://proxy.example:7890")
	cmd := dockerPullCommand(t.Context(), "nginx:alpine")

	assert.Equal(t, "docker", filepath.Base(cmd.Path))
	assert.True(t, reflect.DeepEqual([]string{"docker", "pull", "nginx:alpine"}, cmd.Args))
	assert.Contains(t, strings.Join(cmd.Env, "\n"), "HTTP_PROXY=http://proxy.example:7890")
}
