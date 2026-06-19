package kubeproxy

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"minik8s/internal/pod"
	"minik8s/internal/service"
)

type recordingRunner struct {
	commands []string
	failOn   string
}

func (r *recordingRunner) Run(ctx context.Context, args ...string) error {
	_ = ctx
	command := strings.Join(args, " ")
	r.commands = append(r.commands, command)
	if r.failOn != "" && strings.Contains(command, r.failOn) {
		return errors.New("runner failed")
	}
	if strings.Contains(command, "-D ") {
		return errors.New("Bad rule (does a matching rule exist in that chain?)")
	}
	return nil
}

func TestIPTablesProxySyncServiceProgramsClusterIPAndNodePort(t *testing.T) {
	runner := &recordingRunner{}
	proxy := NewIPTablesProxy(runner.Run)
	svc := &service.Service{
		ObjectMeta: pod.ObjectMeta{Name: "nginx", Namespace: "default"},
		Spec: service.ServiceSpec{
			Type: service.ServiceTypeNodePort,
			Ports: []service.ServicePort{{
				Protocol:   "TCP",
				Port:       80,
				TargetPort: 8080,
				NodePort:   30080,
			}},
		},
		Status: service.ServiceStatus{
			ClusterIP: "10.96.0.10",
			Endpoints: []service.Endpoint{
				{PodName: "nginx-a", IP: "10.244.0.2", Port: 80, TargetPort: 8080, Protocol: "TCP"},
				{PodName: "nginx-b", IP: "10.244.0.3", Port: 80, TargetPort: 8080, Protocol: "TCP"},
			},
		},
	}

	require.NoError(t, proxy.SyncService(context.Background(), svc))

	joined := strings.Join(runner.commands, "\n")
	assert.Contains(t, joined, "-t nat -N MK8S-SVC-")
	assert.Contains(t, joined, "-t nat -A PREROUTING -p tcp -d 10.96.0.10 --dport 80 -j MK8S-SVC-")
	assert.Contains(t, joined, "-t nat -A OUTPUT -p tcp -d 10.96.0.10 --dport 80 -j MK8S-SVC-")
	assert.Contains(t, joined, "-t nat -A PREROUTING -p tcp --dport 30080 -j MK8S-SVC-")
	assert.Contains(t, joined, "-t nat -A OUTPUT -p tcp --dport 30080 -j MK8S-SVC-")
	assert.Contains(t, joined, "-t nat -A MK8S-SVC-")
	assert.Contains(t, joined, "--dport 80 -m statistic --mode random --probability 0.500000 -j DNAT --to-destination 10.244.0.2:8080")
	assert.Contains(t, joined, "--dport 80 -j DNAT --to-destination 10.244.0.3:8080")
	assert.Contains(t, joined, "--dport 30080 -m statistic --mode random --probability 0.500000 -j DNAT --to-destination 10.244.0.2:8080")
	assert.Contains(t, joined, "--dport 30080 -j DNAT --to-destination 10.244.0.3:8080")
	assert.Contains(t, joined, "-t nat -A POSTROUTING -p tcp ! -s 10.244.0.0/16 -d 10.244.0.2 --dport 8080 -j MASQUERADE")
	assert.Contains(t, joined, "-t nat -A POSTROUTING -p tcp ! -s 10.244.0.0/16 -d 10.244.0.3 --dport 8080 -j MASQUERADE")
}

func TestIPTablesProxySyncAllReconcilesEveryService(t *testing.T) {
	runner := &recordingRunner{}
	proxy := NewIPTablesProxy(runner.Run)
	services := []*service.Service{
		{ObjectMeta: pod.ObjectMeta{Name: "a", Namespace: "default"}, Status: service.ServiceStatus{ClusterIP: "10.96.0.1"}},
		{ObjectMeta: pod.ObjectMeta{Name: "b", Namespace: "default"}, Status: service.ServiceStatus{ClusterIP: "10.96.0.2"}},
	}

	require.NoError(t, proxy.SyncAll(context.Background(), services))

	assert.Equal(t, 2, countCommandsContaining(runner.commands, "-t nat -N MK8S-SVC-"))
}

