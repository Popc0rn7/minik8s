package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"minik8s/internal/cliui"
	"minik8s/internal/node"
	"minik8s/internal/pod"
	"minik8s/internal/service"
)

// NewRootCommand builds the Cobra command tree for minik8s.
func NewRootCommand(app *App, out io.Writer) *cobra.Command {
	if out == nil {
		out = io.Discard
	}
	var server string
	var namespace string

	root := &cobra.Command{
		Use:           "minik8s",
		Short:         "A small Kubernetes-like lab control plane",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Usage()
		},
	}
	root.SetOut(out)
	root.SetErr(out)
	root.PersistentFlags().StringVar(&server, "server", "", "Harbor API server URL")
	root.PersistentFlags().StringVarP(&namespace, "namespace", "n", "default", "Namespace for namespaced resources")

	bind := func() {
		app.server = server
		app.namespace = namespace
	}

	root.AddCommand(newApplyCommand(app, out, bind))
	root.AddCommand(newGetCommand(app, out, bind))
	root.AddCommand(newDeleteCommand(app, out, bind))
	root.AddCommand(newDescribeCommand(app, out, bind))
	root.AddCommand(newAPIResourcesCommand(app, out, bind))
	root.AddCommand(newVersionCommand(app, out, bind))
	root.AddCommand(newDoctorCommand(app, out))
	root.AddCommand(newCNICommand(app, out))
	root.AddCommand(newNetRegistryCommand(app, out))
	root.AddCommand(newNetDCommand(app, out))
	root.AddCommand(newBridgeCommand(app, out))
	root.AddCommand(newSailerCommand(app, out))
	return root
}

func newApplyCommand(app *App, out io.Writer, bind func()) *cobra.Command {
	var filename string
	cmd := &cobra.Command{
		Use:   "apply -f <manifest>",
		Short: "Apply a Pod or Service manifest",
		RunE: func(cmd *cobra.Command, args []string) error {
			bind()
			if filename == "" {
				return fmt.Errorf("missing required -f flag")
			}
			return app.apply(cmd.Context(), []string{"-f", filename}, out)
		},
	}
	cmd.Flags().StringVarP(&filename, "filename", "f", "", "Manifest file to apply")
	return cmd
}

func newGetCommand(app *App, out io.Writer, bind func()) *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:     "get pods|services|nodes [name]",
		Aliases: []string{"list"},
		Short:   "Get resources",
		Args:    cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			bind()
			ref, err := parseResourceRef(args, true)
			if err != nil {
				return err
			}
			return app.getResource(cmd.Context(), ref, output, out)
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "table", "Output format: table, json, yaml")
	return cmd
}

func newDeleteCommand(app *App, out io.Writer, bind func()) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete pod|service <name>",
		Short: "Delete a Pod or Service",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			bind()
			ref, err := parseResourceRef(args, false)
			if err != nil {
				return err
			}
			switch ref.resource {
			case resourcePods:
				if err := app.delete(cmd.Context(), []string{"pod", ref.name, "-n", app.namespace}, out); err != nil {
					return err
				}
			case resourceServices:
				if err := app.delete(cmd.Context(), []string{"service", ref.name, "-n", app.namespace}, out); err != nil {
					return err
				}
			default:
				return fmt.Errorf("delete supports pods and services")
			}
			return nil
		},
	}
	return cmd
}

func newDescribeCommand(app *App, out io.Writer, bind func()) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "describe pod|service|node <name>",
		Short: "Show resource details",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			bind()
			ref, err := parseResourceRef(args, false)
			if err != nil {
				return err
			}
			return app.describeResource(cmd.Context(), ref, out)
		},
	}
	return cmd
}

