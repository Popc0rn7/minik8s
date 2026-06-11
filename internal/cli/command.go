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
	"minik8s/internal/dns"
	"minik8s/internal/eventtrigger"
	"minik8s/internal/function"
	"minik8s/internal/hpa"
	"minik8s/internal/metrics"
	"minik8s/internal/node"
	"minik8s/internal/pod"
	"minik8s/internal/replicaset"
	"minik8s/internal/service"
	"minik8s/internal/workflow"
)

// NewRootCommand builds the Cobra command tree for the minik8s runtime/admin binary.
func NewRootCommand(app *App, out io.Writer) *cobra.Command {
	if out == nil {
		out = io.Discard
	}
	var server string
	var namespace string

	root := &cobra.Command{
		Use:           "minik8s",
		Short:         "Run and diagnose Minik8s components",
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

	addRuntimeCommands(root, app, out, bind)
	return root
}

// NewKubectlCommand builds the Kubernetes-style user command tree.
func NewKubectlCommand(app *App, out io.Writer) *cobra.Command {
	if out == nil {
		out = io.Discard
	}
	var server string
	var namespace string

	root := &cobra.Command{
		Use:           "kubectl",
		Short:         "Control Minik8s resources through the Harbor API",
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

	addKubectlCommands(root, app, out, bind)
	return root
}

func newCompatCommand(app *App, out io.Writer) *cobra.Command {
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
	addKubectlCommands(root, app, out, bind)
	addRuntimeCommands(root, app, out, bind)
	return root
}

func addKubectlCommands(root *cobra.Command, app *App, out io.Writer, bind func()) {
	root.AddCommand(newApplyCommand(app, out, bind))
	root.AddCommand(newGetCommand(app, out, bind))
	root.AddCommand(newTopCommand(app, out, bind))
	root.AddCommand(newDeleteCommand(app, out, bind))
	root.AddCommand(newDescribeCommand(app, out, bind))
	root.AddCommand(newAPIResourcesCommand(app, out, bind))
	root.AddCommand(newVersionCommand(app, out, bind))
}

func addRuntimeCommands(root *cobra.Command, app *App, out io.Writer, bind func()) {
	root.AddCommand(newInvokeCommand(app, out, bind))
	root.AddCommand(newPublishCommand(app, out))
	root.AddCommand(newInitCommand(app, out))
	root.AddCommand(newDoctorCommand(app, out))
	root.AddCommand(newCNICommand(app, out))
	root.AddCommand(newNetRegistryCommand(app, out))
	root.AddCommand(newNetDCommand(app, out))
	root.AddCommand(newRouteProxyCommand(app, out))
	root.AddCommand(newBridgeCommand(app, out))
	root.AddCommand(newSailerCommand(app, out))
}

func newInitCommand(app *App, out io.Writer) *cobra.Command {
	var force bool
	var dnsListen, ingressListen string
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize local Minik8s startup files",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			legacy := []string{}
			if force {
				legacy = append(legacy, "--force")
			}
			if dnsListen != "" {
				legacy = append(legacy, "--dns-listen", dnsListen)
			}
			if ingressListen != "" {
				legacy = append(legacy, "--ingress-listen", ingressListen)
			}
			return app.initialize(cmd.Context(), legacy, out)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "Overwrite generated startup files")
	cmd.Flags().StringVar(&dnsListen, "dns-listen", "", "DNS host listen port/address")
	cmd.Flags().StringVar(&ingressListen, "ingress-listen", "", "HTTP ingress host listen port/address")
	return cmd
}

func newPublishCommand(app *App, out io.Writer) *cobra.Command {
	var data string
	cmd := &cobra.Command{
		Use:   "publish <subject>",
		Short: "Publish a message to the configured NATS subject",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.publishNATS(cmd.Context(), args[0], data, out)
		},
	}
	cmd.Flags().StringVar(&data, "data", "", "Message payload")
	return cmd
}

