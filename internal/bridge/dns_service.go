package bridge

import (
	"errors"
	"fmt"
	"strings"

	store "minik8s/internal/bridge/logbook"
	"minik8s/internal/pod"
	"minik8s/internal/service"
)

const (
	ClusterDNSServiceName      = "minik8s-dns"
	ClusterDNSServiceNamespace = "minik8s-system"
	InternalAnnotation         = "minik8s.internal"
)

// EnsureClusterDNSService creates or updates the Service used as Pod nameserver.
// Pods query the Service on port 53; kube-proxy forwards to the DNS addon host port.
func (k *Bridge) EnsureClusterDNSService(gatewayIP string, dnsHostPort int32) (string, error) {
	gatewayIP = strings.TrimSpace(gatewayIP)
	if gatewayIP == "" {
		return "", fmt.Errorf("dns gateway IP is required")
	}
	if dnsHostPort <= 0 {
		return "", fmt.Errorf("dns host port must be positive")
	}
	existing, err := k.serviceStore.List("", nil)
	if err != nil {
		return "", fmt.Errorf("listing services: %w", err)
	}
	svc := clusterDNSService(gatewayIP, dnsHostPort)
	for _, item := range existing {
		if item != nil && item.Name == ClusterDNSServiceName && item.Namespace == ClusterDNSServiceNamespace {
			svc.Status.ClusterIP = item.Status.ClusterIP
			break
		}
	}
	allocator, err := service.NewAllocator(service.AllocatorConfig{
		ServiceCIDR:   k.serviceCIDR,
		NodePortRange: k.nodePortRange,
	})
	if err != nil {
		return "", err
	}
	if err := allocator.Assign(svc, existing); err != nil {
		return "", err
	}
	if err := k.serviceStore.Create(svc); err != nil {
		if !errors.Is(err, store.ErrServiceAlreadyExists) {
			return "", fmt.Errorf("creating cluster DNS service: %w", err)
		}
		if err := k.serviceStore.Update(svc); err != nil {
			return "", fmt.Errorf("updating cluster DNS service: %w", err)
		}
	}
	return svc.Status.ClusterIP, nil
}

func clusterDNSService(gatewayIP string, dnsHostPort int32) *service.Service {
	return &service.Service{
		TypeMeta: pod.TypeMeta{Kind: "Service", APIVersion: "v1"},
		ObjectMeta: pod.ObjectMeta{
			Name:      ClusterDNSServiceName,
			Namespace: ClusterDNSServiceNamespace,
			Labels: map[string]string{
				"app":          ClusterDNSServiceName,
				"minik8s.kind": "cluster-dns",
			},
			Annotations: map[string]string{
				InternalAnnotation: "true",
			},
		},
		Spec: service.ServiceSpec{
			Type: service.ServiceTypeClusterIP,
			Selector: pod.LabelSelector{MatchLabels: map[string]string{
				"app": ClusterDNSServiceName,
			}},
			Ports: []service.ServicePort{{
				Name:       "dns-udp",
				Protocol:   "UDP",
				Port:       53,
				TargetPort: dnsHostPort,
			}, {
				Name:       "dns-tcp",
				Protocol:   "TCP",
				Port:       53,
				TargetPort: dnsHostPort,
			}},
		},
		Status: service.ServiceStatus{
			Endpoints: []service.Endpoint{{
				IP:         gatewayIP,
				Port:       53,
				TargetPort: dnsHostPort,
				Protocol:   "UDP",
			}, {
				IP:         gatewayIP,
				Port:       53,
				TargetPort: dnsHostPort,
				Protocol:   "TCP",
			}},
		},
	}
}