func newAPIResourcesCommand(app *App, out io.Writer, bind func()) *cobra.Command {
	return &cobra.Command{
		Use:   "api-resources",
		Short: "Print supported API resources",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			bind()
			client, err := app.controlPlaneClient()
			if err != nil {
				return err
			}
			resources, err := client.APIResources(cmd.Context())
			if err != nil {
				return err
			}
			items, _ := resources["resources"].([]any)
			if err := writef(out, "%s %s %s\n", cliui.PadRight("NAME", 18), cliui.PadRight("NAMESPACED", 12), "KIND"); err != nil {
				return err
			}
			for _, item := range items {
				row, _ := item.(map[string]any)
				if err := writef(out, "%s %s %s\n",
					cliui.PadRight(fmt.Sprint(row["name"]), 18),
					cliui.PadRight(fmt.Sprint(row["namespaced"]), 12),
					fmt.Sprint(row["kind"]),
				); err != nil {
					return err
				}
			}
			return nil
		},
	}
}

func newVersionCommand(app *App, out io.Writer, bind func()) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print Harbor version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			bind()
			client, err := app.controlPlaneClient()
			if err != nil {
				return err
			}
			version, err := client.Version(cmd.Context())
			if err != nil {
				return err
			}
			return writeObject(out, "json", version)
		},
	}
}

func newDoctorCommand(app *App, out io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor docker|network|logbook",
		Short: "Run diagnostics",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.doctor(cmd.Context(), args, out)
		},
	}
}

func newCNICommand(app *App, out io.Writer) *cobra.Command {
	cmd := &cobra.Command{Use: "cni", Short: "Manage CNI configuration"}
	var podCIDR, gateway string
	var routes []string
	initCmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize CNI configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			legacy := []string{"init"}
			if podCIDR != "" {
				legacy = append(legacy, "--pod-cidr", podCIDR)
			}
			if gateway != "" {
				legacy = append(legacy, "--gateway", gateway)
			}
			for _, route := range routes {
				legacy = append(legacy, "--route", route)
			}
			return app.cni(cmd.Context(), legacy, out)
		},
	}
	initCmd.Flags().StringVar(&podCIDR, "pod-cidr", "", "Pod CIDR for this node")
	initCmd.Flags().StringVar(&gateway, "gateway", "", "Bridge gateway IP")
	initCmd.Flags().StringArrayVar(&routes, "route", nil, "Static host-gw route <remote-cidr>=<node-ip>")
	cmd.AddCommand(initCmd)
	return cmd
}

func newNetRegistryCommand(app *App, out io.Writer) *cobra.Command {
	var listen, leaseTTL string
	cmd := &cobra.Command{
		Use:   "net-registry",
		Short: "Run the lightweight network registry",
		RunE: func(cmd *cobra.Command, args []string) error {
			legacy := []string{}
			if listen != "" {
				legacy = append(legacy, "--listen", listen)
			}
			if leaseTTL != "" {
				legacy = append(legacy, "--lease-ttl", leaseTTL)
			}
			return app.netRegistry(cmd.Context(), legacy, out)
		},
	}
	cmd.Flags().StringVar(&listen, "listen", "", "Listen address")
	cmd.Flags().StringVar(&leaseTTL, "lease-ttl", "", "Lease TTL")
	return cmd
}

func newNetDCommand(app *App, out io.Writer) *cobra.Command {
	var nodeName, nodeIP, podCIDR, registry, interval string
	var vxlanID, vxlanPort int
	var vxlanName string
	var once bool
	cmd := &cobra.Command{
		Use:   "netd",
		Short: "Run the VXLAN route sync agent",
		RunE: func(cmd *cobra.Command, args []string) error {
			legacy := []string{}
			appendFlag := func(name, value string) {
				if value != "" {
					legacy = append(legacy, name, value)
				}
			}
			appendIntFlag := func(name string, value int) {
				if value != 0 {
					legacy = append(legacy, name, fmt.Sprintf("%d", value))
				}
			}
			appendFlag("--node-name", nodeName)
			appendFlag("--node-ip", nodeIP)
			appendFlag("--pod-cidr", podCIDR)
			appendFlag("--registry", registry)
			appendFlag("--interval", interval)
			appendIntFlag("--vxlan-id", vxlanID)
			appendIntFlag("--vxlan-port", vxlanPort)
			appendFlag("--vxlan-name", vxlanName)
			if once {
				legacy = append(legacy, "--once")
			}
			return app.netd(cmd.Context(), legacy, out)
		},
	}
	cmd.Flags().StringVar(&nodeName, "node-name", "", "Node name")
	cmd.Flags().StringVar(&nodeIP, "node-ip", "", "Node host IP")
	cmd.Flags().StringVar(&podCIDR, "pod-cidr", "", "Pod CIDR")
	cmd.Flags().StringVar(&registry, "registry", "", "Network registry URL")
	cmd.Flags().StringVar(&interval, "interval", "", "Sync interval")
	cmd.Flags().IntVar(&vxlanID, "vxlan-id", 0, "VXLAN network identifier")
	cmd.Flags().IntVar(&vxlanPort, "vxlan-port", 0, "VXLAN UDP port")
	cmd.Flags().StringVar(&vxlanName, "vxlan-name", "", "VXLAN device name")
	cmd.Flags().BoolVar(&once, "once", false, "Run one sync and exit")
	return cmd
}