func newInvokeCommand(app *App, out io.Writer, bind func()) *cobra.Command {
	var data string
	cmd := &cobra.Command{
		Use:   "invoke function <name>",
		Short: "Invoke a serverless function",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			bind()
			if args[0] != "function" && args[0] != "fn" {
				return fmt.Errorf("invoke supports function")
			}
			return app.invokeFunction(cmd.Context(), args[1], data, out)
		},
	}
	cmd.Flags().StringVar(&data, "data", "", "Invocation payload")
	return cmd
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
		Use:     "get pods|services|dns|replicasets|hpa|nodes [name]",
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
		Use:   "delete pod|service|dns|replicaset|hpa <name>",
		Short: "Delete a Pod, Service, DNS, ReplicaSet, or HPA",
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
			case resourceDNS:
				if err := app.delete(cmd.Context(), []string{"dns", ref.name, "-n", app.namespace}, out); err != nil {
					return err
				}
			case resourceReplicaSets:
				if err := app.delete(cmd.Context(), []string{"replicaset", ref.name, "-n", app.namespace}, out); err != nil {
					return err
				}
			case resourceHPAs:
				if err := app.delete(cmd.Context(), []string{"hpa", ref.name, "-n", app.namespace}, out); err != nil {
					return err
				}
			case resourceFunctions:
				if err := app.delete(cmd.Context(), []string{"function", ref.name, "-n", app.namespace}, out); err != nil {
					return err
				}
			case resourceEventTriggers:
				if err := app.delete(cmd.Context(), []string{"eventtrigger", ref.name, "-n", app.namespace}, out); err != nil {
					return err
				}
			case resourceWorkflows:
				if err := app.delete(cmd.Context(), []string{"workflow", ref.name, "-n", app.namespace}, out); err != nil {
					return err
				}
			default:
				return fmt.Errorf("delete supports pods, services, dns, replicasets, hpas, functions, eventtriggers, and workflows")
			}
			return nil
		},
	}
	return cmd
}

func newDescribeCommand(app *App, out io.Writer, bind func()) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "describe pod|service|dns|replicaset|hpa|node <name>",
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

func newTopCommand(app *App, out io.Writer, bind func()) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "top nodes|pods",
		Short: "Display resource usage",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			bind()
			switch strings.ToLower(args[0]) {
			case "pods", "pod", "po":
				return app.topPods(cmd.Context(), out)
			case "nodes", "node", "no":
				return app.topNodes(cmd.Context(), out)
			default:
				return fmt.Errorf("top supports pods or nodes")
			}
		},
	}
	return cmd
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
		Use:   "doctor docker|network|clean|logbook|serverless|addons|addon <name>",
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

func newRouteProxyCommand(app *App, out io.Writer) *cobra.Command {
	var listen string
	var routes string
	cmd := &cobra.Command{
		Use:   "route-proxy",
		Short: "Run the Minik8s DNS HTTP route proxy",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.routeProxy(cmd.Context(), listen, routes, out)
		},
	}
	cmd.Flags().StringVar(&listen, "listen", "127.0.0.1:18081", "Route proxy listen address")
	cmd.Flags().StringVar(&routes, "routes", DefaultDNSRoutesPath(), "DNS route snapshot path")
	return cmd
}

