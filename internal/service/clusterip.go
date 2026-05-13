package service

import (
	"fmt"
)

const DefaultClusterIP = "10.96.0.1"

func EnsureClusterIP(svc *Service, existing []*Service) error {
	if svc == nil {
		return fmt.Errorf("service is nil")
	}
	used := make(map[string]bool, len(existing))
	for _, item := range existing {
		if item == nil {
			continue
		}
		if item.Name == svc.Name && serviceNamespace(item.Namespace) == serviceNamespace(svc.Namespace) {
			if item.Status.ClusterIP != "" {
				svc.Status.ClusterIP = item.Status.ClusterIP
			}
			continue
		}
		if item.Status.ClusterIP != "" {
			used[item.Status.ClusterIP] = true
		}
	}
	if svc.Status.ClusterIP != "" && !used[svc.Status.ClusterIP] {
		return nil
	}
	for i := 1; i < 255; i++ {
		candidate := fmt.Sprintf("10.96.0.%d", i)
		if !used[candidate] {
			svc.Status.ClusterIP = candidate
			return nil
		}
	}
	return fmt.Errorf("no available service ClusterIP")
}

func serviceNamespace(namespace string) string {
	if namespace == "" {
		return "default"
	}
	return namespace
}
