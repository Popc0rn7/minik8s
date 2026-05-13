package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	store "minik8s/internal/kubebridge/etcd"
	"minik8s/internal/kubeproxy"
)

func TestNewKubebridgeConfigInjectsDefaultServiceProxy(t *testing.T) {
	t.Setenv("MINIK8S_SERVICE_PROXY_DISABLED", "")

	config := newKubebridgeConfig(
		store.NewInMemoryPodStore(),
		store.NewInMemoryServiceStore(),
		store.NewInMemoryNodeStore(),
	)

	require.NotNil(t, config.ServiceProxy)
	_, ok := config.ServiceProxy.(*kubeproxy.IPTablesProxy)
	assert.True(t, ok)
}

func TestNewKubebridgeConfigHonorsServiceProxyDisabled(t *testing.T) {
	t.Setenv("MINIK8S_SERVICE_PROXY_DISABLED", "1")

	config := newKubebridgeConfig(
		store.NewInMemoryPodStore(),
		store.NewInMemoryServiceStore(),
		store.NewInMemoryNodeStore(),
	)

	assert.Nil(t, config.ServiceProxy)
}