func TestIPTablesProxySyncAllCleansStaleMinik8sRulesBeforeFirstSync(t *testing.T) {
	runner := &recordingRunner{}
	proxy := NewIPTablesProxy(runner.Run)
	proxy.ruleLister = func(ctx context.Context) (string, error) {
		_ = ctx
		return strings.Join([]string{
			"-A PREROUTING -d 10.96.0.1/32 -p tcp -m tcp --dport 80 -j MK8S-SVC-STALE",
			"-A OUTPUT -d 10.96.0.1/32 -p tcp -m tcp --dport 80 -j MK8S-SVC-STALE",
			"-A POSTROUTING ! -s 10.244.0.0/16 -d 10.244.0.3/32 -p tcp -m tcp --dport 8080 -j MASQUERADE",
			":MK8S-SVC-STALE - [0:0]",
			"-A MK8S-SVC-STALE -p tcp -m tcp --dport 80 -j DNAT --to-destination 10.244.0.3:8080",
		}, "\n"), nil
	}
	svc := &service.Service{
		ObjectMeta: pod.ObjectMeta{Name: "nginx", Namespace: "default"},
		Spec: service.ServiceSpec{
			Type:  service.ServiceTypeClusterIP,
			Ports: []service.ServicePort{{Protocol: "TCP", Port: 80, TargetPort: 80}},
		},
		Status: service.ServiceStatus{
			ClusterIP: "10.96.0.1",
			Endpoints: []service.Endpoint{
				{PodName: "nginx-a", IP: "10.244.0.7", Port: 80, TargetPort: 80, Protocol: "TCP"},
			},
		},
	}

	require.NoError(t, proxy.SyncAll(context.Background(), []*service.Service{svc}))

	joined := strings.Join(runner.commands, "\n")
	assert.Contains(t, joined, "-t nat -D PREROUTING -d 10.96.0.1/32 -p tcp -m tcp --dport 80 -j MK8S-SVC-STALE")
	assert.Contains(t, joined, "-t nat -D OUTPUT -d 10.96.0.1/32 -p tcp -m tcp --dport 80 -j MK8S-SVC-STALE")
	assert.Contains(t, joined, "-t nat -D POSTROUTING ! -s 10.244.0.0/16 -d 10.244.0.3/32 -p tcp -m tcp --dport 8080 -j MASQUERADE")
	assert.Contains(t, joined, "-t nat -F MK8S-SVC-STALE")
	assert.Contains(t, joined, "-t nat -X MK8S-SVC-STALE")
	assert.Less(t,
		indexCommandContaining(runner.commands, "-t nat -X MK8S-SVC-STALE"),
		indexCommandContaining(runner.commands, "-t nat -N MK8S-SVC-"),
	)
}

func TestIPTablesProxySyncAllDeletesServicesMissingFromSnapshot(t *testing.T) {
	runner := &recordingRunner{}
	proxy := NewIPTablesProxy(runner.Run)
	oldSvc := &service.Service{
		ObjectMeta: pod.ObjectMeta{Name: "old", Namespace: "default"},
		Spec: service.ServiceSpec{
			Type:  service.ServiceTypeClusterIP,
			Ports: []service.ServicePort{{Protocol: "TCP", Port: 80, TargetPort: 8080}},
		},
		Status: service.ServiceStatus{ClusterIP: "10.96.0.10"},
	}
	newSvc := &service.Service{
		ObjectMeta: pod.ObjectMeta{Name: "new", Namespace: "default"},
		Spec: service.ServiceSpec{
			Type:  service.ServiceTypeClusterIP,
			Ports: []service.ServicePort{{Protocol: "TCP", Port: 80, TargetPort: 8080}},
		},
		Status: service.ServiceStatus{ClusterIP: "10.96.0.11"},
	}

	require.NoError(t, proxy.SyncAll(context.Background(), []*service.Service{oldSvc}))
	runner.commands = nil
	require.NoError(t, proxy.SyncAll(context.Background(), []*service.Service{newSvc}))

	joined := strings.Join(runner.commands, "\n")
	assert.Contains(t, joined, "-t nat -D PREROUTING -p tcp -d 10.96.0.10 --dport 80 -j MK8S-SVC-")
	assert.Contains(t, joined, "-t nat -F MK8S-SVC-")
	assert.Contains(t, joined, "-t nat -X MK8S-SVC-")
}

func TestIPTablesProxySyncServiceDeletesOldEndpointMasqueradeRules(t *testing.T) {
	runner := &recordingRunner{}
	proxy := NewIPTablesProxy(runner.Run)
	oldSvc := &service.Service{
		ObjectMeta: pod.ObjectMeta{Name: "nginx", Namespace: "default"},
		Spec: service.ServiceSpec{
			Type: service.ServiceTypeNodePort,
			Ports: []service.ServicePort{{
				Protocol:   "TCP",
				Port:       80,
				TargetPort: 8080,
				NodePort:   30080,
			}},
		},
		Status: service.ServiceStatus{
			ClusterIP: "10.96.0.10",
			Endpoints: []service.Endpoint{
				{PodName: "nginx-a", IP: "10.244.0.2", Port: 80, TargetPort: 8080, Protocol: "TCP"},
			},
		},
	}
	newSvc := oldSvc.DeepCopy()
	newSvc.Status.Endpoints = []service.Endpoint{
		{PodName: "nginx-b", IP: "10.244.1.2", Port: 80, TargetPort: 8080, Protocol: "TCP"},
	}

	require.NoError(t, proxy.SyncService(context.Background(), oldSvc))
	runner.commands = nil
	require.NoError(t, proxy.SyncService(context.Background(), newSvc))

	joined := strings.Join(runner.commands, "\n")
	assert.Contains(t, joined, "-t nat -D POSTROUTING -p tcp ! -s 10.244.0.0/16 -d 10.244.0.2 --dport 8080 -j MASQUERADE")
	assert.Contains(t, joined, "-t nat -A POSTROUTING -p tcp ! -s 10.244.0.0/16 -d 10.244.1.2 --dport 8080 -j MASQUERADE")
}