func newBridgeCommand(app *App, out io.Writer) *cobra.Command {
	var listen, addons, serviceSyncInterval, dnsSyncInterval, replicaSetSyncInterval, hpaSyncInterval, clusterCIDR, gatewayIP, dnsListen, ingressListen string
	var nodeCIDRMaskSize int
	cmd := &cobra.Command{
		Use:   "bridge",
		Short: "Run the control plane",
		RunE: func(cmd *cobra.Command, args []string) error {
			legacy := []string{}
			if listen != "" {
				legacy = append(legacy, "--listen", listen)
			}
			if addons != "" {
				legacy = append(legacy, "--addons", addons)
			}
			if serviceSyncInterval != "" {
				legacy = append(legacy, "--service-sync-interval", serviceSyncInterval)
			}
			if dnsSyncInterval != "" {
				legacy = append(legacy, "--dns-sync-interval", dnsSyncInterval)
			}
			if gatewayIP != "" {
				legacy = append(legacy, "--gateway-ip", gatewayIP)
			}
			if dnsListen != "" {
				legacy = append(legacy, "--dns-listen", dnsListen)
			}
			if ingressListen != "" {
				legacy = append(legacy, "--ingress-listen", ingressListen)
			}
			if replicaSetSyncInterval != "" {
				legacy = append(legacy, "--replicaset-sync-interval", replicaSetSyncInterval)
			}
			if hpaSyncInterval != "" {
				legacy = append(legacy, "--hpa-sync-interval", hpaSyncInterval)
			}
			if clusterCIDR != "" {
				legacy = append(legacy, "--cluster-cidr", clusterCIDR)
			}
			if nodeCIDRMaskSize != 0 {
				legacy = append(legacy, "--node-cidr-mask-size", fmt.Sprintf("%d", nodeCIDRMaskSize))
			}
			return app.bridge(cmd.Context(), legacy, out)
		},
	}
	cmd.Flags().StringVar(&listen, "listen", "", "Listen address")
	cmd.Flags().StringVar(&addons, "addons", "", "Comma-separated addons to run: dns, metrics, serverless, or none")
	cmd.Flags().StringVar(&serviceSyncInterval, "service-sync-interval", "", "Service sync interval")
	cmd.Flags().StringVar(&dnsSyncInterval, "dns-sync-interval", "", "DNS sync interval")
	cmd.Flags().StringVar(&gatewayIP, "gateway-ip", "", "DNS answer gateway IP")
	cmd.Flags().StringVar(&dnsListen, "dns-listen", "", "DNS host listen port/address")
	cmd.Flags().StringVar(&ingressListen, "ingress-listen", "", "HTTP ingress host listen port/address")
	cmd.Flags().StringVar(&replicaSetSyncInterval, "replicaset-sync-interval", "", "ReplicaSet sync interval")
	cmd.Flags().StringVar(&hpaSyncInterval, "hpa-sync-interval", "", "HPA sync interval")
	cmd.Flags().StringVar(&clusterCIDR, "cluster-cidr", "", "Cluster CIDR for Node PodCIDR allocation")
	cmd.Flags().IntVar(&nodeCIDRMaskSize, "node-cidr-mask-size", 0, "Per-node PodCIDR mask size")
	return cmd
}

func newSailerCommand(app *App, out io.Writer) *cobra.Command {
	var harbor, interval, clusterDNS string
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
			appendFlag("--cluster-dns", clusterDNS)
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
	cmd.Flags().StringVar(&clusterDNS, "cluster-dns", "", "Cluster DNS nameserver IP")
	cmd.Flags().IntVar(&vxlanID, "vxlan-id", 0, "VXLAN network identifier")
	cmd.Flags().IntVar(&vxlanPort, "vxlan-port", 0, "VXLAN UDP port")
	cmd.Flags().StringVar(&vxlanName, "vxlan-name", "", "VXLAN device name")
	cmd.Flags().BoolVar(&once, "once", false, "Run one sync and exit")
	cmd.Flags().BoolVar(&proxyDisabled, "proxy-disabled", false, "Disable node-local Service proxy sync")
	return cmd
}

type resourceName string

