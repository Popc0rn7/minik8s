package kubeproxy

import (
	"context"
	"crypto/sha1"
	"fmt"
	"os/exec"
	"sort"
	"strings"

	"minik8s/internal/service"
)

// IPTablesRunner applies one iptables command.
type IPTablesRunner func(ctx context.Context, args ...string) error
type IPTablesRuleLister func(ctx context.Context) (string, error)

// IPTablesProxy implements kube-proxy style Service forwarding with iptables.
type IPTablesProxy struct {
	runner     IPTablesRunner
	ruleLister IPTablesRuleLister
	known      map[string]*service.Service
	cleaned    bool
}

const defaultClusterPodCIDR = "10.244.0.0/16"

func NewIPTablesProxy(runner IPTablesRunner) *IPTablesProxy {
	ruleLister := IPTablesRuleLister(nil)
	if runner == nil {
		runner = runIPTables
		ruleLister = listNATRules
	}
	return &IPTablesProxy{
		runner:     runner,
		ruleLister: ruleLister,
		known:      make(map[string]*service.Service),
	}
}

func (p *IPTablesProxy) SyncAll(ctx context.Context, services []*service.Service) error {
	if !p.cleaned {
		if err := p.CleanupStaleRules(ctx); err != nil {
			return err
		}
		p.cleaned = true
	}
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

func (p *IPTablesProxy) CleanupStaleRules(ctx context.Context) error {
	if p.ruleLister == nil {
		return nil
	}
	rules, err := p.ruleLister(ctx)
	if err != nil {
		return nil
	}
	chains := map[string]struct{}{}
	for _, line := range strings.Split(rules, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) == 0 {
			continue
		}
		switch fields[0] {
		case "-A":
			chain := ""
			if len(fields) > 1 {
				chain = fields[1]
			}
			if chain == "" {
				continue
			}
			target := jumpTarget(fields)
			if strings.HasPrefix(chain, "MK8S-SVC-") {
				chains[chain] = struct{}{}
			}
			if strings.HasPrefix(target, "MK8S-SVC-") {
				chains[target] = struct{}{}
				args := append([]string{"-t", "nat", "-D", chain}, fields[2:]...)
				if err := p.runner(ctx, args...); err != nil && !isIPTablesMissingRule(err) {
					return err
				}
			}
			if chain == "POSTROUTING" && hasMinik8sMasqueradeRule(fields) {
				args := append([]string{"-t", "nat", "-D", chain}, fields[2:]...)
				if err := p.runner(ctx, args...); err != nil && !isIPTablesMissingRule(err) {
					return err
				}
			}
		case "-N", ":":
			if len(fields) > 1 && strings.HasPrefix(fields[1], "MK8S-SVC-") {
				chains[fields[1]] = struct{}{}
			}
		default:
			if strings.HasPrefix(fields[0], ":MK8S-SVC-") {
				chains[strings.TrimPrefix(fields[0], ":")] = struct{}{}
			}
		}
	}
	names := make([]string, 0, len(chains))
	for chain := range chains {
		names = append(names, chain)
	}
	sort.Strings(names)
	for _, chain := range names {
		if err := p.runner(ctx, "-t", "nat", "-F", chain); err != nil && !isIPTablesMissingRule(err) {
			return err
		}
		if err := p.runner(ctx, "-t", "nat", "-X", chain); err != nil && !isIPTablesMissingRule(err) {
			return err
		}
	}
	return nil
}

func (p *IPTablesProxy) SyncService(ctx context.Context, svc *service.Service) error {
	deleteTarget := svc
	if old := p.known[serviceKey(svc)]; old != nil {
		deleteTarget = old
	}
	if err := p.DeleteService(ctx, deleteTarget); err != nil {
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
		if err := p.appendEndpointMasqueradeRules(ctx, proto, endpoints); err != nil {
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
		for _, ep := range endpointsForPort(svc.Status.Endpoints, port.Port) {
			if err := p.deleteRuleUntilMissing(ctx, "POSTROUTING", "-p", proto, "!", "-s", defaultClusterPodCIDR, "-d", ep.IP, "--dport", fmt.Sprint(ep.TargetPort), "-j", "MASQUERADE"); err != nil && firstErr == nil {
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

func (p *IPTablesProxy) appendEndpointMasqueradeRules(ctx context.Context, proto string, endpoints []service.Endpoint) error {
	for _, ep := range endpoints {
		if err := p.runner(ctx, "-t", "nat", "-A", "POSTROUTING", "-p", proto, "!", "-s", defaultClusterPodCIDR, "-d", ep.IP, "--dport", fmt.Sprint(ep.TargetPort), "-j", "MASQUERADE"); err != nil {
			return err
		}
	}
	return nil
}

func jumpTarget(fields []string) string {
	for i := 0; i < len(fields)-1; i++ {
		if fields[i] == "-j" {
			return fields[i+1]
		}
	}
	return ""
}

func hasMinik8sMasqueradeRule(fields []string) bool {
	return jumpTarget(fields) == "MASQUERADE" &&
		containsFieldSequence(fields, "!", "-s", defaultClusterPodCIDR) &&
		containsField(fields, "-d") &&
		containsField(fields, "--dport")
}

func containsField(fields []string, needle string) bool {
	for _, field := range fields {
		if field == needle {
			return true
		}
	}
	return false
}

func containsFieldSequence(fields []string, sequence ...string) bool {
	if len(sequence) == 0 {
		return true
	}
	for i := 0; i <= len(fields)-len(sequence); i++ {
		matched := true
		for j, needle := range sequence {
			if fields[i+j] != needle {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
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

func listNATRules(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, "iptables", "-t", "nat", "-S")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("iptables -t nat -S: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func isIPTablesMissingRule(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "Bad rule") ||
		strings.Contains(msg, "No chain/target/match by that name") ||
		strings.Contains(msg, "does not exist") ||
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
