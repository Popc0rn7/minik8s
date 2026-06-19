package service

import (
	"encoding/binary"
	"fmt"
	"net"
	"strconv"
	"strings"
)

const (
	DefaultServiceCIDR   = "10.96.0.0/12"
	DefaultNodePortRange = "30000-32767"
)

type AllocatorConfig struct {
	ServiceCIDR   string
	NodePortRange string
}

type Allocator struct {
	serviceCIDR *net.IPNet
	nodePortMin int32
	nodePortMax int32
}

func NewAllocator(config AllocatorConfig) (*Allocator, error) {
	serviceCIDR := strings.TrimSpace(config.ServiceCIDR)
	if serviceCIDR == "" {
		serviceCIDR = DefaultServiceCIDR
	}
	_, network, err := net.ParseCIDR(serviceCIDR)
	if err != nil {
		return nil, fmt.Errorf("invalid service CIDR %q: %w", serviceCIDR, err)
	}
	if network.IP.To4() == nil {
		return nil, fmt.Errorf("service CIDR %q must be IPv4", serviceCIDR)
	}
	network.IP = network.IP.To4()

	nodePortRange := strings.TrimSpace(config.NodePortRange)
	if nodePortRange == "" {
		nodePortRange = DefaultNodePortRange
	}
	minPort, maxPort, err := parseNodePortRange(nodePortRange)
	if err != nil {
		return nil, err
	}
	return &Allocator{
		serviceCIDR: network,
		nodePortMin: minPort,
		nodePortMax: maxPort,
	}, nil
}

func (a *Allocator) Assign(svc *Service, existing []*Service) error {
	if svc == nil {
		return fmt.Errorf("service is nil")
	}
	if a == nil {
		var err error
		a, err = NewAllocator(AllocatorConfig{})
		if err != nil {
			return err
		}
	}
	svcNS := serviceNamespace(svc.Namespace)
	sameService := func(item *Service) bool {
		return item != nil && item.Name == svc.Name && serviceNamespace(item.Namespace) == svcNS
	}

	for _, item := range existing {
		if sameService(item) {
			if svc.Status.ClusterIP == "" && item.Status.ClusterIP != "" {
				svc.Status.ClusterIP = item.Status.ClusterIP
			}
			a.preserveNodePorts(svc, item)
			break
		}
	}

	if err := a.assignClusterIP(svc, existing, sameService); err != nil {
		return err
	}
	if svc.Spec.Type == ServiceTypeNodePort {
		return a.assignNodePorts(svc, existing, sameService)
	}
	return nil
}

func (a *Allocator) assignClusterIP(svc *Service, existing []*Service, sameService func(*Service) bool) error {
	used := make(map[string]string, len(existing))
	for _, item := range existing {
		if item == nil || item.Status.ClusterIP == "" || sameService(item) {
			continue
		}
		used[item.Status.ClusterIP] = serviceNamespace(item.Namespace) + "/" + item.Name
	}
	if svc.Status.ClusterIP != "" {
		if err := a.validateClusterIP(svc.Status.ClusterIP); err != nil {
			return err
		}
		if owner := used[svc.Status.ClusterIP]; owner != "" {
			return fmt.Errorf("clusterIP %s conflicts with service %s", svc.Status.ClusterIP, owner)
		}
		return nil
	}
	for ip := firstUsable(a.serviceCIDR); a.serviceCIDR.Contains(ip); ip = nextIP(ip) {
		if isNetworkOrBroadcast(ip, a.serviceCIDR) {
			continue
		}
		candidate := ip.String()
		if used[candidate] == "" {
			svc.Status.ClusterIP = candidate
			return nil
		}
	}
	return fmt.Errorf("no available service ClusterIP in %s", a.serviceCIDR.String())
}

func (a *Allocator) validateClusterIP(value string) error {
	ip := net.ParseIP(value)
	if ip == nil || ip.To4() == nil {
		return fmt.Errorf("clusterIP %q must be IPv4", value)
	}
	if !a.serviceCIDR.Contains(ip.To4()) || isNetworkOrBroadcast(ip.To4(), a.serviceCIDR) {
		return fmt.Errorf("clusterIP %s is outside service CIDR %s", value, a.serviceCIDR.String())
	}
	return nil
}