const (
	resourcePods          resourceName = "pods"
	resourceServices      resourceName = "services"
	resourceDNS           resourceName = "dns"
	resourceReplicaSets   resourceName = "replicasets"
	resourceHPAs          resourceName = "horizontalpodautoscalers"
	resourceNodes         resourceName = "nodes"
	resourceFunctions     resourceName = "functions"
	resourceEventTriggers resourceName = "eventtriggers"
	resourceWorkflows     resourceName = "workflows"
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
	case "dns":
		return resourceDNS, nil
	case "replicaset", "replicasets", "rs":
		return resourceReplicaSets, nil
	case "hpa", "hpas", "horizontalpodautoscaler", "horizontalpodautoscalers":
		return resourceHPAs, nil
	case "node", "nodes", "no":
		return resourceNodes, nil
	case "function", "functions", "fn":
		return resourceFunctions, nil
	case "eventtrigger", "eventtriggers", "trigger", "triggers":
		return resourceEventTriggers, nil
	case "workflow", "workflows", "wf":
		return resourceWorkflows, nil
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
	case resourceDNS:
		if ref.name != "" {
			d, err := client.GetDNS(ctx, ref.name, a.namespace)
			if err != nil {
				return err
			}
			if output != "table" {
				return writeObject(out, output, d)
			}
			return writeDNSTable(out, []*dns.DNS{d})
		}
		items, err := client.ListDNS(ctx, a.namespace)
		if err != nil {
			return err
		}
		sortDNSList(items)
		if output != "table" {
			return writeObject(out, output, map[string]any{"kind": "DNSList", "apiVersion": "v1", "items": items})
		}
		return writeDNSTable(out, items)
	case resourceReplicaSets:
		if ref.name != "" {
			rs, err := client.GetReplicaSet(ctx, ref.name, a.namespace)
			if err != nil {
				return err
			}
			if output != "table" {
				return writeObject(out, output, rs)
			}
			return writeReplicaSetTable(out, []*replicaset.ReplicaSet{rs})
		}
		replicaSets, err := client.ListReplicaSets(ctx, a.namespace)
		if err != nil {
			return err
		}
		sortReplicaSetList(replicaSets)
		if output != "table" {
			return writeObject(out, output, map[string]any{"kind": "ReplicaSetList", "apiVersion": "v1", "items": replicaSets})
		}
		return writeReplicaSetTable(out, replicaSets)
	case resourceHPAs:
		if ref.name != "" {
			autoscaler, err := client.GetHPA(ctx, ref.name, a.namespace)
			if err != nil {
				return err
			}
			if output != "table" {
				return writeObject(out, output, autoscaler)
			}
			return writeHPATable(out, []*hpa.HorizontalPodAutoscaler{autoscaler})
		}
		hpas, err := client.ListHPAs(ctx, a.namespace)
		if err != nil {
			return err
		}
		sortHPAList(hpas)
		if output != "table" {
			return writeObject(out, output, map[string]any{"kind": "HorizontalPodAutoscalerList", "apiVersion": "v1", "items": hpas})
		}
		return writeHPATable(out, hpas)
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
	case resourceFunctions:
		if ref.name != "" {
			fn, err := client.GetFunction(ctx, ref.name, a.namespace)
			if err != nil {
				return err
			}
			if output != "table" {
				return writeObject(out, output, fn)
			}
			return writeFunctionTable(out, []*function.Function{fn})
		}
		functions, err := client.ListFunctions(ctx, a.namespace)
		if err != nil {
			return err
		}
		sortFunctionList(functions)
		if output != "table" {
			return writeObject(out, output, map[string]any{"kind": "FunctionList", "apiVersion": "v1", "items": functions})
		}
		return writeFunctionTable(out, functions)
	case resourceEventTriggers:
		if ref.name != "" {
			trigger, err := client.GetEventTrigger(ctx, ref.name, a.namespace)
			if err != nil {
				return err
			}
			if output != "table" {
				return writeObject(out, output, trigger)
			}
			return writeEventTriggerTable(out, []*eventtrigger.EventTrigger{trigger})
		}
		triggers, err := client.ListEventTriggers(ctx, a.namespace)
		if err != nil {
			return err
		}
		sortEventTriggerList(triggers)
		if output != "table" {
			return writeObject(out, output, map[string]any{"kind": "EventTriggerList", "apiVersion": "v1", "items": triggers})
		}
		return writeEventTriggerTable(out, triggers)
	case resourceWorkflows:
		if ref.name != "" {
			wf, err := client.GetWorkflow(ctx, ref.name, a.namespace)
			if err != nil {
				return err
			}
			if output != "table" {
				return writeObject(out, output, wf)
			}
			return writeWorkflowTable(out, []*workflow.Workflow{wf})
		}
		workflows, err := client.ListWorkflows(ctx, a.namespace)
		if err != nil {
			return err
		}
		sortWorkflowList(workflows)
		if output != "table" {
			return writeObject(out, output, map[string]any{"kind": "WorkflowList", "apiVersion": "v1", "items": workflows})
		}
		return writeWorkflowTable(out, workflows)
	default:
		return fmt.Errorf("unsupported resource %q", ref.resource)
	}
}