func TestIPTablesProxyDeleteServiceIgnoresMissingRules(t *testing.T) {
	runner := &recordingRunner{}
	proxy := NewIPTablesProxy(runner.Run)
	svc := &service.Service{
		ObjectMeta: pod.ObjectMeta{Name: "nginx", Namespace: "default"},
		Spec: service.ServiceSpec{
			Type:  service.ServiceTypeClusterIP,
			Ports: []service.ServicePort{{Protocol: "TCP", Port: 80, TargetPort: 8080}},
		},
		Status: service.ServiceStatus{ClusterIP: "10.96.0.10"},
	}

	require.NoError(t, proxy.DeleteService(context.Background(), svc))

	joined := strings.Join(runner.commands, "\n")
	assert.Contains(t, joined, "-t nat -D PREROUTING -p tcp -d 10.96.0.10 --dport 80 -j MK8S-SVC-")
	assert.Contains(t, joined, "-t nat -F MK8S-SVC-")
	assert.Contains(t, joined, "-t nat -X MK8S-SVC-")
}

func TestIPTablesProxyDeleteServiceIgnoresMissingNFTableChain(t *testing.T) {
	runner := func(ctx context.Context, args ...string) error {
		_ = ctx
		command := strings.Join(args, " ")
		if strings.Contains(command, "-D ") {
			return errors.New("iptables v1.8.7 (nf_tables): Chain 'MK8S-SVC-DEADBEEF' does not exist")
		}
		return nil
	}
	proxy := NewIPTablesProxy(runner)
	svc := &service.Service{
		ObjectMeta: pod.ObjectMeta{Name: "nginx", Namespace: "default"},
		Spec: service.ServiceSpec{
			Type:  service.ServiceTypeClusterIP,
			Ports: []service.ServicePort{{Protocol: "TCP", Port: 80, TargetPort: 8080}},
		},
		Status: service.ServiceStatus{ClusterIP: "10.96.0.10"},
	}

	require.NoError(t, proxy.DeleteService(context.Background(), svc))
}

func TestIPTablesProxyDeleteServiceSkipsEmptyClusterIPRules(t *testing.T) {
	runner := &recordingRunner{}
	proxy := NewIPTablesProxy(runner.Run)
	svc := &service.Service{
		ObjectMeta: pod.ObjectMeta{Name: "fn-echo", Namespace: "default"},
		Spec: service.ServiceSpec{
			Type:  service.ServiceTypeClusterIP,
			Ports: []service.ServicePort{{Protocol: "TCP", Port: 8080, TargetPort: 8080}},
		},
	}

	require.NoError(t, proxy.DeleteService(context.Background(), svc))

	joined := strings.Join(runner.commands, "\n")
	assert.NotContains(t, joined, "-d  --dport")
	assert.NotContains(t, joined, "-D PREROUTING")
	assert.NotContains(t, joined, "-D OUTPUT")
	assert.Contains(t, joined, "-t nat -F MK8S-SVC-")
	assert.Contains(t, joined, "-t nat -X MK8S-SVC-")
}

func TestIPTablesProxyDeleteServiceRemovesDuplicateEntryRules(t *testing.T) {
	deleteAttempts := 0
	var commands []string
	runner := func(ctx context.Context, args ...string) error {
		_ = ctx
		command := strings.Join(args, " ")
		commands = append(commands, command)
		if strings.Contains(command, "-D PREROUTING") {
			deleteAttempts++
			if deleteAttempts <= 2 {
				return nil
			}
			return errors.New("Bad rule (does a matching rule exist in that chain?)")
		}
		if strings.Contains(command, "-D ") {
			return errors.New("Bad rule (does a matching rule exist in that chain?)")
		}
		return nil
	}
	proxy := NewIPTablesProxy(runner)
	svc := &service.Service{
		ObjectMeta: pod.ObjectMeta{Name: "nginx", Namespace: "default"},
		Spec: service.ServiceSpec{
			Type:  service.ServiceTypeClusterIP,
			Ports: []service.ServicePort{{Protocol: "TCP", Port: 80, TargetPort: 8080}},
		},
		Status: service.ServiceStatus{ClusterIP: "10.96.0.10"},
	}

	require.NoError(t, proxy.DeleteService(context.Background(), svc))

	assert.GreaterOrEqual(t, countCommandsContaining(commands, "-t nat -D PREROUTING"), 3)
	assert.Contains(t, strings.Join(commands, "\n"), "-t nat -X MK8S-SVC-")
}

func countCommandsContaining(commands []string, needle string) int {
	count := 0
	for _, command := range commands {
		if strings.Contains(command, needle) {
			count++
		}
	}
	return count
}

func indexCommandContaining(commands []string, needle string) int {
	for i, command := range commands {
		if strings.Contains(command, needle) {
			return i
		}
	}
	return -1
}
