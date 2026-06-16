package kubeproxy

import (
	"context"
	"crypto/sha1"
	"fmt"
	"os/exec"
	"strings"

	"minik8s/internal/service"
)

// IPTablesRunner applies one iptables command.
type IPTablesRunner func(ctx context.Context, args ...string) error

// IPTablesProxy implements kube-proxy style Service forwarding with iptables.
type IPTablesProxy struct {
	runner IPTablesRunner
	known  map[string]*service.Service
}

func NewIPTablesProxy(runner IPTablesRunner) *IPTablesProxy {
	if runner == nil {
		runner = runIPTables
	}
	return &IPTablesProxy{runner: runner, known: make(map[string]*service.Service)}
}

func (p *IPTablesProxy) SyncAll(ctx context.Context, services []*service.Service) error {
	next := make(map[string]*service.Service, len(services))
	for _, svc := range services {
		if err := p.SyncService(ctx, svc); err != nil {
			return err
		}
		next[serviceKey(svc)] = svc.DeepCopy()
	}
	for key, svc := range p.known {
		if _, ok := next[key]; ok {
			continue
		}
		if err := p.DeleteService(ctx, svc); err != nil {
			return err
		}
	}
	p.known = next
	return nil
}

func (p *IPTablesProxy) SyncService(ctx context.Context, svc *service.Service) error {
	if err := p.DeleteService(ctx, svc); err != nil {
		return err
	}
	chain := serviceChainName(svc)
	if err := p.runner(ctx, "-t", "nat", "-N", chain); err != nil {
		return err
	}
	for _, port := range svc.Spec.Ports {
		proto := normalizedProtocol(port.Protocol)
		if err := p.appendClusterIPRules(ctx, chain, svc.Status.ClusterIP, port.Port, proto); err != nil {
			return err
		}
		endpoints := endpointsForPort(svc.Status.Endpoints, port.Port)
		if svc.Spec.Type == service.ServiceTypeNodePort && port.NodePort > 0 {
			if err := p.appendNodePortRules(ctx, chain, port.NodePort, proto); err != nil {
				return err
			}
			if err := p.appendEndpointRules(ctx, chain, port.NodePort, proto, endpoints); err != nil {
				return err
			}
		}
		if err := p.appendEndpointRules(ctx, chain, port.Port, proto, endpoints); err != nil {
			return err
		}
	}
	p.known[serviceKey(svc)] = svc.DeepCopy()
	return nil
}

func (p *IPTablesProxy) DeleteService(ctx context.Context, svc *service.Service) error {
	chain := serviceChainName(svc)
	var firstErr error
	for _, port := range svc.Spec.Ports {
		proto := normalizedProtocol(port.Protocol)
		if svc.Status.ClusterIP != "" {
			if err := p.deleteRuleUntilMissing(ctx, "PREROUTING", "-p", proto, "-d", svc.Status.ClusterIP, "--dport", fmt.Sprint(port.Port), "-j", chain); err != nil && firstErr == nil {
				firstErr = err
			}
			if err := p.deleteRuleUntilMissing(ctx, "OUTPUT", "-p", proto, "-d", svc.Status.ClusterIP, "--dport", fmt.Sprint(port.Port), "-j", chain); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		if svc.Spec.Type == service.ServiceTypeNodePort && port.NodePort > 0 {
			if err := p.deleteRuleUntilMissing(ctx, "PREROUTING", "-p", proto, "--dport", fmt.Sprint(port.NodePort), "-j", chain); err != nil && firstErr == nil {
				firstErr = err
			}
			if err := p.deleteRuleUntilMissing(ctx, "OUTPUT", "-p", proto, "--dport", fmt.Sprint(port.NodePort), "-j", chain); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	if err := p.runner(ctx, "-t", "nat", "-F", chain); err != nil && !isIPTablesMissingRule(err) && firstErr == nil {
		firstErr = err
	}
	if err := p.runner(ctx, "-t", "nat", "-X", chain); err != nil && !isIPTablesMissingRule(err) && firstErr == nil {
		firstErr = err
	}
	delete(p.known, serviceKey(svc))
	return firstErr
}

func (p *IPTablesProxy) deleteRuleUntilMissing(ctx context.Context, chain string, rule ...string) error {
	args := append([]string{"-t", "nat", "-D", chain}, rule...)
	for i := 0; i < 32; i++ {
		err := p.runner(ctx, args...)
		if err == nil {
			continue
		}
		if isIPTablesMissingRule(err) {
			return nil
		}
		return err
	}
	return fmt.Errorf("iptables delete did not converge for %s %s", chain, strings.Join(rule, " "))
}

func (p *IPTablesProxy) appendClusterIPRules(ctx context.Context, chain, clusterIP string, port int32, proto string) error {
	if clusterIP == "" {
		return nil
	}
	args := []string{"-t", "nat", "-A", "PREROUTING", "-p", proto, "-d", clusterIP, "--dport", fmt.Sprint(port), "-j", chain}
	if err := p.runner(ctx, args...); err != nil {
		return err
	}
	return p.runner(ctx, "-t", "nat", "-A", "OUTPUT", "-p", proto, "-d", clusterIP, "--dport", fmt.Sprint(port), "-j", chain)
}

func (p *IPTablesProxy) appendNodePortRules(ctx context.Context, chain string, nodePort int32, proto string) error {
	if err := p.runner(ctx, "-t", "nat", "-A", "PREROUTING", "-p", proto, "--dport", fmt.Sprint(nodePort), "-j", chain); err != nil {
		return err
	}
	return p.runner(ctx, "-t", "nat", "-A", "OUTPUT", "-p", proto, "--dport", fmt.Sprint(nodePort), "-j", chain)
}

func (p *IPTablesProxy) appendEndpointRules(ctx context.Context, chain string, servicePort int32, proto string, endpoints []service.Endpoint) error {
	for i, ep := range endpoints {
		args := []string{"-t", "nat", "-A", chain, "-p", proto, "--dport", fmt.Sprint(servicePort)}
		if i < len(endpoints)-1 {
			probability := 1.0 / float64(len(endpoints)-i)
			args = append(args, "-m", "statistic", "--mode", "random", "--probability", fmt.Sprintf("%.6f", probability))
		}
		args = append(args, "-j", "DNAT", "--to-destination", fmt.Sprintf("%s:%d", ep.IP, ep.TargetPort))
		if err := p.runner(ctx, args...); err != nil {
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

func normalizedProtocol(protocol string) string {
	proto := strings.ToLower(protocol)
	if proto == "" {
		return "tcp"
	}
	return proto
}

func runIPTables(ctx context.Context, args ...string) error {
	args = append([]string{"-w", "5"}, args...)
	cmd := exec.CommandContext(ctx, "iptables", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("iptables %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func isIPTablesMissingRule(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "Bad rule") ||
		strings.Contains(msg, "No chain/target/match by that name") ||
		strings.Contains(msg, "No such file or directory") ||
		strings.Contains(msg, "does a matching rule exist")
}

func serviceChainName(svc *service.Service) string {
	sum := sha1.Sum([]byte(svc.Namespace + "/" + svc.Name))
	return fmt.Sprintf("MK8S-SVC-%X", sum[:6])
}

func serviceKey(svc *service.Service) string {
	if svc == nil {
		return "/"
	}
	namespace := svc.Namespace
	if namespace == "" {
		namespace = "default"
	}
	return namespace + "/" + svc.Name
}