func (a *App) topPods(ctx context.Context, out io.Writer) error {
	client, err := a.controlPlaneClient()
	if err != nil {
		return err
	}
	list, err := client.ListPodMetrics(ctx)
	if err != nil {
		return err
	}
	if err := writef(out, "%s %s %s\n", cliui.PadRight("NAME", 28), cliui.PadRight("CPU", 10), "MEMORY"); err != nil {
		return err
	}
	for _, item := range list.Items {
		if item.Metadata.Namespace != "" && item.Metadata.Namespace != a.namespace {
			continue
		}
		usage := metrics.SumPodUsage(item)
		if err := writef(out, "%s %s %s\n",
			cliui.PadRight(item.Metadata.Name, 28),
			cliui.PadRight(metricOrDash(usage[metrics.ResourceCPU]), 10),
			metricOrDash(usage[metrics.ResourceMemory]),
		); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) topNodes(ctx context.Context, out io.Writer) error {
	client, err := a.controlPlaneClient()
	if err != nil {
		return err
	}
	list, err := client.ListNodeMetrics(ctx)
	if err != nil {
		return err
	}
	if err := writef(out, "%s %s %s\n", cliui.PadRight("NAME", 28), cliui.PadRight("CPU", 10), "MEMORY"); err != nil {
		return err
	}
	for _, item := range list.Items {
		if err := writef(out, "%s %s %s\n",
			cliui.PadRight(item.Metadata.Name, 28),
			cliui.PadRight(metricOrDash(item.Usage[metrics.ResourceCPU]), 10),
			metricOrDash(item.Usage[metrics.ResourceMemory]),
		); err != nil {
			return err
		}
	}
	return nil
}

func metricOrDash(value string) string {
	if value == "" {
		return "-"
	}
	return value
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
	case resourceDNS:
		d, err := client.GetDNS(ctx, ref.name, a.namespace)
		if err != nil {
			return err
		}
		return describeDNS(out, d)
	case resourceReplicaSets:
		rs, err := client.GetReplicaSet(ctx, ref.name, a.namespace)
		if err != nil {
			return err
		}
		return describeReplicaSet(out, rs)
	case resourceHPAs:
		autoscaler, err := client.GetHPA(ctx, ref.name, a.namespace)
		if err != nil {
			return err
		}
		return describeHPA(out, autoscaler)
	case resourceNodes:
		n, err := client.GetNode(ctx, ref.name)
		if err != nil {
			return err
		}
		return describeNode(out, n)
	case resourceFunctions:
		fn, err := client.GetFunction(ctx, ref.name, a.namespace)
		if err != nil {
			return err
		}
		return describeFunction(out, fn)
	case resourceEventTriggers:
		trigger, err := client.GetEventTrigger(ctx, ref.name, a.namespace)
		if err != nil {
			return err
		}
		return describeEventTrigger(out, trigger)
	case resourceWorkflows:
		wf, err := client.GetWorkflow(ctx, ref.name, a.namespace)
		if err != nil {
			return err
		}
		return describeWorkflow(out, wf)
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

func sortDNSList(items []*dns.DNS) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Namespace == items[j].Namespace {
			return items[i].Name < items[j].Name
		}
		return items[i].Namespace < items[j].Namespace
	})
}

func sortReplicaSetList(replicaSets []*replicaset.ReplicaSet) {
	sort.Slice(replicaSets, func(i, j int) bool {
		if replicaSets[i].Namespace == replicaSets[j].Namespace {
			return replicaSets[i].Name < replicaSets[j].Name
		}
		return replicaSets[i].Namespace < replicaSets[j].Namespace
	})
}

func sortHPAList(hpas []*hpa.HorizontalPodAutoscaler) {
	sort.Slice(hpas, func(i, j int) bool {
		if hpas[i].Namespace == hpas[j].Namespace {
			return hpas[i].Name < hpas[j].Name
		}
		return hpas[i].Namespace < hpas[j].Namespace
	})
}

func sortNodeList(nodes []node.Node) {
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].Name() < nodes[j].Name()
	})
}

func sortFunctionList(functions []*function.Function) {
	sort.Slice(functions, func(i, j int) bool {
		if functions[i].Namespace == functions[j].Namespace {
			return functions[i].Name < functions[j].Name
		}
		return functions[i].Namespace < functions[j].Namespace
	})
}

