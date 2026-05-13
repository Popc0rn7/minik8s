package kubecaptain

import (
	"context"
	"fmt"
	"sort"
	"strings"

	store "minik8s/internal/kubebridge/etcd"
	"minik8s/internal/kubeproxy"
	"minik8s/internal/minilog"
	"minik8s/internal/pod"
	"minik8s/internal/service"
)

type ServiceProxy = kubeproxy.Proxy

func NewIPTablesServiceProxy() *kubeproxy.IPTablesProxy {
	return kubeproxy.NewIPTablesProxy(nil)
}

type ServiceKubecaptain struct {
	podStore     store.PodStore
	serviceStore store.ServiceStore
	proxy        ServiceProxy
}

func NewServiceKubecaptain(podStore store.PodStore, serviceStore store.ServiceStore, proxy ServiceProxy) *ServiceKubecaptain {
	return &ServiceKubecaptain{
		podStore:     podStore,
		serviceStore: serviceStore,
		proxy:        proxy,
	}
}

func (c *ServiceKubecaptain) Sync(ctx context.Context) error {
	services, err := c.serviceStore.List("", nil)
	if err != nil {
		return fmt.Errorf("listing services: %w", err)
	}
	sort.Slice(services, func(i, j int) bool {
		if services[i].Namespace == services[j].Namespace {
			return services[i].Name < services[j].Name
		}
		return services[i].Namespace < services[j].Namespace
	})
	for _, svc := range services {
		if err := c.reconcileService(ctx, svc); err != nil {
			return err
		}
	}
	return nil
}

func (c *ServiceKubecaptain) DeleteService(ctx context.Context, name, namespace string) error {
	svc, err := c.serviceStore.Get(name, namespace)
	if err != nil {
		return err
	}
	if c.proxy != nil {
		if err := c.proxy.DeleteService(ctx, svc); err != nil {
			return fmt.Errorf("deleting service proxy rules: %w", err)
		}
	}
	return c.serviceStore.Delete(name, namespace)
}

func (c *ServiceKubecaptain) reconcileService(ctx context.Context, svc *service.Service) error {
	pods, err := c.podStore.List(svc.Namespace, &svc.Spec.Selector)
	if err != nil {
		return fmt.Errorf("listing selected pods: %w", err)
	}
	endpoints := make([]service.Endpoint, 0)
	for _, p := range pods {
		if p.Status.Phase != pod.PodRunning || p.Status.PodIP == "" {
			continue
		}
		for _, port := range svc.Spec.Ports {
			endpoints = append(endpoints, service.Endpoint{
				PodName:    p.Name,
				IP:         p.Status.PodIP,
				Port:       port.Port,
				TargetPort: port.TargetPort,
				Protocol:   port.Protocol,
			})
		}
	}
	sort.Slice(endpoints, func(i, j int) bool {
		if endpoints[i].PodName == endpoints[j].PodName {
			return endpoints[i].TargetPort < endpoints[j].TargetPort
		}
		return endpoints[i].PodName < endpoints[j].PodName
	})
	svc.Status.Endpoints = endpoints
	if svc.Status.ClusterIP == "" {
		svc.Status.ClusterIP = service.DefaultClusterIP
	}
	if err := c.serviceStore.Update(svc); err != nil {
		return fmt.Errorf("updating service status: %w", err)
	}
	if c.proxy != nil {
		if err := c.proxy.SyncService(ctx, svc); err != nil {
			return fmt.Errorf("applying service proxy rules: %w", err)
		}
	}
	minilog.Info("service-sync", "service=%s/%s endpoints=%s", svc.Namespace, svc.Name, endpointSummary(endpoints))
	return nil
}

func endpointSummary(endpoints []service.Endpoint) string {
	if len(endpoints) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(endpoints))
	for _, ep := range endpoints {
		parts = append(parts, fmt.Sprintf("%s:%d", ep.IP, ep.TargetPort))
	}
	return strings.Join(parts, ",")
}