func newBridgeCommand(app *App, out io.Writer) *cobra.Command {
	var listen string
	cmd := &cobra.Command{
		Use:   "bridge",
		Short: "Run the control plane",
		RunE: func(cmd *cobra.Command, args []string) error {
			legacy := []string{}
			if listen != "" {
				legacy = append(legacy, "--listen", listen)
			}
			return app.bridge(cmd.Context(), legacy, out)
		},
	}
	cmd.Flags().StringVar(&listen, "listen", "", "Listen address")
	return cmd
}

func newSailerCommand(app *App, out io.Writer) *cobra.Command {
	var harbor, interval string
	var vxlanID, vxlanPort int
	var vxlanName string
	var once, proxyDisabled bool
	cmd := &cobra.Command{
		Use:   "sailer <node.yaml>",
		Short: "Run the worker node agent",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			legacy := []string{args[0]}
			appendFlag := func(name, value string) {
				if value != "" {
					legacy = append(legacy, name, value)
				}
			}
			appendIntFlag := func(name string, value int) {
				if value != 0 {
					legacy = append(legacy, name, fmt.Sprintf("%d", value))
				}
			}
			appendFlag("--harbor", harbor)
			appendFlag("--interval", interval)
			appendIntFlag("--vxlan-id", vxlanID)
			appendIntFlag("--vxlan-port", vxlanPort)
			appendFlag("--vxlan-name", vxlanName)
			if once {
				legacy = append(legacy, "--once")
			}
			if proxyDisabled {
				legacy = append(legacy, "--proxy-disabled")
			}
			return app.sailer(cmd.Context(), legacy, out)
		},
	}
	cmd.Flags().StringVar(&harbor, "harbor", "", "Harbor URL")
	cmd.Flags().StringVar(&interval, "interval", "", "Sync interval")
	cmd.Flags().IntVar(&vxlanID, "vxlan-id", 0, "VXLAN network identifier")
	cmd.Flags().IntVar(&vxlanPort, "vxlan-port", 0, "VXLAN UDP port")
	cmd.Flags().StringVar(&vxlanName, "vxlan-name", "", "VXLAN device name")
	cmd.Flags().BoolVar(&once, "once", false, "Run one sync and exit")
	cmd.Flags().BoolVar(&proxyDisabled, "proxy-disabled", false, "Disable node-local Service proxy sync")
	return cmd
}

type resourceName string

const (
	resourcePods     resourceName = "pods"
	resourceServices resourceName = "services"
	resourceNodes    resourceName = "nodes"
)

type resourceRef struct {
	resource resourceName
	name     string
}

func parseResourceRef(args []string, nameOptional bool) (resourceRef, error) {
	resourcePart := args[0]
	name := ""
	if before, after, ok := strings.Cut(resourcePart, "/"); ok {
		resourcePart = before
		name = after
	}
	if len(args) == 2 {
		if name != "" {
			return resourceRef{}, fmt.Errorf("resource name specified twice")
		}
		name = args[1]
	}
	if name == "" && !nameOptional {
		return resourceRef{}, fmt.Errorf("resource name is required")
	}
	resource, err := normalizeResource(resourcePart)
	if err != nil {
		return resourceRef{}, err
	}
	return resourceRef{resource: resource, name: name}, nil
}