func sortEventTriggerList(triggers []*eventtrigger.EventTrigger) {
	sort.Slice(triggers, func(i, j int) bool {
		if triggers[i].Namespace == triggers[j].Namespace {
			return triggers[i].Name < triggers[j].Name
		}
		return triggers[i].Namespace < triggers[j].Namespace
	})
}

func sortWorkflowList(workflows []*workflow.Workflow) {
	sort.Slice(workflows, func(i, j int) bool {
		if workflows[i].Namespace == workflows[j].Namespace {
			return workflows[i].Name < workflows[j].Name
		}
		return workflows[i].Namespace < workflows[j].Namespace
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

func writeDNSTable(out io.Writer, items []*dns.DNS) error {
	if err := writef(out, "%s %s %s %s %s\n",
		cliui.PadRight("DNS", 31),
		cliui.PadRight("HOST", 26),
		cliui.PadRight("PATHS", 38),
		cliui.PadRight("NAMESPACE", 14),
		"LABELS",
	); err != nil {
		return err
	}
	for _, d := range items {
		name := fmt.Sprintf("%s  %s", cliui.Icon(cliui.IconInfo, "[dns]"), d.Name)
		if err := writef(out, "%s %s %s %s %s\n",
			cliui.PadRight(name, 31),
			cliui.PadRight(d.Spec.Host, 26),
			cliui.PadRight(formatDNSPaths(d.Spec.Paths), 38),
			cliui.PadRight(d.Namespace, 14),
			formatLabels(d.Labels),
		); err != nil {
			return err
		}
	}
	return nil
}

func writeReplicaSetTable(out io.Writer, replicaSets []*replicaset.ReplicaSet) error {
	if err := writef(out, "%s %s %s %s %s\n",
		cliui.PadRight("REPLICASET", 31),
		cliui.PadRight("DESIRED", 10),
		cliui.PadRight("CURRENT", 10),
		cliui.PadRight("NAMESPACE", 14),
		"LABELS",
	); err != nil {
		return err
	}
	for _, rs := range replicaSets {
		name := fmt.Sprintf("%s  %s", cliui.Icon(cliui.IconInfo, "[rs]"), rs.Name)
		if err := writef(out, "%s %s %s %s %s\n",
			cliui.PadRight(name, 31),
			cliui.PadRight(fmt.Sprintf("%d", rs.Spec.Replicas), 10),
			cliui.PadRight(fmt.Sprintf("%d", rs.Status.Replicas), 10),
			cliui.PadRight(rs.Namespace, 14),
			formatLabels(rs.Labels),
		); err != nil {
			return err
		}
	}
	return nil
}

func writeHPATable(out io.Writer, hpas []*hpa.HorizontalPodAutoscaler) error {
	if err := writef(out, "%s %s %s %s %s %s %s\n",
		cliui.PadRight("HPA", 31),
		cliui.PadRight("TARGET", 18),
		cliui.PadRight("MIN", 6),
		cliui.PadRight("MAX", 6),
		cliui.PadRight("REPLICAS", 12),
		cliui.PadRight("METRICS", 22),
		"NAMESPACE",
	); err != nil {
		return err
	}
	for _, autoscaler := range hpas {
		name := fmt.Sprintf("%s  %s", cliui.Icon(cliui.IconInfo, "[hpa]"), autoscaler.Name)
		replicas := fmt.Sprintf("%d/%d", autoscaler.Status.CurrentReplicas, autoscaler.Status.DesiredReplicas)
		if err := writef(out, "%s %s %s %s %s %s %s\n",
			cliui.PadRight(name, 31),
			cliui.PadRight(formatHPATarget(autoscaler), 18),
			cliui.PadRight(fmt.Sprintf("%d", autoscaler.Spec.MinReplicas), 6),
			cliui.PadRight(fmt.Sprintf("%d", autoscaler.Spec.MaxReplicas), 6),
			cliui.PadRight(replicas, 12),
			cliui.PadRight(formatHPAMetrics(autoscaler.Status.CurrentMetrics), 22),
			autoscaler.Namespace,
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

func writeFunctionTable(out io.Writer, functions []*function.Function) error {
	if err := writef(out, "%s %s %s %s %s\n",
		cliui.PadRight("FUNCTION", 31),
		cliui.PadRight("RUNTIME", 12),
		cliui.PadRight("STATUS", 12),
		cliui.PadRight("NAMESPACE", 14),
		"LABELS",
	); err != nil {
		return err
	}
	for _, fn := range functions {
		name := fmt.Sprintf("%s  %s", cliui.Icon(cliui.IconInfo, "[fn]"), fn.Name)
		if err := writef(out, "%s %s %s %s %s\n",
			cliui.PadRight(name, 31),
			cliui.PadRight(fn.Spec.Runtime, 12),
			cliui.PadRight(emptyDash(fn.Status.Phase), 12),
			cliui.PadRight(fn.Namespace, 14),
			formatLabels(fn.Labels),
		); err != nil {
			return err
		}
	}
	return nil
}

func writeEventTriggerTable(out io.Writer, triggers []*eventtrigger.EventTrigger) error {
	if err := writef(out, "%s %s %s %s %s\n",
		cliui.PadRight("EVENTTRIGGER", 31),
		cliui.PadRight("SUBJECT", 24),
		cliui.PadRight("FUNCTION", 18),
		cliui.PadRight("NAMESPACE", 14),
		"LABELS",
	); err != nil {
		return err
	}
	for _, trigger := range triggers {
		name := fmt.Sprintf("%s  %s", cliui.Icon(cliui.IconInfo, "[trigger]"), trigger.Name)
		if err := writef(out, "%s %s %s %s %s\n",
			cliui.PadRight(name, 31),
			cliui.PadRight(trigger.Spec.Subject, 24),
			cliui.PadRight(trigger.Spec.FunctionRef.Name, 18),
			cliui.PadRight(trigger.Namespace, 14),
			formatLabels(trigger.Labels),
		); err != nil {
			return err
		}
	}
	return nil
}

func writeWorkflowTable(out io.Writer, workflows []*workflow.Workflow) error {
	if err := writef(out, "%s %s %s %s\n",
		cliui.PadRight("WORKFLOW", 31),
		cliui.PadRight("STEPS", 8),
		cliui.PadRight("NAMESPACE", 14),
		"LABELS",
	); err != nil {
		return err
	}
	for _, wf := range workflows {
		name := fmt.Sprintf("%s  %s", cliui.Icon(cliui.IconInfo, "[wf]"), wf.Name)
		if err := writef(out, "%s %s %s %s\n",
			cliui.PadRight(name, 31),
			cliui.PadRight(fmt.Sprintf("%d", len(wf.Spec.Steps)), 8),
			cliui.PadRight(wf.Namespace, 14),
			formatLabels(wf.Labels),
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

func describeDNS(out io.Writer, d *dns.DNS) error {
	lines := []string{
		fmt.Sprintf("Name: %s", d.Name),
		fmt.Sprintf("Namespace: %s", d.Namespace),
		fmt.Sprintf("Host: %s", d.Spec.Host),
		fmt.Sprintf("Paths: %s", formatDNSPaths(d.Spec.Paths)),
		fmt.Sprintf("Labels: %s", formatLabels(d.Labels)),
	}
	for _, line := range lines {
		if err := writef(out, "%s\n", line); err != nil {
			return err
		}
	}
	return nil
}

func formatDNSPaths(paths []dns.DNSPath) string {
	if len(paths) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(paths))
	for _, p := range paths {
		parts = append(parts, fmt.Sprintf("%s(%s)->%s:%d", p.Path, p.PathType, p.ServiceName, p.ServicePort))
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

func describeReplicaSet(out io.Writer, rs *replicaset.ReplicaSet) error {
	lines := []string{
		fmt.Sprintf("Name: %s", rs.Name),
		fmt.Sprintf("Namespace: %s", rs.Namespace),
		fmt.Sprintf("Desired: %d", rs.Spec.Replicas),
		fmt.Sprintf("Current: %d", rs.Status.Replicas),
		fmt.Sprintf("Selector: %s", formatServiceSelector(rs.Spec.Selector)),
		fmt.Sprintf("Labels: %s", formatLabels(rs.Labels)),
	}
	for _, line := range lines {
		if err := writef(out, "%s\n", line); err != nil {
			return err
		}
	}
	return nil
}

func describeHPA(out io.Writer, autoscaler *hpa.HorizontalPodAutoscaler) error {
	lines := []string{
		fmt.Sprintf("Name: %s", autoscaler.Name),
		fmt.Sprintf("Namespace: %s", autoscaler.Namespace),
		fmt.Sprintf("Target: %s", formatHPATarget(autoscaler)),
		fmt.Sprintf("MinReplicas: %d", autoscaler.Spec.MinReplicas),
		fmt.Sprintf("MaxReplicas: %d", autoscaler.Spec.MaxReplicas),
		fmt.Sprintf("CurrentReplicas: %d", autoscaler.Status.CurrentReplicas),
		fmt.Sprintf("DesiredReplicas: %d", autoscaler.Status.DesiredReplicas),
		fmt.Sprintf("Metrics: %s", formatHPAMetrics(autoscaler.Status.CurrentMetrics)),
		fmt.Sprintf("Conditions: %s", formatHPAConditions(autoscaler.Status.Conditions)),
		fmt.Sprintf("Labels: %s", formatLabels(autoscaler.Labels)),
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

func describeFunction(out io.Writer, fn *function.Function) error {
	lines := []string{
		fmt.Sprintf("Name: %s", fn.Name),
		fmt.Sprintf("Namespace: %s", fn.Namespace),
		fmt.Sprintf("Runtime: %s", fn.Spec.Runtime),
		fmt.Sprintf("Handler: %s", fn.Spec.Handler),
		fmt.Sprintf("Status: %s", emptyDash(fn.Status.Phase)),
		fmt.Sprintf("Labels: %s", formatLabels(fn.Labels)),
	}
	for _, line := range lines {
		if err := writef(out, "%s\n", line); err != nil {
			return err
		}
	}
	return nil
}

func describeEventTrigger(out io.Writer, trigger *eventtrigger.EventTrigger) error {
	lines := []string{
		fmt.Sprintf("Name: %s", trigger.Name),
		fmt.Sprintf("Namespace: %s", trigger.Namespace),
		fmt.Sprintf("Subject: %s", trigger.Spec.Subject),
		fmt.Sprintf("Function: %s", trigger.Spec.FunctionRef.Name),
		fmt.Sprintf("Active: %t", trigger.Status.Active),
		fmt.Sprintf("Labels: %s", formatLabels(trigger.Labels)),
	}
	for _, line := range lines {
		if err := writef(out, "%s\n", line); err != nil {
			return err
		}
	}
	return nil
}

func describeWorkflow(out io.Writer, wf *workflow.Workflow) error {
	steps := make([]string, 0, len(wf.Spec.Steps))
	for _, step := range wf.Spec.Steps {
		steps = append(steps, fmt.Sprintf("%s=%s", step.Name, step.FunctionRef.Name))
	}
	lines := []string{
		fmt.Sprintf("Name: %s", wf.Name),
		fmt.Sprintf("Namespace: %s", wf.Namespace),
		fmt.Sprintf("Steps: %s", strings.Join(steps, ",")),
		fmt.Sprintf("Status: %s", emptyDash(wf.Status.Phase)),
		fmt.Sprintf("Labels: %s", formatLabels(wf.Labels)),
	}
	for _, line := range lines {
		if err := writef(out, "%s\n", line); err != nil {
			return err
		}
	}
	return nil
}

func formatHPATarget(autoscaler *hpa.HorizontalPodAutoscaler) string {
	return fmt.Sprintf("%s/%s", autoscaler.Spec.ScaleTargetRef.Kind, autoscaler.Spec.ScaleTargetRef.Name)
}

func formatHPAMetrics(metrics []hpa.MetricStatus) string {
	if len(metrics) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(metrics))
	for _, metric := range metrics {
		parts = append(parts, fmt.Sprintf("%s=%d%%", metric.Name, metric.CurrentAverageUtilization))
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

func formatHPAConditions(conditions []hpa.HorizontalPodCondition) string {
	if len(conditions) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(conditions))
	for _, condition := range conditions {
		if condition.Reason != "" {
			parts = append(parts, fmt.Sprintf("%s=%s(%s)", condition.Type, condition.Status, condition.Reason))
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%s", condition.Type, condition.Status))
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
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
