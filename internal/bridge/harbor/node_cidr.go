package harbor

import (
	"encoding/binary"
	"fmt"
	"net"

	"minik8s/internal/node"
)

const (
	defaultClusterCIDR      = "10.244.0.0/16"
	defaultNodeCIDRMaskSize = 24
)

type nodeCIDRAllocator struct {
	cluster *net.IPNet
	mask    int
}

func newNodeCIDRAllocator(clusterCIDR string, maskSize int) (*nodeCIDRAllocator, error) {
	if clusterCIDR == "" {
		clusterCIDR = defaultClusterCIDR
	}
	if maskSize == 0 {
		maskSize = defaultNodeCIDRMaskSize
	}
	ip, network, err := net.ParseCIDR(clusterCIDR)
	if err != nil {
		return nil, fmt.Errorf("invalid cluster CIDR %q: %w", clusterCIDR, err)
	}
	ip4 := ip.To4()
	if ip4 == nil {
		return nil, fmt.Errorf("cluster CIDR %q must be IPv4", clusterCIDR)
	}
	ones, bits := network.Mask.Size()
	if bits != 32 {
		return nil, fmt.Errorf("cluster CIDR %q must be IPv4", clusterCIDR)
	}
	if maskSize < ones || maskSize > bits {
		return nil, fmt.Errorf("node CIDR mask size %d must be between %d and %d", maskSize, ones, bits)
	}
	network.IP = ip4
	return &nodeCIDRAllocator{cluster: network, mask: maskSize}, nil
}

func (a *nodeCIDRAllocator) assign(name string, nodes []node.Node) (string, error) {
	for _, n := range nodes {
		if n.Name() == name && n.Spec.PodCIDR != "" {
			return n.Spec.PodCIDR, nil
		}
	}
	used := map[string]struct{}{}
	for _, n := range nodes {
		if n.Spec.PodCIDR != "" {
			used[n.Spec.PodCIDR] = struct{}{}
		}
	}
	ones, _ := a.cluster.Mask.Size()
	count := uint64(1) << uint(a.mask-ones)
	base := uint64(binary.BigEndian.Uint32(a.cluster.IP.To4()))
	step := uint64(1) << uint(32-a.mask)
	for i := uint64(0); i < count; i++ {
		ip := make(net.IP, 4)
		binary.BigEndian.PutUint32(ip, uint32(base+i*step))
		candidate := (&net.IPNet{IP: ip, Mask: net.CIDRMask(a.mask, 32)}).String()
		if _, ok := used[candidate]; ok {
			continue
		}
		return candidate, nil
	}
	return "", fmt.Errorf("no free node PodCIDR in %s/%d", a.cluster.String(), a.mask)
}