func normalizeResource(resource string) (resourceName, error) {
	switch strings.ToLower(resource) {
	case "pod", "pods", "po":
		return resourcePods, nil
	case "service", "services", "svc":
		return resourceServices, nil
	case "node", "nodes", "no":
		return resourceNodes, nil
	default:
		return "", fmt.Errorf("unsupported resource %q", resource)
	}
}

func (a *App) getResource(ctx context.Context, ref resourceRef, output string, out io.Writer) error {
	client, err := a.controlPlaneClient()
	if err != nil {
		return err
	}
	switch ref.resource {
	case resourcePods:
		if ref.name != "" {
			p, err := client.GetPod(ctx, ref.name, a.namespace)
			if err != nil {
				return err
			}
			if output != "table" {
				return writeObject(out, output, p)
			}
			return writePodTable(out, []*pod.Pod{p})
		}
		pods, err := client.ListPods(ctx, a.namespace)
		if err != nil {
			return err
		}
		sortPodList(pods)
		if output != "table" {
			return writeObject(out, output, map[string]any{"kind": "PodList", "apiVersion": "v1", "items": pods})
		}
		return writePodTable(out, pods)
	case resourceServices:
		if ref.name != "" {
			svc, err := client.GetService(ctx, ref.name, a.namespace)
			if err != nil {
				return err
			}
			if output != "table" {
				return writeObject(out, output, svc)
			}
			return writeServiceTable(out, []*service.Service{svc})
		}
		services, err := client.ListServices(ctx, a.namespace)
		if err != nil {
			return err
		}
		sortServiceList(services)
		if output != "table" {
			return writeObject(out, output, map[string]any{"kind": "ServiceList", "apiVersion": "v1", "items": services})
		}
		return writeServiceTable(out, services)
	case resourceNodes:
		if ref.name != "" {
			n, err := client.GetNode(ctx, ref.name)
			if err != nil {
				return err
			}
			if output != "table" {
				return writeObject(out, output, n)
			}
			return writeNodeTable(out, []node.Node{*n})
		}
		nodes, err := client.ListNodes(ctx)
		if err != nil {
			return err
		}
		sortNodeList(nodes)
		if output != "table" {
			return writeObject(out, output, map[string]any{"kind": "NodeList", "apiVersion": "v1", "items": nodes})
		}
		return writeNodeTable(out, nodes)
	default:
		return fmt.Errorf("unsupported resource %q", ref.resource)
	}
}

func (a *App) describeResource(ctx context.Context, ref resourceRef, out io.Writer) error {
	client, err := a.controlPlaneClient()
	if err != nil {
		return err
	}
	switch ref.resource {
	case resourcePods:
		p, err := client.GetPod(ctx, ref.name, a.namespace)
		if err != nil {
			return err
		}
		return describePod(out, p)
	case resourceServices:
		svc, err := client.GetService(ctx, ref.name, a.namespace)
		if err != nil {
			return err
		}
		return describeService(out, svc)
	case resourceNodes:
		n, err := client.GetNode(ctx, ref.name)
		if err != nil {
			return err
		}
		return describeNode(out, n)
	default:
		return fmt.Errorf("unsupported resource %q", ref.resource)
	}
}

func writeObject(out io.Writer, output string, value any) error {
	switch output {
	case "json":
		data, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(out, "%s\n", data)
		return err
	case "yaml":
		data, err := yaml.Marshal(value)
		if err != nil {
			return err
		}
		_, err = out.Write(data)
		return err
	case "table", "":
		return fmt.Errorf("table output requires a resource table")
	default:
		return fmt.Errorf("unsupported output format %q", output)
	}
}

