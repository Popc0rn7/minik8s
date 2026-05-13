package controller

import (
	"context"
	"crypto/sha1"
	"fmt"
	"os/exec"
	"strings"

	"minik8s/internal/service"
)

type IPTablesServiceProxy struct{}

func NewIPTablesServiceProxy() *IPTablesServiceProxy {
	return &IPTablesServiceProxy{}
}

func (p *IPTablesServiceProxy) ApplyService(ctx context.Context, svc *service.Service) error {
	if err := p.DeleteService(ctx, svc); err != nil {
		return err
	}
	chain := serviceChainName(svc)
	if err := runIPTables(ctx, "-t", "nat", "-N", chain); err != nil {
		return err
	}
	for _, port := range svc.Spec.Ports {
		proto := strings.ToLower(port.Protocol)
		if proto == "" {
			proto = "tcp"
		}
		if err := appendClusterIPRules(ctx, chain, svc.Status.ClusterIP, port.Port, proto); err != nil {
			return err
		}
		if svc.Spec.Type == service.ServiceTypeNodePort && port.NodePort > 0 {
			if err := appendNodePortRules(ctx, chain, port.NodePort, proto); err != nil {
				return err
			}
			if err := appendEndpointRules(ctx, chain, port.NodePort, proto, endpointsForPort(svc.Status.Endpoints, port.Port)); err != nil {
				return err
			}
		}
		endpoints := endpointsForPort(svc.Status.Endpoints, port.Port)
		if err := appendEndpointRules(ctx, chain, port.Port, proto, endpoints); err != nil {
			return err
		}
	}
	return nil
}

func (p *IPTablesServiceProxy) DeleteService(ctx context.Context, svc *service.Service) error {
	chain := serviceChainName(svc)
	for _, port := range svc.Spec.Ports {
		proto := strings.ToLower(port.Protocol)
		if proto == "" {
			proto = "tcp"
		}
		_ = runIPTables(ctx, "-t", "nat", "-D", "PREROUTING", "-p", proto, "-d", svc.Status.ClusterIP, "--dport", fmt.Sprint(port.Port), "-j", chain)
		_ = runIPTables(ctx, "-t", "nat", "-D", "OUTPUT", "-p", proto, "-d", svc.Status.ClusterIP, "--dport", fmt.Sprint(port.Port), "-j", chain)
		if svc.Spec.Type == service.ServiceTypeNodePort && port.NodePort > 0 {
			_ = runIPTables(ctx, "-t", "nat", "-D", "PREROUTING", "-p", proto, "--dport", fmt.Sprint(port.NodePort), "-j", chain)
			_ = runIPTables(ctx, "-t", "nat", "-D", "OUTPUT", "-p", proto, "--dport", fmt.Sprint(port.NodePort), "-j", chain)
		}
	}
	_ = runIPTables(ctx, "-t", "nat", "-F", chain)
	_ = runIPTables(ctx, "-t", "nat", "-X", chain)
	return nil
}

func appendClusterIPRules(ctx context.Context, chain, clusterIP string, port int32, proto string) error {
	if clusterIP == "" {
		return nil
	}
	args := []string{"-t", "nat", "-A", "PREROUTING", "-p", proto, "-d", clusterIP, "--dport", fmt.Sprint(port), "-j", chain}
	if err := runIPTables(ctx, args...); err != nil {
		return err
	}
	return runIPTables(ctx, "-t", "nat", "-A", "OUTPUT", "-p", proto, "-d", clusterIP, "--dport", fmt.Sprint(port), "-j", chain)
}

func appendNodePortRules(ctx context.Context, chain string, nodePort int32, proto string) error {
	if err := runIPTables(ctx, "-t", "nat", "-A", "PREROUTING", "-p", proto, "--dport", fmt.Sprint(nodePort), "-j", chain); err != nil {
		return err
	}
	return runIPTables(ctx, "-t", "nat", "-A", "OUTPUT", "-p", proto, "--dport", fmt.Sprint(nodePort), "-j", chain)
}

func appendEndpointRules(ctx context.Context, chain string, servicePort int32, proto string, endpoints []service.Endpoint) error {
	for i, ep := range endpoints {
		args := []string{"-t", "nat", "-A", chain, "-p", proto, "--dport", fmt.Sprint(servicePort)}
		if i < len(endpoints)-1 {
			probability := 1.0 / float64(len(endpoints)-i)
			args = append(args, "-m", "statistic", "--mode", "random", "--probability", fmt.Sprintf("%.6f", probability))
		}
		args = append(args, "-j", "DNAT", "--to-destination", fmt.Sprintf("%s:%d", ep.IP, ep.TargetPort))
		if err := runIPTables(ctx, args...); err != nil {
			return err
		}
	}
	return nil
}

func endpointsForPort(endpoints []service.Endpoint, port int32) []service.Endpoint {
	result := make([]service.Endpoint, 0, len(endpoints))
	for _, ep := range endpoints {
		if ep.Port == port {
			result = append(result, ep)
		}
	}
	return result
}

func runIPTables(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "iptables", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("iptables %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func serviceChainName(svc *service.Service) string {
	sum := sha1.Sum([]byte(svc.Namespace + "/" + svc.Name))
	return fmt.Sprintf("MK8S-SVC-%X", sum[:6])
}