func (a *Allocator) preserveNodePorts(svc *Service, existing *Service) {
	if existing == nil {
		return
	}
	for i := range svc.Spec.Ports {
		if svc.Spec.Ports[i].NodePort != 0 {
			continue
		}
		for _, oldPort := range existing.Spec.Ports {
			if oldPort.Port == svc.Spec.Ports[i].Port && strings.EqualFold(oldPort.Protocol, svc.Spec.Ports[i].Protocol) && oldPort.NodePort != 0 {
				svc.Spec.Ports[i].NodePort = oldPort.NodePort
				break
			}
		}
	}
}

func (a *Allocator) assignNodePorts(svc *Service, existing []*Service, sameService func(*Service) bool) error {
	used := make(map[int32]string)
	for _, item := range existing {
		if item == nil || sameService(item) {
			continue
		}
		for _, port := range item.Spec.Ports {
			if port.NodePort != 0 {
				used[port.NodePort] = serviceNamespace(item.Namespace) + "/" + item.Name
			}
		}
	}
	for i := range svc.Spec.Ports {
		port := &svc.Spec.Ports[i]
		if port.NodePort != 0 {
			if port.NodePort < a.nodePortMin || port.NodePort > a.nodePortMax {
				return fmt.Errorf("nodePort %d is outside nodePort range %d-%d", port.NodePort, a.nodePortMin, a.nodePortMax)
			}
			if owner := used[port.NodePort]; owner != "" {
				return fmt.Errorf("nodePort %d conflicts with service %s", port.NodePort, owner)
			}
			used[port.NodePort] = serviceNamespace(svc.Namespace) + "/" + svc.Name
			continue
		}
		allocated := false
		for candidate := a.nodePortMin; candidate <= a.nodePortMax; candidate++ {
			if used[candidate] != "" {
				continue
			}
			port.NodePort = candidate
			used[candidate] = serviceNamespace(svc.Namespace) + "/" + svc.Name
			allocated = true
			break
		}
		if !allocated {
			return fmt.Errorf("no available nodePort in range %d-%d", a.nodePortMin, a.nodePortMax)
		}
	}
	return nil
}

func EnsureClusterIP(svc *Service, existing []*Service) error {
	allocator, err := NewAllocator(AllocatorConfig{})
	if err != nil {
		return err
	}
	return allocator.Assign(svc, existing)
}

func parseNodePortRange(value string) (int32, int32, error) {
	parts := strings.Split(value, "-")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("nodePort range %q must be min-max", value)
	}
	minPort, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return 0, 0, fmt.Errorf("invalid nodePort range %q: %w", value, err)
	}
	maxPort, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return 0, 0, fmt.Errorf("invalid nodePort range %q: %w", value, err)
	}
	if minPort <= 0 || maxPort > 65535 || minPort > maxPort {
		return 0, 0, fmt.Errorf("nodePort range %q must be within 1-65535", value)
	}
	return int32(minPort), int32(maxPort), nil
}

func serviceNamespace(namespace string) string {
	if namespace == "" {
		return "default"
	}
	return namespace
}

func firstUsable(network *net.IPNet) net.IP {
	return nextIP(network.IP)
}

func nextIP(ip net.IP) net.IP {
	value := binary.BigEndian.Uint32(ip.To4())
	next := make(net.IP, net.IPv4len)
	binary.BigEndian.PutUint32(next, value+1)
	return next
}

func isNetworkOrBroadcast(ip net.IP, network *net.IPNet) bool {
	if ip.Equal(network.IP) {
		return true
	}
	ones, bits := network.Mask.Size()
	if bits != 32 || ones == 32 {
		return false
	}
	broadcast := append(net.IP(nil), network.IP.To4()...)
	for idx := range broadcast {
		broadcast[idx] |= ^network.Mask[idx]
	}
	return ip.Equal(broadcast)
}