func sortPodList(pods []*pod.Pod) {
	sort.Slice(pods, func(i, j int) bool {
		if pods[i].Namespace == pods[j].Namespace {
			return pods[i].Name < pods[j].Name
		}
		return pods[i].Namespace < pods[j].Namespace
	})
}

func sortServiceList(services []*service.Service) {
	sort.Slice(services, func(i, j int) bool {
		if services[i].Namespace == services[j].Namespace {
			return services[i].Name < services[j].Name
		}
		return services[i].Namespace < services[j].Namespace
	})
}

func sortNodeList(nodes []node.Node) {
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].Name() < nodes[j].Name()
	})
}

func writePodTable(out io.Writer, pods []*pod.Pod) error {
	if err := writef(out, "%s %s %s %s %s %s\n",
		cliui.PadRight("POD", 31),
		cliui.PadRight("STATUS", 18),
		cliui.PadRight("IP", 15),
		cliui.PadRight("UPTIME", 10),
		cliui.PadRight("NAMESPACE", 14),
		"LABELS",
	); err != nil {
		return err
	}
	for _, p := range pods {
		podName := fmt.Sprintf("%s  %s", cliui.Icon(cliui.IconPod, "[pod]"), p.Name)
		status := fmt.Sprintf("%s %s", cliui.StatusIcon(p.Status.Phase), p.Status.Phase)
		if err := writef(out, "%s %s %s %s %s %s\n",
			cliui.PadRight(podName, 31),
			cliui.PadRight(status, 18),
			formatPodIP(p.Status.PodIP),
			cliui.PadRight(formatUptime(p.Status), 10),
			cliui.PadRight(p.Namespace, 14),
			formatLabels(p.Labels),
		); err != nil {
			return err
		}
	}
	return nil
}

func writeServiceTable(out io.Writer, services []*service.Service) error {
	if err := writef(out, "%s %s %s %s %s %s %s %s\n",
		cliui.PadRight("SERVICE", 31),
		cliui.PadRight("TYPE", 12),
		cliui.PadRight("CLUSTER-IP", 16),
		cliui.PadRight("PORTS", 18),
		cliui.PadRight("ENDPOINTS", 28),
		cliui.PadRight("SELECTOR", 22),
		cliui.PadRight("NAMESPACE", 14),
		"LABELS",
	); err != nil {
		return err
	}
	for _, svc := range services {
		serviceName := fmt.Sprintf("%s  %s", cliui.Icon(cliui.IconInfo, "[svc]"), svc.Name)
		if err := writef(out, "%s %s %s %s %s %s %s %s\n",
			cliui.PadRight(serviceName, 31),
			cliui.PadRight(string(svc.Spec.Type), 12),
			cliui.PadRight(svc.Status.ClusterIP, 16),
			cliui.PadRight(formatServicePorts(svc), 18),
			cliui.PadRight(formatServiceEndpoints(svc.Status.Endpoints), 28),
			cliui.PadRight(formatServiceSelector(svc.Spec.Selector), 22),
			cliui.PadRight(svc.Namespace, 14),
			formatLabels(svc.Labels),
		); err != nil {
			return err
		}
	}
	return nil
}

func writeNodeTable(out io.Writer, nodes []node.Node) error {
	if err := writef(out, "%s %s %s %s %s %s %s %s\n",
		cliui.PadRight("NODE", 31),
		cliui.PadRight("ROLE", 14),
		cliui.PadRight("STATUS", 14),
		cliui.PadRight("IP", 15),
		cliui.PadRight("PODCIDR", 18),
		cliui.PadRight("CPU", 8),
		cliui.PadRight("MEMORY", 10),
		"AGE",
	); err != nil {
		return err
	}
	for _, n := range nodes {
		nodeName := fmt.Sprintf("%s  %s", cliui.Icon(cliui.IconInfo, "[node]"), n.Name())
		if err := writef(out, "%s %s %s %s %s %s %s %s\n",
			cliui.PadRight(nodeName, 31),
			cliui.PadRight(string(n.Spec.Role), 14),
			cliui.PadRight(formatNodeStatus(n.Status.Phase), 14),
			cliui.PadRight(emptyDash(n.InternalIP()), 15),
			cliui.PadRight(emptyDash(n.Spec.PodCIDR), 18),
			cliui.PadRight(emptyDash(n.Status.Allocatable.CPU), 8),
			cliui.PadRight(emptyDash(n.Status.Allocatable.Memory), 10),
			formatNodeAge(n),
		); err != nil {
			return err
		}
	}
	return nil
}

func describePod(out io.Writer, p *pod.Pod) error {
	lines := []string{
		fmt.Sprintf("Name: %s", p.Name),
		fmt.Sprintf("Namespace: %s", p.Namespace),
		fmt.Sprintf("Status: %s", p.Status.Phase),
		fmt.Sprintf("IP: %s", formatPodIP(p.Status.PodIP)),
		fmt.Sprintf("Node: %s", emptyDash(p.Spec.NodeName)),
		fmt.Sprintf("Labels: %s", formatLabels(p.Labels)),
	}
	for _, line := range lines {
		if err := writef(out, "%s\n", line); err != nil {
			return err
		}
	}
	return nil
}

func describeService(out io.Writer, svc *service.Service) error {
	lines := []string{
		fmt.Sprintf("Name: %s", svc.Name),
		fmt.Sprintf("Namespace: %s", svc.Namespace),
		fmt.Sprintf("Type: %s", svc.Spec.Type),
		fmt.Sprintf("ClusterIP: %s", emptyDash(svc.Status.ClusterIP)),
		fmt.Sprintf("Ports: %s", formatServicePorts(svc)),
		fmt.Sprintf("Endpoints: %s", formatServiceEndpoints(svc.Status.Endpoints)),
		fmt.Sprintf("Labels: %s", formatLabels(svc.Labels)),
	}
	for _, line := range lines {
		if err := writef(out, "%s\n", line); err != nil {
			return err
		}
	}
	return nil
}

func describeNode(out io.Writer, n *node.Node) error {
	labelMap := n.LabelMap()
	labels := make([]string, 0, len(labelMap))
	for key, value := range labelMap {
		labels = append(labels, fmt.Sprintf("%s=%s", key, value))
	}
	sort.Strings(labels)
	if len(labels) == 0 {
		labels = append(labels, "-")
	}
	lines := []string{
		fmt.Sprintf("Name: %s", n.Name()),
		fmt.Sprintf("Role: %s", n.Spec.Role),
		fmt.Sprintf("Status: %s", n.Status.Phase),
		fmt.Sprintf("InternalIP: %s", emptyDash(n.InternalIP())),
		fmt.Sprintf("PodCIDR: %s", emptyDash(n.Spec.PodCIDR)),
		fmt.Sprintf("Capacity: cpu=%s memory=%s", emptyDash(n.Spec.Capacity.CPU), emptyDash(n.Spec.Capacity.Memory)),
		fmt.Sprintf("Allocatable: cpu=%s memory=%s", emptyDash(n.Status.Allocatable.CPU), emptyDash(n.Status.Allocatable.Memory)),
		fmt.Sprintf("Conditions: %s", formatNodeConditions(n.Status.Conditions)),
		fmt.Sprintf("LastHeartbeat: %s", formatNodeLastHeartbeat(n.Status.LastHeartbeat)),
		fmt.Sprintf("Age: %s", formatNodeAge(*n)),
		fmt.Sprintf("Labels: %s", strings.Join(labels, ",")),
	}
	for _, line := range lines {
		if err := writef(out, "%s\n", line); err != nil {
			return err
		}
	}
	return nil
}

func formatNodeConditions(conditions []node.NodeCondition) string {
	if len(conditions) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(conditions))
	for _, cond := range conditions {
		parts = append(parts, fmt.Sprintf("%s=%s", cond.Type, cond.Status))
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

func formatNodeLastHeartbeat(last time.Time) string {
	if last.IsZero() {
		return "-"
	}
	return last.Format(time.RFC3339)
}

func emptyDash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}
