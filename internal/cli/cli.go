package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
	bridge "minik8s/internal/bridge"
	"minik8s/internal/bridge/bootstrap"
	store "minik8s/internal/bridge/logbook"
	bridgeServerless "minik8s/internal/bridge/serverless"
	"minik8s/internal/bridge/tokens"
	"minik8s/internal/cliui"
	"minik8s/internal/cni"
	"minik8s/internal/dns"
	"minik8s/internal/dnssync"
	"minik8s/internal/hpa"
	"minik8s/internal/k8scompat"
	"minik8s/internal/kubeproxy"
	"minik8s/internal/minilog"
	"minik8s/internal/natslite"
	"minik8s/internal/netagent"
	"minik8s/internal/netregistry"
	"minik8s/internal/node"
	"minik8s/internal/pod"
	"minik8s/internal/routeproxy"
	dockerruntime "minik8s/internal/runtime/docker"
	nodeSailer "minik8s/internal/sailer"
	"minik8s/internal/service"
	"minik8s/pkg/runtime"
	podyaml "minik8s/pkg/yaml"
)

// Config contains CLI dependencies.
type Config struct {
	Runtime           runtime.ContainerRuntime
	Store             store.PodStore
	ServiceStore      store.ServiceStore
	DNSStore          store.DNSStore
	ReplicaSetStore   store.ReplicaSetStore
	HPAStore          store.HPAStore
	MetricsStore      store.MetricsStore
	NodeStore         store.NodeStore
	K8sCompatStore    store.K8sCompatStore
	FunctionStore     store.FunctionStore
	EventTriggerStore store.EventTriggerStore
	WorkflowStore     store.WorkflowStore
	Bridge            *bridge.Bridge
	Network           nodeSailer.PodNetworkManager
	ServiceProxy      kubeproxy.Proxy
	HTTPClient        *http.Client
	NetRunner         netagent.Runner
	FlannelRunner     FlannelRunner
	MooringCNIRunner  MooringCNIRunner
}

// App is the Minik8s command-line application.
type App struct {
	runtime           runtime.ContainerRuntime
	store             store.PodStore
	serviceStore      store.ServiceStore
	dnsStore          store.DNSStore
	replicaSetStore   store.ReplicaSetStore
	hpaStore          store.HPAStore
	metricsStore      store.MetricsStore
	nodeStore         store.NodeStore
	k8sCompatStore    store.K8sCompatStore
	functionStore     store.FunctionStore
	eventTriggerStore store.EventTriggerStore
	workflowStore     store.WorkflowStore
	controlBridge     *bridge.Bridge
	network           nodeSailer.PodNetworkManager
	serviceProxy      kubeproxy.Proxy
	httpClient        *http.Client
	netRunner         netagent.Runner
	flannelRunner     FlannelRunner
	mooringCNIRunner  MooringCNIRunner
	namespace         string
}

// New creates an App.
func New(config Config) *App {
	network := config.Network
	if network == nil {
		network = defaultNetworkManager()
	}
	serviceStore := config.ServiceStore
	if serviceStore == nil {
		serviceStore = store.NewInMemoryServiceStore()
	}
	dnsStore := config.DNSStore
	if dnsStore == nil {
		dnsStore = store.NewInMemoryDNSStore()
	}
	replicaSetStore := config.ReplicaSetStore
	if replicaSetStore == nil {
		replicaSetStore = store.NewInMemoryReplicaSetStore()
	}
	hpaStore := config.HPAStore
	if hpaStore == nil {
		hpaStore = store.NewInMemoryHPAStore()
	}
	metricsStore := config.MetricsStore
	if metricsStore == nil {
		metricsStore = store.NewInMemoryMetricsStore()
	}
	nodeStore := config.NodeStore
	if nodeStore == nil {
		nodeStore = store.NewInMemoryNodeStore()
	}
	k8sCompatStore := config.K8sCompatStore
	if k8sCompatStore == nil {
		k8sCompatStore = store.NewInMemoryK8sCompatStore()
	}
	functionStore := config.FunctionStore
	if functionStore == nil {
		functionStore = store.NewInMemoryFunctionStore()
	}
	eventTriggerStore := config.EventTriggerStore
	if eventTriggerStore == nil {
		eventTriggerStore = store.NewInMemoryEventTriggerStore()
	}
	workflowStore := config.WorkflowStore
	if workflowStore == nil {
		workflowStore = store.NewInMemoryWorkflowStore()
	}
	serviceProxy := config.ServiceProxy
	controlBridge := config.Bridge
	if controlBridge == nil {
		controlBridge = bridge.New(bridge.Config{
			PodStore:           config.Store,
			ServiceStore:       serviceStore,
			DNSStore:           dnsStore,
			ReplicaSetStore:    replicaSetStore,
			HPAStore:           hpaStore,
			MetricsStore:       metricsStore,
			NodeStore:          nodeStore,
			K8sCompatStore:     k8sCompatStore,
			FunctionStore:      functionStore,
			EventTriggerStore:  eventTriggerStore,
			WorkflowStore:      workflowStore,
			BootstrapTokenPath: DefaultBootstrapTokenPath(),
		})
	}
	return &App{
		runtime:           config.Runtime,
		store:             config.Store,
		serviceStore:      serviceStore,
		dnsStore:          dnsStore,
		replicaSetStore:   replicaSetStore,
		hpaStore:          hpaStore,
		metricsStore:      metricsStore,
		nodeStore:         nodeStore,
		k8sCompatStore:    k8sCompatStore,
		functionStore:     functionStore,
		eventTriggerStore: eventTriggerStore,
		workflowStore:     workflowStore,
		controlBridge:     controlBridge,
		network:           network,
		serviceProxy:      serviceProxy,
		httpClient:        config.HTTPClient,
		netRunner:         config.NetRunner,
		flannelRunner:     config.FlannelRunner,
		mooringCNIRunner:  config.MooringCNIRunner,
		namespace:         "default",
	}
}

// Run executes the compatibility command tree used by tests and in-process callers.
func (a *App) Run(ctx context.Context, args []string, out io.Writer) error {
	cmd := newCompatCommand(a, out)
	cmd.SetArgs(args)
	return cmd.ExecuteContext(ctx)
}

func (a *App) apply(ctx context.Context, args []string, out io.Writer) error {
	minilog.Info("cli-apply", "start args=%v", args)
	client, err := a.controlPlaneClient()
	if err != nil {
		return err
	}
	minilog.Info("cli-apply", "harbor=%s", client.baseURL)
	path, err := valueFlag(args, "-f")
	if err != nil {
		return err
	}
	objects, err := loadApplyObjects(ctx, path, a.httpClient)
	if err != nil {
		return err
	}
	for _, object := range objects {
		if err := a.applyObject(ctx, client, object, out); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) applyObject(ctx context.Context, client *controlPlaneClient, object applyObject, out io.Writer) error {
	kind := object.Kind
	if kind == "Service" {
		svc, err := podyaml.LoadServiceFromYAML(object.Data)
		if err != nil {
			return err
		}
		updated, err := client.ApplyService(ctx, svc)
		if err != nil {
			return err
		}
		return writes(out, cliui.SuccessLine("service/%s created (%s)", updated.Name, updated.Spec.Type))
	}
	if kind == dns.Kind {
		d, err := podyaml.LoadDNSFromYAML(object.Data)
		if err != nil {
			return err
		}
		updated, err := client.ApplyDNS(ctx, d)
		if err != nil {
			return err
		}
		return writes(out, cliui.SuccessLine("dns/%s created (%s)", updated.Name, updated.Spec.Host))
	}
	if kind == "Node" {
		n, err := podyaml.LoadNodeFromYAML(object.Data)
		if err != nil {
			return err
		}
		updated, err := client.ApplyNode(ctx, n)
		if err != nil {
			return err
		}
		return writes(out, cliui.SuccessLine("node/%s created (%s)", updated.Name(), updated.Status.Phase))
	}
	if kind == "ReplicaSet" {
		rs, err := podyaml.LoadReplicaSetFromYAML(object.Data)
		if err != nil {
			return err
		}
		updated, err := client.ApplyReplicaSet(ctx, rs)
		if err != nil {
			return err
		}
		return writes(out, cliui.SuccessLine("replicaset/%s created (%d/%d)", updated.Name, updated.Status.Replicas, updated.Spec.Replicas))
	}
	if kind == "Function" {
		fn, err := podyaml.LoadFunctionFromYAML(object.Data)
		if err != nil {
			return err
		}
		updated, err := client.ApplyFunction(ctx, fn)
		if err != nil {
			return err
		}
		return writes(out, cliui.SuccessLine("function/%s created (%s)", updated.Name, updated.Spec.Runtime))
	}
	if kind == "EventTrigger" {
		trigger, err := podyaml.LoadEventTriggerFromYAML(object.Data)
		if err != nil {
			return err
		}
		updated, err := client.ApplyEventTrigger(ctx, trigger)
		if err != nil {
			return err
		}
		return writes(out, cliui.SuccessLine("eventtrigger/%s created (%s)", updated.Name, updated.Spec.Subject))
	}
	if kind == "Workflow" {
		wf, err := podyaml.LoadWorkflowFromYAML(object.Data)
		if err != nil {
			return err
		}
		updated, err := client.ApplyWorkflow(ctx, wf)
		if err != nil {
			return err
		}
		return writes(out, cliui.SuccessLine("workflow/%s created (%d steps)", updated.Name, len(updated.Spec.Steps)))
	}
	if kind == hpa.Kind {
		autoscaler, err := podyaml.LoadHPAFromYAML(object.Data)
		if err != nil {
			return err
		}
		updated, err := client.ApplyHPA(ctx, autoscaler)
		if err != nil {
			return err
		}
		return writes(out, cliui.SuccessLine("hpa/%s created (%d/%d)", updated.Name, updated.Status.DesiredReplicas, updated.Spec.MaxReplicas))
	}
	if kind == k8scompat.KindConfigMap {
		var cm k8scompat.ConfigMap
		if err := yaml.Unmarshal(object.Data, &cm); err != nil {
			return err
		}
		updated, err := client.ApplyConfigMap(ctx, &cm)
		if err != nil {
			return err
		}
		return writes(out, cliui.SuccessLine("configmap/%s created", updated.Name))
	}
	if kind == k8scompat.KindDaemonSet {
		var ds k8scompat.DaemonSet
		if err := yaml.Unmarshal(object.Data, &ds); err != nil {
			return err
		}
		updated, err := client.ApplyDaemonSet(ctx, &ds)
		if err != nil {
			return err
		}
		return writes(out, cliui.SuccessLine("daemonset/%s created", updated.Name))
	}
	if isGenericK8sCompatKind(kind) {
		var obj k8scompat.GenericObject
		if err := yaml.Unmarshal(object.Data, &obj); err != nil {
			return err
		}
		updated, err := client.ApplyGenericCompat(ctx, &obj)
		if err != nil {
			return err
		}
		return writes(out, cliui.SuccessLine("%s/%s accepted", strings.ToLower(updated.Kind), updated.Name))
	}
	if kind != "" && kind != "Pod" {
		return fmt.Errorf("unsupported kind %q", kind)
	}
	p, err := podyaml.LoadPodFromYAML(object.Data)
	if err != nil {
		return err
	}
	p.Status.Phase = pod.PodPending
	updated, err := client.ApplyPod(ctx, p)
	if err != nil {
		return err
	}
	if updated.Status.Phase == pod.PodFailed {
		if err := writes(out, cliui.WarnLine("pod/%s created (%s)", updated.Name, updated.Status.Phase)); err != nil {
			return err
		}
	} else if err := writes(out, cliui.SuccessLine("pod/%s created (%s)", updated.Name, updated.Status.Phase)); err != nil {
		return err
	}
	if updated.Status.Phase == pod.PodFailed && updated.Status.Reason != "" {
		return writes(out, cliui.WarnLine("reason: %s", updated.Status.Reason))
	}
	return nil
}

type applyObject struct {
	Source string
	Kind   string
	Data   []byte
}

func loadApplyObjects(ctx context.Context, source string, client *http.Client) ([]applyObject, error) {
	data, err := readApplySource(ctx, source, client)
	if err != nil {
		return nil, err
	}
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	var objects []applyObject
	for {
		var raw map[string]any
		err := dec.Decode(&raw)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", source, err)
		}
		if len(raw) == 0 {
			continue
		}
		doc, err := yaml.Marshal(raw)
		if err != nil {
			return nil, err
		}
		kind, _ := raw["kind"].(string)
		objects = append(objects, applyObject{Source: source, Kind: kind, Data: doc})
	}
	if len(objects) == 0 {
		return nil, fmt.Errorf("%s contains no Kubernetes objects", source)
	}
	return objects, nil
}

func readApplySource(ctx context.Context, source string, client *http.Client) ([]byte, error) {
	parsed, err := url.Parse(source)
	if err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") {
		if client == nil {
			client = http.DefaultClient
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
		if err != nil {
			return nil, err
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("fetch %s: %s", source, resp.Status)
		}
		return io.ReadAll(resp.Body)
	}
	return os.ReadFile(source)
}

func isGenericK8sCompatKind(kind string) bool {
	switch kind {
	case k8scompat.KindNamespace, k8scompat.KindClusterRole, k8scompat.KindClusterRoleBinding, k8scompat.KindServiceAccount:
		return true
	default:
		return false
	}
}

func (a *App) delete(ctx context.Context, args []string, out io.Writer) error {
	minilog.Info("cli-delete", "start args=%v", args)
	client, err := a.controlPlaneClient()
	if err != nil {
		return err
	}
	if len(args) < 2 {
		return fmt.Errorf("usage: minik8s delete pod|service|dns|replicaset|hpa|node|function|eventtrigger|workflow <name> [-n namespace]")
	}
	if args[0] == "service" || args[0] == "svc" {
		name := args[1]
		namespace := namespaceFlag(args[2:])
		if err := client.DeleteService(ctx, name, namespace); err != nil {
			return err
		}
		return writes(out, cliui.SuccessLine("service/%s deleted", name))
	}
	if args[0] == "dns" {
		name := args[1]
		namespace := namespaceFlag(args[2:])
		if err := client.DeleteDNS(ctx, name, namespace); err != nil {
			return err
		}
		return writes(out, cliui.SuccessLine("dns/%s deleted", name))
	}
	if args[0] == "replicaset" || args[0] == "replicasets" || args[0] == "rs" {
		name := args[1]
		namespace := namespaceFlag(args[2:])
		if err := client.DeleteReplicaSet(ctx, name, namespace); err != nil {
			return err
		}
		return writes(out, cliui.SuccessLine("replicaset/%s deleted", name))
	}
	if args[0] == "hpa" || args[0] == "hpas" || args[0] == "horizontalpodautoscaler" || args[0] == "horizontalpodautoscalers" {
		name := args[1]
		namespace := namespaceFlag(args[2:])
		if err := client.DeleteHPA(ctx, name, namespace); err != nil {
			return err
		}
		return writes(out, cliui.SuccessLine("hpa/%s deleted", name))
	}
	if args[0] == "node" || args[0] == "nodes" || args[0] == "no" {
		name := args[1]
		if err := client.DeleteNode(ctx, name); err != nil {
			return err
		}
		return writes(out, cliui.SuccessLine("node/%s deleted", name))
	}
	if args[0] == "function" || args[0] == "functions" || args[0] == "fn" {
		name := args[1]
		namespace := namespaceFlag(args[2:])
		if err := client.DeleteFunction(ctx, name, namespace); err != nil {
			return err
		}
		return writes(out, cliui.SuccessLine("function/%s deleted", name))
	}
	if args[0] == "eventtrigger" || args[0] == "eventtriggers" || args[0] == "trigger" || args[0] == "triggers" {
		name := args[1]
		namespace := namespaceFlag(args[2:])
		if err := client.DeleteEventTrigger(ctx, name, namespace); err != nil {
			return err
		}
		return writes(out, cliui.SuccessLine("eventtrigger/%s deleted", name))
	}
	if args[0] == "workflow" || args[0] == "workflows" || args[0] == "wf" {
		name := args[1]
		namespace := namespaceFlag(args[2:])
		if err := client.DeleteWorkflow(ctx, name, namespace); err != nil {
			return err
		}
		return writes(out, cliui.SuccessLine("workflow/%s deleted", name))
	}
	if args[0] != "pod" {
		return fmt.Errorf("usage: minik8s delete pod|service|dns|replicaset|hpa|node|function|eventtrigger|workflow <name> [-n namespace]")
	}
	name := args[1]
	namespace := namespaceFlag(args[2:])
	if err := client.DeletePod(ctx, name, namespace); err != nil {
		return err
	}
	return writes(out, cliui.SuccessLine("pod/%s deleted", name))
}

func (a *App) controlPlaneClient() (*controlPlaneClient, error) {
	if server := strings.TrimSpace(os.Getenv("MINIK8S_HARBOR")); server != "" {
		return newControlPlaneClient(server, a.httpClient)
	}
	conf, err := readLocalConfig(DefaultLocalConfigPath())
	if err != nil {
		return nil, err
	}
	return newControlPlaneClient(conf.Harbor, a.httpClient)
}

func (a *App) invokeFunction(ctx context.Context, name, data string, out io.Writer) error {
	client, err := a.controlPlaneClient()
	if err != nil {
		return err
	}
	resp, err := client.InvokeFunction(ctx, name, a.namespace, data)
	if err != nil {
		return err
	}
	if resp.Phase == "Failed" {
		return writes(out, cliui.WarnLine("function/%s failed: %s", name, resp.Error))
	}
	return writes(out, cliui.SuccessLine("function/%s invoked output=%s", name, resp.Output))
}

func (a *App) publishNATS(ctx context.Context, subject, data string, out io.Writer) error {
	natsURL := os.Getenv("MINIK8S_NATS_URL")
	if err := natslite.Publish(ctx, natsURL, subject, []byte(data)); err != nil {
		return err
	}
	return writes(out, cliui.SuccessLine("published subject=%s bytes=%d", subject, len(data)))
}

func (a *App) doctor(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: minik8s doctor docker|network|clean|logbook|serverless|addons|addon <name>")
	}
	if args[0] == "addons" {
		return a.doctorAddons(out, knownAddons...)
	}
	if args[0] == "addon" {
		if len(args) < 2 {
			return fmt.Errorf("usage: minik8s doctor addon <name>")
		}
		name := AddonName(strings.ToLower(strings.TrimSpace(args[1])))
		if !isKnownAddon(name) {
			return fmt.Errorf("unknown addon %q", name)
		}
		return a.doctorAddons(out, name)
	}
	if args[0] == "network" {
		return a.doctorNetwork(out)
	}
	if args[0] == "clean" {
		return a.doctorClean(ctx, out)
	}
	if args[0] == "logbook" {
		return a.doctorLogbook(ctx, out)
	}
	if args[0] == "serverless" {
		return a.doctorServerless(ctx, out)
	}
	if args[0] != "docker" {
		return fmt.Errorf("usage: minik8s doctor docker|network|clean|logbook|serverless|addons|addon <name>")
	}
	minilog.Info("doctor-docker", "start args=%v", args)
	endpoint := dockerruntime.ResolveDockerEndpoint()
	host := endpoint.Host
	if host == "" {
		host = "docker default"
	}
	if err := writes(out, cliui.InfoLine("host: %s", host)); err != nil {
		return err
	}
	if err := writes(out, cliui.InfoLine("source: %s", endpoint.Source)); err != nil {
		return err
	}
	if endpoint.Context != "" {
		if err := writes(out, cliui.InfoLine("context: %s", endpoint.Context)); err != nil {
			return err
		}
	}
	if a.runtime.IsHealthy(ctx) {
		if err := writef(out, "%s  ping: ok\n", cliui.Icon(cliui.IconSuccess, "[ok]")); err != nil {
			return err
		}
	} else {
		if err := writes(out, cliui.WarnLine("ping: failed")); err != nil {
			return err
		}
	}
	if len(args) >= 3 && args[1] == "pull" {
		imageName := args[2]
		minilog.Info("doctor-docker-pull", "image=%s", imageName)
		if err := a.runtime.PullImage(ctx, imageName); err != nil {
			return writes(out, cliui.WarnLine("pull: failed %v", err))
		}
		return writes(out, cliui.SuccessLine("pull: ok image=%s", imageName))
	}
	return nil
}

func (a *App) doctorAddons(out io.Writer, names ...AddonName) error {
	for _, name := range names {
		state, detail := addonReadiness(name)
		line := fmt.Sprintf("addon/%s: %s", name, state)
		if detail != "" {
			line += " " + detail
		}
		switch state {
		case "ready":
			if err := writes(out, cliui.SuccessLine("%s", line)); err != nil {
				return err
			}
		case "disabled", "starting":
			if err := writes(out, cliui.InfoLine("%s", line)); err != nil {
				return err
			}
		default:
			if err := writes(out, cliui.WarnLine("%s", line)); err != nil {
				return err
			}
		}
	}
	return nil
}

func addonReadiness(name AddonName) (string, string) {
	path := addonManifestPath(name)
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return "disabled", "manifest missing; run minik8s init --force"
		}
		return "degraded", err.Error()
	}
	ports := addonProbePorts(name)
	if len(ports) == 0 {
		return "ready", fmt.Sprintf("manifest=%s", path)
	}
	missing := make([]string, 0, len(ports))
	for _, port := range ports {
		if !tcpPortReady("127.0.0.1:" + port) {
			missing = append(missing, port)
		}
	}
	if len(missing) == 0 {
		return "ready", fmt.Sprintf("ports=%s", strings.Join(ports, ","))
	}
	if len(missing) == len(ports) {
		return "starting", fmt.Sprintf("manifest=%s waiting ports=%s", path, strings.Join(missing, ","))
	}
	return "degraded", fmt.Sprintf("missing ports=%s", strings.Join(missing, ","))
}

func addonProbePorts(name AddonName) []string {
	switch name {
	case AddonDNS:
		return []string{"80"}
	case AddonServerless:
		return []string{"4222"}
	default:
		return nil
	}
}

func (a *App) doctorServerless(ctx context.Context, out io.Writer) error {
	natsURL := os.Getenv("MINIK8S_NATS_URL")
	if strings.TrimSpace(natsURL) == "" {
		return writes(out, cliui.WarnLine("serverless: MINIK8S_NATS_URL is not set; NATS event triggers are disabled"))
	}
	if err := writes(out, cliui.InfoLine("nats: %s", natsURL)); err != nil {
		return err
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := natslite.Probe(probeCtx, natsURL); err != nil {
		return writes(out, cliui.WarnLine("serverless: NATS failed %v", err))
	}
	return writes(out, cliui.SuccessLine("serverless: nats ok"))
}

func (a *App) doctorLogbook(ctx context.Context, out io.Writer) error {
	endpoints := store.ParseEndpoints(os.Getenv("MINIK8S_LOGBOOK_ENDPOINTS"))
	if len(endpoints) == 0 {
		return writes(out, cliui.WarnLine("logbook: MINIK8S_LOGBOOK_ENDPOINTS is not set; using local JSON file store"))
	}
	if err := writes(out, cliui.InfoLine("endpoints: %s", strings.Join(endpoints, ","))); err != nil {
		return err
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := store.Probe(probeCtx, endpoints); err != nil {
		return writes(out, cliui.WarnLine("logbook: failed %v", err))
	}
	return writes(out, cliui.SuccessLine("logbook: ok"))
}

func (a *App) doctorNetwork(out io.Writer) error {
	if err := writes(out, cliui.InfoLine("confDir: %s", DefaultCNIConfDir())); err != nil {
		return err
	}
	if err := writes(out, cliui.InfoLine("binDir: %s", DefaultCNIBinDir())); err != nil {
		return err
	}
	if err := writes(out, cliui.InfoLine("plugin: mooring")); err != nil {
		return err
	}
	configPresent := false
	if _, err := os.Stat(DefaultCNIConfDir()); err == nil {
		configPresent = true
		if err := writes(out, cliui.InfoLine("config: present")); err != nil {
			return err
		}
	} else if os.IsNotExist(err) {
		if err := writes(out, cliui.WarnLine("config: missing")); err != nil {
			return err
		}
	} else {
		return err
	}
	if configPresent {
		if err := a.writeCNIDiagnostics(out); err != nil {
			return err
		}
	}
	if _, err := os.Stat(filepath.Join(DefaultCNIBinDir(), "mooring")); err == nil {
		return writes(out, cliui.InfoLine("mooring: present"))
	} else if os.IsNotExist(err) {
		return writes(out, cliui.WarnLine("mooring: missing"))
	} else {
		return err
	}
}

func (a *App) doctorClean(ctx context.Context, out io.Writer) error {
	conf, _ := readCNIDoctorConfig()
	bridgeName := conf.Bridge
	if bridgeName == "" {
		bridgeName = "mk8s0"
	}
	runner := a.netRunner
	if runner == nil {
		runner = func(name string, args ...string) error {
			cmd := exec.CommandContext(ctx, name, args...)
			out, err := cmd.CombinedOutput()
			if err != nil {
				return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
			}
			return nil
		}
	}
	clean := networkCleaner{runner: runner}
	for _, route := range conf.Routes {
		if route.Dst == "" {
			continue
		}
		if conf.PodCIDR != "" {
			if err := clean.iptables("nat", "POSTROUTING", "-s", conf.PodCIDR, "-d", route.Dst, "-j", "ACCEPT"); err != nil {
				return err
			}
			if err := clean.iptables("filter", "FORWARD", "-s", conf.PodCIDR, "-d", route.Dst, "-j", "ACCEPT"); err != nil {
				return err
			}
			if err := clean.iptables("filter", "FORWARD", "-s", route.Dst, "-d", conf.PodCIDR, "-j", "ACCEPT"); err != nil {
				return err
			}
		}
		if err := clean.run("ip", "route", "delete", route.Dst, "dev", bridgeName); err != nil {
			return err
		}
	}
	if conf.PodCIDR != "" {
		if err := clean.iptables("nat", "POSTROUTING", "-s", conf.PodCIDR, "!", "-o", bridgeName, "-j", "MASQUERADE"); err != nil {
			return err
		}
	}
	if err := clean.iptables("filter", "FORWARD", "-i", bridgeName, "-j", "ACCEPT"); err != nil {
		return err
	}
	if err := clean.iptables("filter", "FORWARD", "-o", bridgeName, "-j", "ACCEPT"); err != nil {
		return err
	}
	if err := clean.run("ip", "link", "delete", "mk8s-vxlan"); err != nil {
		return err
	}
	if err := clean.run("ip", "link", "delete", bridgeName); err != nil {
		return err
	}
	if conf.IPAM.StatePath != "" {
		if err := removeIfExists(conf.IPAM.StatePath); err != nil {
			return err
		}
	}
	if err := removeIfExists(filepath.Join(DefaultCNIConfDir(), "10-mooring.conf")); err != nil {
		return err
	}
	return writes(out, cliui.SuccessLine("network cleanup complete bridge=%s", bridgeName))
}

type networkCleaner struct {
	runner netagent.Runner
}

func (c networkCleaner) iptables(table, chain string, rule ...string) error {
	args := append([]string{"-t", table, "-D", chain}, rule...)
	return c.run("iptables", args...)
}

func (c networkCleaner) run(name string, args ...string) error {
	err := c.runner(name, args...)
	if err == nil || isNetworkCleanupMissing(err) {
		return nil
	}
	return err
}

func isNetworkCleanupMissing(err error) bool {
	msg := err.Error()
	missing := []string{
		"No such file or directory",
		"Cannot find device",
		"does a matching rule exist",
		"No chain/target/match by that name",
		"RTNETLINK answers: No such process",
		"not found",
	}
	for _, marker := range missing {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

func readCNIDoctorConfig() (cniDoctorConfig, error) {
	var conf cniDoctorConfig
	data, err := os.ReadFile(filepath.Join(DefaultCNIConfDir(), "10-mooring.conf"))
	if err != nil {
		return conf, err
	}
	if err := json.Unmarshal(data, &conf); err != nil {
		return conf, err
	}
	return conf, nil
}

func removeIfExists(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

type cniDoctorRoute struct {
	Dst string `json:"dst"`
	GW  string `json:"gw"`
}

type cniDoctorConfig struct {
	Bridge  string `json:"bridge"`
	PodCIDR string `json:"podCIDR"`
	Gateway string `json:"gateway"`
	IPAM    struct {
		StatePath string `json:"statePath"`
	} `json:"ipam"`
	Routes []cniDoctorRoute `json:"routes"`
}

type cniDoctorIPAMState struct {
	Allocations map[string]string `json:"allocations"`
}

func (a *App) writeCNIDiagnostics(out io.Writer) error {
	data, err := os.ReadFile(filepath.Join(DefaultCNIConfDir(), "10-mooring.conf"))
	if err != nil {
		if os.IsNotExist(err) {
			return writes(out, cliui.WarnLine("config-file: missing"))
		}
		return err
	}
	var conf cniDoctorConfig
	if err := json.Unmarshal(data, &conf); err != nil {
		return writes(out, cliui.WarnLine("config-parse: failed %v", err))
	}
	if conf.Bridge != "" {
		if err := writes(out, cliui.InfoLine("bridge: %s", conf.Bridge)); err != nil {
			return err
		}
	}
	if conf.PodCIDR != "" {
		if err := writes(out, cliui.InfoLine("podCIDR: %s", conf.PodCIDR)); err != nil {
			return err
		}
	}
	if conf.Gateway != "" {
		if err := writes(out, cliui.InfoLine("gateway: %s", conf.Gateway)); err != nil {
			return err
		}
	}
	if conf.IPAM.StatePath != "" {
		if err := writes(out, cliui.InfoLine("ipam: %s", conf.IPAM.StatePath)); err != nil {
			return err
		}
		if err := writeIPAMDiagnostics(out, conf.IPAM.StatePath); err != nil {
			return err
		}
	}
	for _, route := range conf.Routes {
		if route.Dst == "" || route.GW == "" {
			continue
		}
		if err := writes(out, cliui.InfoLine("route: %s via %s", route.Dst, route.GW)); err != nil {
			return err
		}
		if routeInstalled(route.Dst, route.GW) {
			if err := writes(out, cliui.SuccessLine("route-installed: ok %s", route.Dst)); err != nil {
				return err
			}
		} else if err := writes(out, cliui.WarnLine("route-installed: missing %s", route.Dst)); err != nil {
			return err
		}
	}
	return nil
}

func writeIPAMDiagnostics(out io.Writer, statePath string) error {
	data, err := os.ReadFile(statePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var state cniDoctorIPAMState
	if err := json.Unmarshal(data, &state); err != nil {
		return writes(out, cliui.WarnLine("ipam-parse: failed %v", err))
	}
	keys := make([]string, 0, len(state.Allocations))
	for key := range state.Allocations {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if err := writes(out, cliui.InfoLine("ipam-allocations: %d", len(keys))); err != nil {
		return err
	}
	for _, key := range keys {
		if err := writes(out, cliui.InfoLine("allocation: %s=%s", key, state.Allocations[key])); err != nil {
			return err
		}
	}
	return nil
}

func routeInstalled(dst, gw string) bool {
	out, err := exec.Command("ip", "route", "show", dst).CombinedOutput()
	if err != nil {
		return false
	}
	text := string(out)
	return strings.Contains(text, dst) && strings.Contains(text, gw)
}

type initOptions struct {
	force             bool
	dnsListenPort     int32
	ingressListenPort int32
}

func (a *App) initialize(ctx context.Context, args []string, out io.Writer) error {
	options, err := parseInitOptions(args)
	if err != nil {
		return err
	}
	etcdDir, err := bridgeDependencyEtcdDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(etcdDir, 0o755); err != nil {
		return fmt.Errorf("creating bridge dependency state dir: %w", err)
	}
	dnsDir, err := filepath.Abs(DefaultDNSDir())
	if err != nil {
		return fmt.Errorf("resolving dns config dir: %w", err)
	}
	if err := os.MkdirAll(dnsDir, 0o755); err != nil {
		return fmt.Errorf("creating dns config dir: %w", err)
	}
	if err := writeDNSGatewayConfigs(DefaultCoreDNSCorefilePath(), DefaultNginxDNSConfigPath()); err != nil {
		return fmt.Errorf("writing dns gateway config: %w", err)
	}
	if err := writeTextFile(DefaultDNSHostsPath(), "# generated by minik8s dns-sync\n"); err != nil {
		return fmt.Errorf("initializing dns hosts: %w", err)
	}
	if err := writeTextFile(DefaultDNSRoutesPath(), "{\"hosts\":[]}\n"); err != nil {
		return fmt.Errorf("initializing dns routes: %w", err)
	}
	if err := writeBridgeStaticPodManifests(initManifestOptions{
		Force:             options.force,
		EtcdDir:           etcdDir,
		DNSDir:            dnsDir,
		DNSListenPort:     options.dnsListenPort,
		IngressListenPort: options.ingressListenPort,
	}); err != nil {
		return err
	}
	_ = ctx
	if err := writes(out, cliui.SuccessLine("static pod manifests initialized at %s", DefaultStaticPodDir())); err != nil {
		return err
	}
	return writes(out, cliui.InfoLine("next: ./minik8s bridge --listen :18080"))
}

func parseInitOptions(args []string) (initOptions, error) {
	options := initOptions{dnsListenPort: 53, ingressListenPort: 80}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--force":
			options.force = true
		case "--dns-listen":
			i++
			if i >= len(args) {
				return options, fmt.Errorf("missing value for --dns-listen")
			}
			port, err := listenPort(args[i])
			if err != nil {
				return options, fmt.Errorf("invalid --dns-listen %q: %w", args[i], err)
			}
			options.dnsListenPort = port
		case "--ingress-listen":
			i++
			if i >= len(args) {
				return options, fmt.Errorf("missing value for --ingress-listen")
			}
			port, err := listenPort(args[i])
			if err != nil {
				return options, fmt.Errorf("invalid --ingress-listen %q: %w", args[i], err)
			}
			options.ingressListenPort = port
		default:
			return options, fmt.Errorf("unknown init flag %q", args[i])
		}
	}
	return options, nil
}

type initManifestOptions struct {
	Force             bool
	EtcdDir           string
	DNSDir            string
	DNSListenPort     int32
	IngressListenPort int32
}

func writeBridgeStaticPodManifests(options initManifestOptions) error {
	if err := os.MkdirAll(DefaultStaticPodDir(), 0o755); err != nil {
		return fmt.Errorf("creating static pod manifest dir: %w", err)
	}
	paths := []string{DefaultStorageManifestPath()}
	for _, addon := range knownAddons {
		paths = append(paths, addonManifestPath(addon))
	}
	if !options.Force {
		for _, path := range paths {
			if _, err := os.Stat(path); err == nil {
				return fmt.Errorf("static pod manifest %s already exists; pass --force to overwrite", path)
			} else if !os.IsNotExist(err) {
				return fmt.Errorf("checking static pod manifest %s: %w", path, err)
			}
		}
	}
	if err := writePodManifestFile(DefaultStorageManifestPath(), bootstrap.StoragePod(options.EtcdDir)); err != nil {
		return err
	}
	for _, addon := range knownAddons {
		if err := writePodManifestFile(addonManifestPath(addon), addonPodTemplate(addon, options)); err != nil {
			return err
		}
	}
	return nil
}

func addonPodTemplate(addon AddonName, options initManifestOptions) *pod.Pod {
	switch addon {
	case AddonDNS:
		return bootstrap.DNSPod(options.DNSDir, options.DNSListenPort, options.IngressListenPort)
	case AddonServerless:
		return bootstrap.ServerlessNATSPod()
	case AddonMetrics:
		return bootstrap.MetricsServerPod()
	default:
		return nil
	}
}

func addonManifestPath(addon AddonName) string {
	switch addon {
	case AddonDNS:
		return DefaultDNSGatewayManifestPath()
	case AddonServerless:
		return DefaultServerlessNATSManifestPath()
	case AddonMetrics:
		return DefaultMetricsServerManifestPath()
	default:
		return filepath.Join(DefaultStaticPodDir(), string(addon)+".yaml")
	}
}

func writePodManifestFile(path string, p *pod.Pod) error {
	data, err := yaml.Marshal(p)
	if err != nil {
		return fmt.Errorf("encoding static pod manifest %s: %w", path, err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing static pod manifest %s: %w", path, err)
	}
	return nil
}

func (a *App) cni(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 || args[0] != "init" {
		return fmt.Errorf("usage: minik8s cni init [--pod-cidr cidr] [--gateway ip] [--route remote-cidr=node-ip]")
	}
	config, err := cniInitConfig(args[1:])
	if err != nil {
		return err
	}
	configPath, err := writeCNIConfig(config)
	if err != nil {
		return err
	}
	_ = ctx
	return writes(out, cliui.SuccessLine("cni config initialized at %s", configPath))
}

type cniInitRoute struct {
	Dst string `json:"dst"`
	GW  string `json:"gw"`
}

type cniInitIPAM struct {
	StatePath string `json:"statePath"`
}

type cniInitPluginConfig struct {
	CNIVersion string         `json:"cniVersion"`
	Name       string         `json:"name"`
	Type       string         `json:"type"`
	Bridge     string         `json:"bridge"`
	PodCIDR    string         `json:"podCIDR"`
	Gateway    string         `json:"gateway"`
	IPAM       cniInitIPAM    `json:"ipam"`
	Routes     []cniInitRoute `json:"routes,omitempty"`
}

func cniInitConfig(args []string) (cniInitPluginConfig, error) {
	config := cniInitPluginConfig{
		CNIVersion: "1.0.0",
		Name:       "minik8s",
		Type:       "mooring",
		Bridge:     "mk8s0",
		PodCIDR:    "10.244.0.0/24",
		Gateway:    "10.244.0.1",
		IPAM:       cniInitIPAM{StatePath: ".minik8s/state/cni-ipam.json"},
	}

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--pod-cidr":
			i++
			if i >= len(args) {
				return config, fmt.Errorf("missing value for --pod-cidr")
			}
			if _, _, err := net.ParseCIDR(args[i]); err != nil {
				return config, fmt.Errorf("invalid --pod-cidr %q: %w", args[i], err)
			}
			config.PodCIDR = args[i]
		case "--gateway":
			i++
			if i >= len(args) {
				return config, fmt.Errorf("missing value for --gateway")
			}
			if net.ParseIP(args[i]) == nil {
				return config, fmt.Errorf("invalid --gateway %q", args[i])
			}
			config.Gateway = args[i]
		case "--route":
			i++
			if i >= len(args) {
				return config, fmt.Errorf("missing value for --route")
			}
			route, err := parseCNIRoute(args[i])
			if err != nil {
				return config, err
			}
			config.Routes = append(config.Routes, route)
		default:
			return config, fmt.Errorf("unknown cni init flag %q", args[i])
		}
	}
	return config, nil
}

func writeCNIConfig(config cniInitPluginConfig) (string, error) {
	return writeCNIConfigTo(config, DefaultCNIBinDir(), DefaultCNIConfDir())
}

func writeCNIConfigTo(config cniInitPluginConfig, binDir, confDir string) (string, error) {
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return "", err
	}
	if err := os.MkdirAll(confDir, 0o755); err != nil {
		return "", err
	}
	configPath := filepath.Join(confDir, "10-mooring.conf")
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return "", err
	}
	data = append(data, '\n')
	if err := os.WriteFile(configPath, data, 0o644); err != nil {
		return "", err
	}
	return configPath, nil
}

func cniConfigExists(confDir string) (bool, error) {
	entries, err := os.ReadDir(confDir)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(name, ".conf") || strings.HasSuffix(name, ".conflist") || strings.HasSuffix(name, ".json") {
			return true, nil
		}
	}
	return false, nil
}

func cniConfigForPodCIDR(podCIDR string) (cniInitPluginConfig, error) {
	config, err := cniInitConfig([]string{"--pod-cidr", podCIDR})
	if err != nil {
		return config, err
	}
	gateway, err := gatewayForPodCIDR(podCIDR)
	if err != nil {
		return config, err
	}
	config.Gateway = gateway
	return config, nil
}

func gatewayForPodCIDR(podCIDR string) (string, error) {
	ip, network, err := net.ParseCIDR(podCIDR)
	if err != nil {
		return "", fmt.Errorf("invalid podCIDR %q: %w", podCIDR, err)
	}
	ip4 := ip.To4()
	if ip4 == nil {
		return "", fmt.Errorf("podCIDR %q must be IPv4", podCIDR)
	}
	gateway := append(net.IP(nil), ip4...)
	gateway[3]++
	if !network.Contains(gateway) {
		return "", fmt.Errorf("podCIDR %q has no usable gateway address", podCIDR)
	}
	return gateway.String(), nil
}

func parseCNIRoute(value string) (cniInitRoute, error) {
	dst, gw, ok := strings.Cut(value, "=")
	if !ok || dst == "" || gw == "" {
		return cniInitRoute{}, fmt.Errorf("route must use <remote-cidr>=<node-ip>, got %q", value)
	}
	if _, _, err := net.ParseCIDR(dst); err != nil {
		return cniInitRoute{}, fmt.Errorf("invalid route dst %q: %w", dst, err)
	}
	if net.ParseIP(gw) == nil {
		return cniInitRoute{}, fmt.Errorf("invalid route gw %q", gw)
	}
	return cniInitRoute{Dst: dst, GW: gw}, nil
}

type netRegistryOptions struct {
	listen   string
	leaseTTL time.Duration
}

func (a *App) netRegistry(ctx context.Context, args []string, out io.Writer) error {
	options, err := parseNetRegistryOptions(args)
	if err != nil {
		return err
	}
	store := netregistry.NewStore(options.leaseTTL)
	server := &http.Server{
		Addr:    options.listen,
		Handler: netregistry.NewHandler(store),
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.ListenAndServe()
	}()
	if err := writes(out, cliui.InfoLine("net-registry listening on %s", options.listen)); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return err
		}
		return ctx.Err()
	case err := <-errCh:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}

func (a *App) routeProxy(ctx context.Context, listen, routes string, out io.Writer) error {
	server := &http.Server{
		Addr:    listen,
		Handler: routeproxy.NewFileHandler(routes),
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.ListenAndServe()
	}()
	if err := writes(out, cliui.InfoLine("route-proxy listening on %s routes=%s", listen, routes)); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return err
		}
		return ctx.Err()
	case err := <-errCh:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}

func parseNetRegistryOptions(args []string) (netRegistryOptions, error) {
	options := netRegistryOptions{
		listen:   ":8088",
		leaseTTL: time.Minute,
	}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--listen":
			i++
			if i >= len(args) {
				return options, fmt.Errorf("missing value for --listen")
			}
			options.listen = args[i]
		case "--lease-ttl":
			i++
			if i >= len(args) {
				return options, fmt.Errorf("missing value for --lease-ttl")
			}
			ttl, err := time.ParseDuration(args[i])
			if err != nil {
				return options, fmt.Errorf("invalid --lease-ttl %q: %w", args[i], err)
			}
			options.leaseTTL = ttl
		default:
			return options, fmt.Errorf("unknown net-registry flag %q", args[i])
		}
	}
	return options, nil
}

type netDOptions struct {
	nodeName    string
	nodeIP      string
	podCIDR     string
	registryURL string
	interval    time.Duration
	vxlanID     int
	vxlanPort   int
	vxlanName   string
	once        bool
}

func (a *App) netd(ctx context.Context, args []string, out io.Writer) error {
	options, err := parseNetDOptions(args)
	if err != nil {
		return err
	}
	agent := netagent.New(netagent.Options{
		NodeName:  options.nodeName,
		NodeIP:    options.nodeIP,
		PodCIDR:   options.podCIDR,
		VXLANID:   options.vxlanID,
		VXLANPort: options.vxlanPort,
		VXLANName: options.vxlanName,
		Registry:  netregistry.NewClient(options.registryURL),
	})
	if options.once {
		if err := agent.Sync(ctx); err != nil {
			return err
		}
		return writes(out, cliui.SuccessLine("netd synced VXLAN overlay for %s", options.nodeName))
	}
	if err := writes(out, cliui.InfoLine("netd started node=%s registry=%s", options.nodeName, options.registryURL)); err != nil {
		return err
	}
	return agent.Run(ctx, options.interval)
}

func parseNetDOptions(args []string) (netDOptions, error) {
	options := netDOptions{
		interval:  5 * time.Second,
		vxlanID:   42,
		vxlanPort: 4789,
		vxlanName: "mk8s-vxlan",
	}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--node-name":
			i++
			if i >= len(args) {
				return options, fmt.Errorf("missing value for --node-name")
			}
			options.nodeName = args[i]
		case "--node-ip":
			i++
			if i >= len(args) {
				return options, fmt.Errorf("missing value for --node-ip")
			}
			options.nodeIP = args[i]
		case "--pod-cidr":
			i++
			if i >= len(args) {
				return options, fmt.Errorf("missing value for --pod-cidr")
			}
			options.podCIDR = args[i]
		case "--registry":
			i++
			if i >= len(args) {
				return options, fmt.Errorf("missing value for --registry")
			}
			options.registryURL = args[i]
		case "--interval":
			i++
			if i >= len(args) {
				return options, fmt.Errorf("missing value for --interval")
			}
			interval, err := time.ParseDuration(args[i])
			if err != nil {
				return options, fmt.Errorf("invalid --interval %q: %w", args[i], err)
			}
			options.interval = interval
		case "--vxlan-id":
			i++
			if i >= len(args) {
				return options, fmt.Errorf("missing value for --vxlan-id")
			}
			id, err := strconv.Atoi(args[i])
			if err != nil || id <= 0 {
				return options, fmt.Errorf("invalid --vxlan-id %q", args[i])
			}
			options.vxlanID = id
		case "--vxlan-port":
			i++
			if i >= len(args) {
				return options, fmt.Errorf("missing value for --vxlan-port")
			}
			port, err := strconv.Atoi(args[i])
			if err != nil || port <= 0 || port > 65535 {
				return options, fmt.Errorf("invalid --vxlan-port %q", args[i])
			}
			options.vxlanPort = port
		case "--vxlan-name":
			i++
			if i >= len(args) {
				return options, fmt.Errorf("missing value for --vxlan-name")
			}
			if strings.TrimSpace(args[i]) == "" {
				return options, fmt.Errorf("invalid --vxlan-name %q", args[i])
			}
			options.vxlanName = args[i]
		case "--once":
			options.once = true
		default:
			return options, fmt.Errorf("unknown netd flag %q", args[i])
		}
	}
	if options.nodeName == "" {
		return options, fmt.Errorf("--node-name is required")
	}
	if options.nodeIP == "" {
		return options, fmt.Errorf("--node-ip is required")
	}
	if options.podCIDR == "" {
		return options, fmt.Errorf("--pod-cidr is required")
	}
	if options.registryURL == "" {
		return options, fmt.Errorf("--registry is required")
	}
	if err := netregistry.ValidateNode(netregistry.Node{
		Name:    options.nodeName,
		NodeIP:  options.nodeIP,
		PodCIDR: options.podCIDR,
	}); err != nil {
		return options, err
	}
	return options, nil
}

type bridgeOptions struct {
	listen                 string
	serviceSyncInterval    time.Duration
	dnsSyncInterval        time.Duration
	replicaSetSyncInterval time.Duration
	hpaSyncInterval        time.Duration
	clusterCIDR            string
	nodeCIDRMaskSize       int
	addons                 addonSet
	gatewayIP              string
	dnsListenPort          int32
	ingressListenPort      int32
}

func (a *App) bridge(ctx context.Context, args []string, out io.Writer) error {
	options, err := parseBridgeOptions(args)
	if err != nil {
		return err
	}
	harborURL, err := bridgeHarborURL(options.listen)
	if err != nil {
		return err
	}
	if err := writeLocalConfig(DefaultLocalConfigPath(), localConfig{Harbor: harborURL}); err != nil {
		return err
	}
	a.controlBridge.SetNodeCIDRConfig(options.clusterCIDR, options.nodeCIDRMaskSize)
	server := &http.Server{
		Addr:    options.listen,
		Handler: a.controlBridge.Handler(),
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.ListenAndServe()
	}()
	if options.addons.Enabled(AddonDNS) && options.dnsSyncInterval > 0 {
		if err := writeDNSGatewayConfigs(DefaultCoreDNSCorefilePath(), DefaultNginxDNSConfigPath()); err != nil {
			return err
		}
		go a.runDNSSyncLoop(ctx, options.dnsSyncInterval, options.gatewayIP)
	}
	a.controlBridge.RegisterDefaultControllers(options.serviceSyncInterval, options.replicaSetSyncInterval, options.hpaSyncInterval, 5*time.Second)
	a.controlBridge.StartControllers(ctx)
	if natsURL := strings.TrimSpace(os.Getenv("MINIK8S_NATS_URL")); natsURL != "" {
		go bridgeServerless.NewController(a.controlBridge.FunctionStore(), a.controlBridge.EventTriggerStore(), natsURL).Run(ctx, 5*time.Second)
	}
	if err := writes(out, cliui.InfoLine("bridge listening on %s", options.listen)); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return err
		}
		return ctx.Err()
	case err := <-errCh:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}

func BridgeAddons(args []string) (addonSet, error) {
	if len(args) == 0 || args[0] != "bridge" {
		return newAddonSet(), nil
	}
	options, err := parseBridgeOptions(args[1:])
	if err != nil {
		return nil, err
	}
	return options.addons, nil
}

func StartBridgeDependencies(ctx context.Context, args []string, out io.Writer) (func(), error) {
	options, err := parseBridgeOptions(nil)
	if err != nil {
		return func() {}, err
	}
	if len(args) > 0 && args[0] == "bridge" {
		parsed, err := parseBridgeOptions(args[1:])
		if err != nil {
			return func() {}, err
		}
		options = parsed
	}
	runtime, err := dockerruntime.NewDockerRuntime()
	if err != nil {
		return func() {}, fmt.Errorf("creating docker runtime for bridge dependencies: %w", err)
	}
	if err := runtime.CleanupPod(ctx, "minik8s-system", "storage-etcd"); err != nil {
		_ = runtime.Close()
		return func() {}, fmt.Errorf("cleaning stale bridge dependencies: %w", err)
	}
	for _, name := range []string{"dns-gateway", "serverless-nats", "metrics-server"} {
		if err := runtime.CleanupPod(ctx, "minik8s-system", name); err != nil {
			_ = runtime.Close()
			return func() {}, fmt.Errorf("cleaning stale bridge addon %s: %w", name, err)
		}
	}
	if err := ensureBridgeDependencyPortsFree(options); err != nil {
		_ = runtime.Close()
		return func() {}, err
	}
	etcdDir, err := bridgeDependencyEtcdDir()
	if err != nil {
		_ = runtime.Close()
		return func() {}, err
	}
	if err := os.MkdirAll(etcdDir, 0o755); err != nil {
		_ = runtime.Close()
		return func() {}, fmt.Errorf("creating bridge dependency state dir: %w", err)
	}
	dnsDir, err := filepath.Abs(DefaultDNSDir())
	if err != nil {
		_ = runtime.Close()
		return func() {}, fmt.Errorf("resolving dns config dir: %w", err)
	}
	if options.addons.Enabled(AddonDNS) {
		if err := writeDNSGatewayConfigs(DefaultCoreDNSCorefilePath(), DefaultNginxDNSConfigPath()); err != nil {
			_ = runtime.Close()
			return func() {}, fmt.Errorf("writing dns gateway config: %w", err)
		}
		if err := writeTextFile(DefaultDNSHostsPath(), "# generated by minik8s dns-sync\n"); err != nil {
			_ = runtime.Close()
			return func() {}, fmt.Errorf("initializing dns hosts: %w", err)
		}
		if err := writeTextFile(DefaultDNSRoutesPath(), "{\"hosts\":[]}\n"); err != nil {
			_ = runtime.Close()
			return func() {}, fmt.Errorf("initializing dns routes: %w", err)
		}
	}
	privatePods, err := bridgeDependencyPods(options, etcdDir, dnsDir)
	if err != nil {
		_ = runtime.Close()
		return func() {}, err
	}
	runCtx, cancel := context.WithCancel(ctx)
	privateNode := bootstrap.DefaultNode()
	privateClient := bootstrap.NewPrivatePodClient(privateNode, privatePods...)
	k := nodeSailer.New(nodeSailer.Config{
		Node:     privateNode,
		Runtime:  runtime,
		Client:   privateClient,
		Interval: 2 * time.Second,
	})
	errCh := make(chan error, 1)
	go func() {
		errCh <- k.Run(runCtx)
	}()
	cleanup := func() {
		cancel()
		select {
		case <-errCh:
		case <-time.After(5 * time.Second):
		}
		privateClient.SetPods()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		_ = k.SyncOnce(shutdownCtx)
		shutdownCancel()
		_ = runtime.Close()
	}
	if err := writes(out, cliui.InfoLine("bridge dependencies starting via private sailer node=%s", bootstrap.DefaultNodeName)); err != nil {
		cleanup()
		return func() {}, err
	}
	if err := waitForBridgeDependenciesReady(runCtx, errCh, options, 45*time.Second); err != nil {
		cleanup()
		return func() {}, err
	}
	status := "bridge dependencies ready etcd=http://127.0.0.1:2379"
	if options.addons.Enabled(AddonServerless) {
		status += " nats=nats://127.0.0.1:4222"
	}
	if options.addons.Enabled(AddonDNS) {
		status += fmt.Sprintf(" dns=127.0.0.1:%d ingress=127.0.0.1:%d", options.dnsListenPort, options.ingressListenPort)
	}
	if err := writes(out, cliui.SuccessLine("%s", status)); err != nil {
		cleanup()
		return func() {}, err
	}
	return cleanup, nil
}

func bridgeDependencyPods(options bridgeOptions, etcdDir, dnsDir string) ([]*pod.Pod, error) {
	deps, err := staticPodOrDefault(DefaultStorageManifestPath(), bootstrap.StoragePod(etcdDir))
	if err != nil {
		return nil, err
	}
	pods := []*pod.Pod{deps}
	for _, addon := range options.addons.Names() {
		addonPod, err := readRequiredAddonManifest(addon)
		if err != nil {
			return nil, err
		}
		pods = append(pods, addonPod)
	}
	return pods, nil
}

func readRequiredAddonManifest(addon AddonName) (*pod.Pod, error) {
	path := addonManifestPath(addon)
	p, err := readStaticPodManifest(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("addon %s manifest %s is missing; run minik8s init --force", addon, path)
		}
		return nil, err
	}
	return normalizeStaticDependencyPod(p), nil
}

func staticPodOrDefault(path string, fallback *pod.Pod) (*pod.Pod, error) {
	p, err := readStaticPodManifest(path)
	if err != nil {
		if os.IsNotExist(err) {
			return normalizeStaticDependencyPod(fallback), nil
		}
		return nil, err
	}
	return normalizeStaticDependencyPod(p), nil
}

func readStaticPodManifest(path string) (*pod.Pod, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, err
		}
		return nil, fmt.Errorf("reading static pod manifest %s: %w", path, err)
	}
	var p pod.Pod
	if err := yaml.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("reading static pod manifest %s: %w", path, err)
	}
	if strings.TrimSpace(p.Name) == "" {
		return nil, fmt.Errorf("reading static pod manifest %s: metadata.name is required", path)
	}
	if len(p.Spec.Containers) == 0 {
		return nil, fmt.Errorf("reading static pod manifest %s: spec.containers must contain at least one container", path)
	}
	return &p, nil
}

func normalizeStaticDependencyPod(p *pod.Pod) *pod.Pod {
	if p == nil {
		return nil
	}
	copy := p.DeepCopy()
	if copy.Kind == "" {
		copy.Kind = "Pod"
	}
	if copy.APIVersion == "" {
		copy.APIVersion = "v1"
	}
	if copy.Namespace == "" {
		copy.Namespace = "minik8s-system"
	}
	if copy.Annotations == nil {
		copy.Annotations = make(map[string]string)
	}
	copy.Annotations[bootstrap.AnnotationInternal] = "true"
	if copy.Spec.NodeName == "" {
		copy.Spec.NodeName = bootstrap.DefaultNodeName
	}
	if copy.Spec.RestartPolicy == "" {
		copy.Spec.RestartPolicy = pod.RestartPolicyAlways
	}
	if copy.Status.Phase == "" {
		copy.Status.Phase = pod.PodPending
	}
	return copy
}

func bridgeDependencyEtcdDir() (string, error) {
	etcdDir := filepath.Join(filepath.Dir(DefaultStatePath()), "bridge-deps", "etcd")
	absolute, err := filepath.Abs(etcdDir)
	if err != nil {
		return "", fmt.Errorf("resolving bridge dependency state dir: %w", err)
	}
	return absolute, nil
}

func ensureBridgeDependencyPortsFree(options bridgeOptions) error {
	ports := []string{"2379"}
	if options.addons.Enabled(AddonServerless) {
		ports = append(ports, "4222")
	}
	if options.addons.Enabled(AddonDNS) {
		ports = append(ports, fmt.Sprintf("%d", options.dnsListenPort), fmt.Sprintf("%d", options.ingressListenPort))
	}
	for _, port := range ports {
		ln, err := net.Listen("tcp", "127.0.0.1:"+port)
		if err != nil {
			return fmt.Errorf("bridge dependency port %s is already in use", port)
		}
		if err := ln.Close(); err != nil {
			return fmt.Errorf("closing bridge dependency port probe %s: %w", port, err)
		}
	}
	if options.addons.Enabled(AddonDNS) {
		ln, err := net.ListenPacket("udp", fmt.Sprintf("127.0.0.1:%d", options.dnsListenPort))
		if err != nil {
			return fmt.Errorf("bridge dependency udp port %d is already in use", options.dnsListenPort)
		}
		if err := ln.Close(); err != nil {
			return fmt.Errorf("closing bridge dependency udp port probe %d: %w", options.dnsListenPort, err)
		}
	}
	return nil
}

var bridgeDependencyEtcdProbe = store.Probe
var bridgeDependencyTCPReady = tcpPortReady

func waitForBridgeDependenciesReady(ctx context.Context, errCh <-chan error, options bridgeOptions, timeout time.Duration) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	lastEtcdError := ""
	for {
		ready, err := etcdDependencyReady(ctx)
		if err != nil {
			message := err.Error()
			if message != lastEtcdError {
				minilog.Warn("bridge-dependency", "etcd=http://127.0.0.1:2379 waiting error=%v", err)
				lastEtcdError = message
			}
		} else if ready {
			lastEtcdError = ""
		}
		if options.addons.Enabled(AddonServerless) {
			ready = ready && tcpPortReady("127.0.0.1:4222")
		}
		if options.addons.Enabled(AddonDNS) {
			ready = ready && tcpPortReady(fmt.Sprintf("127.0.0.1:%d", options.ingressListenPort))
		}
		if ready {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-errCh:
			if err != nil && err != context.Canceled {
				return fmt.Errorf("private bridge dependency sailer stopped: %w", err)
			}
			return fmt.Errorf("private bridge dependency sailer stopped")
		case <-deadline.C:
			return fmt.Errorf("timed out waiting for bridge dependencies for addons %s", options.addons.String())
		case <-ticker.C:
		}
	}
}

func etcdDependencyReady(ctx context.Context) (bool, error) {
	if !bridgeDependencyTCPReady("127.0.0.1:2379") {
		return false, nil
	}
	probeCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err := bridgeDependencyEtcdProbe(probeCtx, []string{"http://127.0.0.1:2379"}); err != nil {
		return false, err
	}
	return true, nil
}

func tcpPortReady(address string) bool {
	conn, err := net.DialTimeout("tcp", address, 250*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func parseBridgeOptions(args []string) (bridgeOptions, error) {
	options := bridgeOptions{listen: ":8080", addons: defaultAddonSet(), serviceSyncInterval: 5 * time.Second, dnsSyncInterval: 5 * time.Second, replicaSetSyncInterval: 5 * time.Second, hpaSyncInterval: 15 * time.Second, clusterCIDR: "10.244.0.0/16", nodeCIDRMaskSize: 24, gatewayIP: "127.0.0.1", dnsListenPort: 53, ingressListenPort: 80}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--listen":
			i++
			if i >= len(args) {
				return options, fmt.Errorf("missing value for --listen")
			}
			options.listen = args[i]
		case "--addons":
			i++
			if i >= len(args) {
				return options, fmt.Errorf("missing value for --addons")
			}
			addons, err := parseAddonSet(args[i])
			if err != nil {
				return options, err
			}
			options.addons = addons
		case "--service-sync-interval":
			i++
			if i >= len(args) {
				return options, fmt.Errorf("missing value for --service-sync-interval")
			}
			interval, err := time.ParseDuration(args[i])
			if err != nil {
				return options, fmt.Errorf("invalid --service-sync-interval %q: %w", args[i], err)
			}
			options.serviceSyncInterval = interval
		case "--dns-sync-interval":
			i++
			if i >= len(args) {
				return options, fmt.Errorf("missing value for --dns-sync-interval")
			}
			interval, err := time.ParseDuration(args[i])
			if err != nil {
				return options, fmt.Errorf("invalid --dns-sync-interval %q: %w", args[i], err)
			}
			options.dnsSyncInterval = interval
		case "--gateway-ip":
			i++
			if i >= len(args) {
				return options, fmt.Errorf("missing value for --gateway-ip")
			}
			if net.ParseIP(args[i]) == nil {
				return options, fmt.Errorf("invalid --gateway-ip %q", args[i])
			}
			options.gatewayIP = args[i]
		case "--dns-listen":
			i++
			if i >= len(args) {
				return options, fmt.Errorf("missing value for --dns-listen")
			}
			port, err := listenPort(args[i])
			if err != nil {
				return options, fmt.Errorf("invalid --dns-listen %q: %w", args[i], err)
			}
			options.dnsListenPort = port
		case "--ingress-listen":
			i++
			if i >= len(args) {
				return options, fmt.Errorf("missing value for --ingress-listen")
			}
			port, err := listenPort(args[i])
			if err != nil {
				return options, fmt.Errorf("invalid --ingress-listen %q: %w", args[i], err)
			}
			options.ingressListenPort = port
		case "--replicaset-sync-interval":
			i++
			if i >= len(args) {
				return options, fmt.Errorf("missing value for --replicaset-sync-interval")
			}
			interval, err := time.ParseDuration(args[i])
			if err != nil {
				return options, fmt.Errorf("invalid --replicaset-sync-interval %q: %w", args[i], err)
			}
			options.replicaSetSyncInterval = interval
		case "--hpa-sync-interval":
			i++
			if i >= len(args) {
				return options, fmt.Errorf("missing value for --hpa-sync-interval")
			}
			interval, err := time.ParseDuration(args[i])
			if err != nil {
				return options, fmt.Errorf("invalid --hpa-sync-interval %q: %w", args[i], err)
			}
			options.hpaSyncInterval = interval
		case "--cluster-cidr":
			i++
			if i >= len(args) {
				return options, fmt.Errorf("missing value for --cluster-cidr")
			}
			if _, _, err := net.ParseCIDR(args[i]); err != nil {
				return options, fmt.Errorf("invalid --cluster-cidr %q: %w", args[i], err)
			}
			options.clusterCIDR = args[i]
		case "--node-cidr-mask-size":
			i++
			if i >= len(args) {
				return options, fmt.Errorf("missing value for --node-cidr-mask-size")
			}
			mask, err := strconv.Atoi(args[i])
			if err != nil || mask <= 0 || mask > 32 {
				return options, fmt.Errorf("invalid --node-cidr-mask-size %q", args[i])
			}
			options.nodeCIDRMaskSize = mask
		default:
			return options, fmt.Errorf("unknown bridge flag %q", args[i])
		}
	}
	_, cluster, err := net.ParseCIDR(options.clusterCIDR)
	if err != nil {
		return options, fmt.Errorf("invalid --cluster-cidr %q: %w", options.clusterCIDR, err)
	}
	ones, bits := cluster.Mask.Size()
	if bits != 32 {
		return options, fmt.Errorf("--cluster-cidr %q must be IPv4", options.clusterCIDR)
	}
	if options.nodeCIDRMaskSize < ones || options.nodeCIDRMaskSize > bits {
		return options, fmt.Errorf("--node-cidr-mask-size must be between %d and %d for %s", ones, bits, options.clusterCIDR)
	}
	return options, nil
}

func listenPort(value string) (int32, error) {
	value = strings.TrimPrefix(value, ":")
	_, portText, err := net.SplitHostPort(value)
	if err == nil {
		value = portText
	}
	port, err := strconv.Atoi(value)
	if err != nil || port <= 0 || port > 65535 {
		return 0, fmt.Errorf("port must be between 1 and 65535")
	}
	return int32(port), nil
}

func (a *App) runDNSSyncLoop(ctx context.Context, interval time.Duration, gatewayIP string) {
	syncOnce := func() {
		if err := dnssync.Sync(ctx, dnssync.Config{
			DNSStore:     a.controlBridge.DNSStore(),
			ServiceStore: a.controlBridge.ServiceStore(),
			GatewayIP:    gatewayIP,
			HostsPath:    DefaultDNSHostsPath(),
			RoutesPath:   DefaultDNSRoutesPath(),
		}); err != nil {
			minilog.Warn("dns-periodic-sync", "error=%v", err)
		}
	}
	syncOnce()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			syncOnce()
		}
	}
}

func writeDNSGatewayConfigs(corefilePath, nginxPath string) error {
	corefile := fmt.Sprintf(`.:53 {
    hosts %s {
        fallthrough
    }
    forward . /etc/resolv.conf
    log
}
`, "/minik8s-dns/hosts")
	if err := writeTextFile(corefilePath, corefile); err != nil {
		return err
	}
	nginx := `events {}
http {
    server {
        listen 80;
        server_name _;
        location / {
            proxy_set_header Host $host;
            proxy_set_header X-Real-IP $remote_addr;
            proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
            proxy_set_header X-Forwarded-Proto $scheme;
            proxy_pass http://127.0.0.1:18081;
        }
    }
}
`
	return writeTextFile(nginxPath, nginx)
}

func writeTextFile(path, value string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(value), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

type sailerOptions struct {
	nodeFile      string
	nodeName      string
	harbor        string
	nodeIP        string
	podCIDR       string
	interval      time.Duration
	vxlanID       int
	vxlanPort     int
	vxlanName     string
	proxyDisabled bool
	once          bool
	clusterDNS    string
}

type localSailerConfig struct {
	APIServer string `json:"apiserver"`
	NodeName  string `json:"nodeName"`
	NodeIP    string `json:"nodeIP"`
	PodCIDR   string `json:"podCIDR"`
	NodeToken string `json:"nodeToken"`
}

type localConfig struct {
	Harbor string `json:"harbor"`
}

type sailerNetworkMode int

const (
	sailerNetworkBuiltIn sailerNetworkMode = iota
	sailerNetworkFlannel
	sailerNetworkMooringCNI
)

func (a *App) sailer(ctx context.Context, args []string, out io.Writer) error {
	options, err := parseSailerOptions(args)
	if err != nil {
		return err
	}
	nodeConfig, err := podyaml.LoadNodeFromFile(options.nodeFile)
	if err != nil {
		return err
	}
	options.nodeName = nodeConfig.Name()
	options.nodeIP = nodeConfig.InternalIP()
	options.podCIDR = nodeConfig.Spec.PodCIDR
	podClient := nodeSailer.NewHTTPPodClient(options.harbor, a.httpClient)
	assignedNode, err := a.bootstrapSailerNode(ctx, podClient, nodeConfig)
	if err != nil {
		return err
	}
	options.nodeName = assignedNode.Name()
	options.nodeIP = assignedNode.InternalIP()
	options.podCIDR = assignedNode.Spec.PodCIDR
	return a.runAssignedSailer(ctx, options, podClient, assignedNode, out)
}

func (a *App) sailerJoin(ctx context.Context, apiserver, token, nodeFile string, out io.Writer) error {
	if strings.TrimSpace(apiserver) == "" {
		return fmt.Errorf("--apiserver is required")
	}
	if strings.TrimSpace(token) == "" {
		return fmt.Errorf("--token is required")
	}
	if strings.TrimSpace(nodeFile) == "" {
		return fmt.Errorf("missing required -f flag")
	}
	nodeConfig, err := podyaml.LoadNodeFromFile(nodeFile)
	if err != nil {
		return err
	}
	if nodeConfig.InternalIP() == "" {
		return fmt.Errorf("node InternalIP is required")
	}
	client := nodeSailer.NewHTTPPodClient(apiserver, a.httpClient)
	joined, err := client.JoinNode(ctx, token, nodeConfig)
	if err != nil {
		return err
	}
	if joined.NodeToken == "" {
		return fmt.Errorf("bridge did not return a node token")
	}
	if joined.Node.Spec.PodCIDR == "" {
		return fmt.Errorf("bridge did not assign a podCIDR")
	}
	conf := localSailerConfig{
		APIServer: strings.TrimRight(apiserver, "/"),
		NodeName:  joined.Node.Name(),
		NodeIP:    joined.Node.InternalIP(),
		PodCIDR:   joined.Node.Spec.PodCIDR,
		NodeToken: joined.NodeToken,
	}
	if err := writeLocalSailerConfig(DefaultSailerConfigPath(), conf); err != nil {
		return err
	}
	if err := writeLocalConfig(DefaultLocalConfigPath(), localConfig{Harbor: conf.APIServer}); err != nil {
		return err
	}
	return writes(out, cliui.SuccessLine("sailer joined node=%s apiserver=%s", conf.NodeName, conf.APIServer))
}

func (a *App) sailerRun(ctx context.Context, args []string, out io.Writer) error {
	options, err := parseSailerRunOptions(args)
	if err != nil {
		return err
	}
	conf, err := readLocalSailerConfig(DefaultSailerConfigPath())
	if err != nil {
		return err
	}
	options.harbor = conf.APIServer
	options.nodeName = conf.NodeName
	options.nodeIP = conf.NodeIP
	options.podCIDR = conf.PodCIDR
	podClient := nodeSailer.NewHTTPPodClientWithToken(conf.APIServer, conf.NodeToken, a.httpClient)
	assignedNode, err := podClient.GetNode(ctx, conf.NodeName)
	if err != nil {
		return err
	}
	if assignedNode.Spec.PodCIDR == "" {
		assignedNode.Spec.PodCIDR = conf.PodCIDR
	}
	if assignedNode.InternalIP() == "" && conf.NodeIP != "" {
		assignedNode.Status.Addresses = append(assignedNode.Status.Addresses, node.NodeAddress{Type: node.NodeAddressInternalIP, Address: conf.NodeIP})
	}
	options.nodeName = assignedNode.Name()
	options.nodeIP = assignedNode.InternalIP()
	options.podCIDR = assignedNode.Spec.PodCIDR
	return a.runAssignedSailer(ctx, options, podClient, assignedNode, out)
}

func (a *App) runAssignedSailer(ctx context.Context, options sailerOptions, podClient *nodeSailer.HTTPPodClient, assignedNode *node.Node, out io.Writer) error {
	networkMode, err := a.ensureManifestCNIIfApplied(ctx, options.harbor, assignedNode)
	if err != nil {
		return err
	}
	if networkMode == sailerNetworkBuiltIn {
		hasConfig, err := cniConfigExists(DefaultCNIConfDir())
		if err != nil {
			return err
		}
		if !hasConfig {
			cniConfig, err := cniConfigForPodCIDR(options.podCIDR)
			if err != nil {
				return err
			}
			if _, err := writeCNIConfig(cniConfig); err != nil {
				return err
			}
		}
	}
	network := cniNetworkManager{runner: cni.NewRunner(cni.Config{BinDir: DefaultCNIBinDir(), ConfDir: DefaultCNIConfDir()})}
	k := nodeSailer.New(nodeSailer.Config{
		Node:         assignedNode,
		Runtime:      a.runtime,
		Network:      network,
		Client:       podClient,
		ServiceProxy: a.sailerServiceProxy(options),
		Interval:     options.interval,
		ClusterDNS:   options.clusterDNS,
	})
	var networkAgent *netagent.Agent
	if networkMode != sailerNetworkFlannel {
		networkAgent, err = a.sailerNetworkAgent(options)
	}
	if err != nil {
		return err
	}
	if options.once {
		if networkAgent != nil {
			if err := networkAgent.Sync(ctx); err != nil {
				return err
			}
		}
		if err := k.SyncOnce(ctx); err != nil {
			return err
		}
		return writes(out, cliui.SuccessLine("sailer synced node=%s", options.nodeName))
	}
	if err := writes(out, cliui.InfoLine("sailer started node=%s harbor=%s", options.nodeName, options.harbor)); err != nil {
		return err
	}
	if networkAgent != nil {
		return runSailerWithNetwork(ctx, k, networkAgent, options.interval)
	}
	return k.Run(ctx)
}

func (a *App) ensureManifestCNIIfApplied(ctx context.Context, harborURL string, assignedNode *node.Node) (sailerNetworkMode, error) {
	flannelActive, err := a.ensureFlannelIfApplied(ctx, harborURL, assignedNode)
	if err != nil {
		return sailerNetworkBuiltIn, err
	}
	if flannelActive {
		return sailerNetworkFlannel, nil
	}
	minik8sActive, err := a.ensureMooringCNIIfApplied(ctx, harborURL, assignedNode)
	if err != nil {
		return sailerNetworkBuiltIn, err
	}
	if minik8sActive {
		return sailerNetworkMooringCNI, nil
	}
	return sailerNetworkBuiltIn, nil
}

func (a *App) ensureFlannelIfApplied(ctx context.Context, harborURL string, assignedNode *node.Node) (bool, error) {
	client, err := newControlPlaneClient(harborURL, a.httpClient)
	if err != nil {
		return false, err
	}
	cm, err := client.GetConfigMap(ctx, k8scompat.FlannelConfigMap, k8scompat.FlannelNamespace)
	if err != nil {
		if apiErr, ok := err.(controlPlaneError); ok && apiErr.statusCode == http.StatusNotFound {
			return false, a.cleanupFlannel(ctx, assignedNode)
		}
		return false, err
	}
	ds, err := client.GetDaemonSet(ctx, k8scompat.FlannelDaemonSet, k8scompat.FlannelNamespace)
	if err != nil {
		if apiErr, ok := err.(controlPlaneError); ok && apiErr.statusCode == http.StatusNotFound {
			return false, a.cleanupFlannel(ctx, assignedNode)
		}
		return false, err
	}
	runner := a.flannelRunner
	if runner == nil {
		runner = DockerFlannelRunner{}
	}
	if err := runner.Ensure(ctx, FlannelOptions{
		HarborURL:  harborURL,
		Node:       assignedNode,
		ConfigMap:  cm,
		DaemonSet:  ds,
		CNIBinDir:  DefaultCNIBinDir(),
		CNIConfDir: DefaultCNIConfDir(),
	}); err != nil {
		return false, err
	}
	return true, nil
}

func (a *App) ensureMooringCNIIfApplied(ctx context.Context, harborURL string, assignedNode *node.Node) (bool, error) {
	client, err := newControlPlaneClient(harborURL, a.httpClient)
	if err != nil {
		return false, err
	}
	cm, err := client.GetConfigMap(ctx, k8scompat.MooringCNIConfigMap, k8scompat.MooringCNINamespace)
	if err != nil {
		if apiErr, ok := err.(controlPlaneError); ok && apiErr.statusCode == http.StatusNotFound {
			return false, nil
		}
		return false, err
	}
	ds, err := client.GetDaemonSet(ctx, k8scompat.MooringCNIDaemonSet, k8scompat.MooringCNINamespace)
	if err != nil {
		return false, err
	}
	runner := a.mooringCNIRunner
	if runner == nil {
		runner = LocalMooringCNIRunner{}
	}
	if err := runner.Ensure(ctx, MooringCNIOptions{
		Node:       assignedNode,
		ConfigMap:  cm,
		DaemonSet:  ds,
		CNIBinDir:  DefaultCNIBinDir(),
		CNIConfDir: DefaultCNIConfDir(),
	}); err != nil {
		return false, err
	}
	return true, nil
}

func (a *App) cleanupFlannel(ctx context.Context, assignedNode *node.Node) error {
	if assignedNode == nil {
		return fmt.Errorf("node is required")
	}
	runner := a.flannelRunner
	if runner == nil {
		runner = DockerFlannelRunner{}
	}
	return runner.Cleanup(ctx, FlannelCleanupOptions{
		NodeName:   assignedNode.Name(),
		CNIBinDir:  DefaultCNIBinDir(),
		CNIConfDir: DefaultCNIConfDir(),
	})
}

func (a *App) bootstrapSailerNode(ctx context.Context, client *nodeSailer.HTTPPodClient, nodeConfig *node.Node) (*node.Node, error) {
	if nodeConfig == nil {
		return nil, fmt.Errorf("node yaml is required")
	}
	if nodeConfig.Name() == "" {
		return nil, fmt.Errorf("node name is required")
	}
	if nodeConfig.InternalIP() == "" {
		return nil, fmt.Errorf("node InternalIP is required")
	}
	if _, err := client.ListAssignedPods(ctx, nodeSailer.NodeHeartbeat{Node: nodeConfig}); err != nil {
		return nil, err
	}
	assigned, err := client.GetNode(ctx, nodeConfig.Name())
	if err != nil {
		return nil, err
	}
	if assigned.Spec.PodCIDR == "" {
		return nil, fmt.Errorf("node podCIDR is not assigned")
	}
	if assigned.InternalIP() == "" {
		return nil, fmt.Errorf("node InternalIP is required")
	}
	return assigned, nil
}

func (a *App) sailerNetworkAgent(options sailerOptions) (*netagent.Agent, error) {
	if options.nodeIP == "" && options.podCIDR == "" {
		return nil, nil
	}
	if options.nodeIP == "" {
		return nil, fmt.Errorf("--node-ip is required when --pod-cidr is set")
	}
	if options.podCIDR == "" {
		return nil, fmt.Errorf("--pod-cidr is required when --node-ip is set")
	}
	return netagent.New(netagent.Options{
		NodeName:  options.nodeName,
		NodeIP:    options.nodeIP,
		PodCIDR:   options.podCIDR,
		VXLANID:   options.vxlanID,
		VXLANPort: options.vxlanPort,
		VXLANName: options.vxlanName,
		Registry:  netregistry.NewClientWithHTTPClient(options.harbor, a.httpClient),
		Runner:    a.netRunner,
	}), nil
}

func runSailerWithNetwork(ctx context.Context, k *nodeSailer.Sailer, agent *netagent.Agent, interval time.Duration) error {
	if err := agent.Sync(ctx); err != nil {
		return err
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	errCh := make(chan error, 2)
	go func() {
		errCh <- agent.Run(runCtx, interval)
	}()
	go func() {
		errCh <- k.Run(runCtx)
	}()
	err := <-errCh
	cancel()
	return err
}

func (a *App) bridgeTokenSet(token string, ttl time.Duration, out io.Writer) error {
	if err := tokens.SetBootstrapToken(DefaultBootstrapTokenPath(), token, ttl, time.Now().UTC()); err != nil {
		return err
	}
	return writes(out, cliui.SuccessLine("bridge bootstrap token set path=%s expiresIn=%s", DefaultBootstrapTokenPath(), ttl))
}

func (a *App) bridgeTokenClear(out io.Writer) error {
	if err := tokens.ClearBootstrapToken(DefaultBootstrapTokenPath()); err != nil {
		return err
	}
	return writes(out, cliui.SuccessLine("bridge bootstrap token cleared path=%s", DefaultBootstrapTokenPath()))
}

func (a *App) bridgeTokenStatus(out io.Writer) error {
	status, err := tokens.BootstrapTokenStatus(DefaultBootstrapTokenPath(), time.Now().UTC())
	if err != nil {
		return err
	}
	if !status.Configured {
		return writes(out, cliui.InfoLine("bridge bootstrap token not configured path=%s", DefaultBootstrapTokenPath()))
	}
	state := "valid"
	if status.Expired {
		state = "expired"
	}
	return writes(out, cliui.InfoLine("bridge bootstrap token %s path=%s expiresAt=%s", state, DefaultBootstrapTokenPath(), status.ExpiresAt.Format(time.RFC3339)))
}

func parseSailerOptions(args []string) (sailerOptions, error) {
	options := sailerOptions{
		interval:  5 * time.Second,
		vxlanID:   42,
		vxlanPort: 4789,
		vxlanName: "mk8s-vxlan",
	}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--node-name", "--node-ip", "--pod-cidr":
			return options, fmt.Errorf("%s is no longer supported; pass a Node YAML as minik8s sailer <node.yaml>", args[i])
		case "--harbor":
			i++
			if i >= len(args) {
				return options, fmt.Errorf("missing value for --harbor")
			}
			options.harbor = args[i]
		case "--interval":
			i++
			if i >= len(args) {
				return options, fmt.Errorf("missing value for --interval")
			}
			interval, err := time.ParseDuration(args[i])
			if err != nil {
				return options, fmt.Errorf("invalid --interval %q: %w", args[i], err)
			}
			options.interval = interval
		case "--cluster-dns":
			i++
			if i >= len(args) {
				return options, fmt.Errorf("missing value for --cluster-dns")
			}
			if net.ParseIP(args[i]) == nil {
				return options, fmt.Errorf("invalid --cluster-dns %q", args[i])
			}
			options.clusterDNS = args[i]
		case "--vxlan-id":
			i++
			if i >= len(args) {
				return options, fmt.Errorf("missing value for --vxlan-id")
			}
			id, err := strconv.Atoi(args[i])
			if err != nil || id <= 0 {
				return options, fmt.Errorf("invalid --vxlan-id %q", args[i])
			}
			options.vxlanID = id
		case "--vxlan-port":
			i++
			if i >= len(args) {
				return options, fmt.Errorf("missing value for --vxlan-port")
			}
			port, err := strconv.Atoi(args[i])
			if err != nil || port <= 0 || port > 65535 {
				return options, fmt.Errorf("invalid --vxlan-port %q", args[i])
			}
			options.vxlanPort = port
		case "--vxlan-name":
			i++
			if i >= len(args) {
				return options, fmt.Errorf("missing value for --vxlan-name")
			}
			if strings.TrimSpace(args[i]) == "" {
				return options, fmt.Errorf("invalid --vxlan-name %q", args[i])
			}
			options.vxlanName = args[i]
		case "--once":
			options.once = true
		case "--proxy-disabled":
			options.proxyDisabled = true
		default:
			if strings.HasPrefix(args[i], "-") {
				return options, fmt.Errorf("unknown sailer flag %q", args[i])
			}
			if options.nodeFile != "" {
				return options, fmt.Errorf("node yaml specified twice")
			}
			options.nodeFile = args[i]
		}
	}
	if options.nodeFile == "" {
		return options, fmt.Errorf("node yaml is required")
	}
	if options.harbor == "" {
		options.harbor = os.Getenv("MINIK8S_HARBOR")
	}
	if options.harbor == "" {
		return options, fmt.Errorf("--harbor is required")
	}
	return options, nil
}

func parseSailerRunOptions(args []string) (sailerOptions, error) {
	options := sailerOptions{
		interval:  5 * time.Second,
		vxlanID:   42,
		vxlanPort: 4789,
		vxlanName: "mk8s-vxlan",
	}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "run":
		case "--interval":
			i++
			if i >= len(args) {
				return options, fmt.Errorf("missing value for --interval")
			}
			interval, err := time.ParseDuration(args[i])
			if err != nil {
				return options, fmt.Errorf("invalid --interval %q: %w", args[i], err)
			}
			options.interval = interval
		case "--cluster-dns":
			i++
			if i >= len(args) {
				return options, fmt.Errorf("missing value for --cluster-dns")
			}
			if net.ParseIP(args[i]) == nil {
				return options, fmt.Errorf("invalid --cluster-dns %q", args[i])
			}
			options.clusterDNS = args[i]
		case "--vxlan-id":
			i++
			if i >= len(args) {
				return options, fmt.Errorf("missing value for --vxlan-id")
			}
			id, err := strconv.Atoi(args[i])
			if err != nil || id <= 0 {
				return options, fmt.Errorf("invalid --vxlan-id %q", args[i])
			}
			options.vxlanID = id
		case "--vxlan-port":
			i++
			if i >= len(args) {
				return options, fmt.Errorf("missing value for --vxlan-port")
			}
			port, err := strconv.Atoi(args[i])
			if err != nil || port <= 0 || port > 65535 {
				return options, fmt.Errorf("invalid --vxlan-port %q", args[i])
			}
			options.vxlanPort = port
		case "--vxlan-name":
			i++
			if i >= len(args) {
				return options, fmt.Errorf("missing value for --vxlan-name")
			}
			if strings.TrimSpace(args[i]) == "" {
				return options, fmt.Errorf("invalid --vxlan-name %q", args[i])
			}
			options.vxlanName = args[i]
		case "--once":
			options.once = true
		case "--proxy-disabled":
			options.proxyDisabled = true
		default:
			return options, fmt.Errorf("unknown sailer run flag %q", args[i])
		}
	}
	return options, nil
}

func writeLocalSailerConfig(path string, conf localSailerConfig) error {
	if conf.APIServer == "" {
		return fmt.Errorf("apiserver is required")
	}
	if conf.NodeName == "" {
		return fmt.Errorf("nodeName is required")
	}
	if conf.NodeToken == "" {
		return fmt.Errorf("nodeToken is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating sailer state dir: %w", err)
	}
	data, err := json.MarshalIndent(conf, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding sailer config: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("writing sailer config: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("replacing sailer config: %w", err)
	}
	return nil
}

func readLocalSailerConfig(path string) (localSailerConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return localSailerConfig{}, fmt.Errorf("sailer is not joined; run minik8s sailer join first")
		}
		return localSailerConfig{}, fmt.Errorf("reading sailer config: %w", err)
	}
	var conf localSailerConfig
	if err := json.Unmarshal(data, &conf); err != nil {
		return localSailerConfig{}, fmt.Errorf("parsing sailer config: %w", err)
	}
	if conf.APIServer == "" || conf.NodeName == "" || conf.NodeToken == "" {
		return localSailerConfig{}, fmt.Errorf("sailer config is incomplete")
	}
	return conf, nil
}

func writeLocalConfig(path string, conf localConfig) error {
	if strings.TrimSpace(conf.Harbor) == "" {
		return fmt.Errorf("harbor is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating minik8s config dir: %w", err)
	}
	data, err := json.MarshalIndent(conf, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding minik8s config: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("writing minik8s config: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("replacing minik8s config: %w", err)
	}
	return nil
}

func readLocalConfig(path string) (localConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return localConfig{}, fmt.Errorf("Harbor API is not configured; run minik8s bridge on the control-plane node or minik8s sailer join on this worker to create %s", path)
		}
		return localConfig{}, fmt.Errorf("reading minik8s config: %w", err)
	}
	var conf localConfig
	if err := json.Unmarshal(data, &conf); err != nil {
		return localConfig{}, fmt.Errorf("parsing minik8s config: %w", err)
	}
	if strings.TrimSpace(conf.Harbor) == "" {
		return localConfig{}, fmt.Errorf("minik8s config is incomplete: harbor is required")
	}
	return conf, nil
}

func bridgeHarborURL(listen string) (string, error) {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		if !strings.HasPrefix(listen, ":") {
			return "", fmt.Errorf("invalid bridge listen address %q: %w", listen, err)
		}
		host = ""
		port = strings.TrimPrefix(listen, ":")
	}
	if strings.TrimSpace(port) == "" {
		return "", fmt.Errorf("invalid bridge listen address %q: missing port", listen)
	}
	if host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
		host = "127.0.0.1"
	}
	return (&url.URL{Scheme: "http", Host: net.JoinHostPort(host, port)}).String(), nil
}

func (a *App) sailerServiceProxy(options sailerOptions) kubeproxy.Proxy {
	if options.proxyDisabled {
		return nil
	}
	if a.serviceProxy != nil {
		return a.serviceProxy
	}
	return kubeproxy.NewIPTablesProxy(nil)
}

func valueFlag(args []string, name string) (string, error) {
	for i, arg := range args {
		if arg == name && i+1 < len(args) {
			return args[i+1], nil
		}
	}
	return "", fmt.Errorf("missing required %s flag", name)
}

func namespaceFlag(args []string) string {
	for i, arg := range args {
		if (arg == "-n" || arg == "--namespace") && i+1 < len(args) {
			return args[i+1]
		}
	}
	return "default"
}

func formatUptime(status pod.PodStatus) string {
	if status.Phase != pod.PodRunning || status.StartTime == 0 {
		return "-"
	}
	return shortDuration(time.Since(time.Unix(status.StartTime, 0)))
}

func formatPodIP(ip string) string {
	if ip == "" {
		return "-"
	}
	return ip
}

func formatServicePorts(svc *service.Service) string {
	if len(svc.Spec.Ports) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(svc.Spec.Ports))
	for _, port := range svc.Spec.Ports {
		part := fmt.Sprintf("%d->%d/%s", port.Port, port.TargetPort, port.Protocol)
		if svc.Spec.Type == service.ServiceTypeNodePort && port.NodePort > 0 {
			part = fmt.Sprintf("%s:%d", part, port.NodePort)
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, ",")
}

func formatServiceEndpoints(endpoints []service.Endpoint) string {
	if len(endpoints) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(endpoints))
	for _, ep := range endpoints {
		parts = append(parts, fmt.Sprintf("%s:%d", ep.IP, ep.TargetPort))
	}
	return strings.Join(parts, ",")
}

func formatServiceSelector(selector pod.LabelSelector) string {
	if len(selector.MatchLabels) == 0 {
		return "-"
	}
	labels := make(map[string]string, len(selector.MatchLabels))
	for key, value := range selector.MatchLabels {
		labels[key] = value
	}
	return formatLabels(labels)
}

func formatNodeStatus(status node.NodePhase) string {
	icon := cliui.Icon(cliui.IconInfo, "[i]")
	if status == node.NodeReady {
		icon = cliui.Icon(cliui.IconSuccess, "[ok]")
	}
	if status == node.NodeUnknown {
		icon = cliui.Icon(cliui.IconWarning, "[!]")
	}
	return fmt.Sprintf("%s %s", icon, status)
}

func formatNodeAge(n node.Node) string {
	if n.Status.LastHeartbeat.IsZero() {
		return "-"
	}
	return shortDuration(time.Since(n.Status.LastHeartbeat))
}

func shortDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd%dh", int(d.Hours())/24, int(d.Hours())%24)
}

func formatLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return "-"
	}
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+labels[key])
	}
	return strings.Join(parts, ",")
}

func writef(out io.Writer, format string, args ...interface{}) error {
	_, err := fmt.Fprintf(out, format, args...)
	return err
}

func writes(out io.Writer, s string) error {
	_, err := io.WriteString(out, s)
	return err
}

// DefaultStatePath returns the default local state file path.
func DefaultStatePath() string {
	if dir := os.Getenv("MINIK8S_STATE_DIR"); dir != "" {
		return filepath.Join(dir, "pods.json")
	}
	return filepath.Join(".minik8s", "state", "pods.json")
}

func DefaultServiceStatePath() string {
	if dir := os.Getenv("MINIK8S_STATE_DIR"); dir != "" {
		return filepath.Join(dir, "services.json")
	}
	return filepath.Join(".minik8s", "state", "services.json")
}

func DefaultDNSStatePath() string {
	if dir := os.Getenv("MINIK8S_STATE_DIR"); dir != "" {
		return filepath.Join(dir, "dns.json")
	}
	return filepath.Join(".minik8s", "state", "dns.json")
}

func DefaultDNSDir() string {
	if dir := os.Getenv("MINIK8S_DNS_DIR"); dir != "" {
		return dir
	}
	return filepath.Join(".minik8s", "dns")
}

func DefaultDNSHostsPath() string {
	return filepath.Join(DefaultDNSDir(), "hosts")
}

func DefaultDNSRoutesPath() string {
	return filepath.Join(DefaultDNSDir(), "routes.json")
}

func DefaultCoreDNSCorefilePath() string {
	return filepath.Join(DefaultDNSDir(), "Corefile")
}

func DefaultNginxDNSConfigPath() string {
	return filepath.Join(DefaultDNSDir(), "nginx.conf")
}

func DefaultStaticPodDir() string {
	if dir := os.Getenv("MINIK8S_STATIC_POD_DIR"); dir != "" {
		return dir
	}
	return filepath.Join(".minik8s", "manifests")
}

func DefaultStorageManifestPath() string {
	return filepath.Join(DefaultStaticPodDir(), "storage-etcd.yaml")
}

func DefaultBridgeDepsManifestPath() string {
	return DefaultStorageManifestPath()
}

func DefaultDNSGatewayManifestPath() string {
	return filepath.Join(DefaultStaticPodDir(), "dns-gateway.yaml")
}

func DefaultServerlessNATSManifestPath() string {
	return filepath.Join(DefaultStaticPodDir(), "serverless-nats.yaml")
}

func DefaultMetricsServerManifestPath() string {
	return filepath.Join(DefaultStaticPodDir(), "metrics-server.yaml")
}

func DefaultBridgeDNSManifestPath() string {
	return DefaultDNSGatewayManifestPath()
}

func DefaultReplicaSetStatePath() string {
	if dir := os.Getenv("MINIK8S_STATE_DIR"); dir != "" {
		return filepath.Join(dir, "replicasets.json")
	}
	return filepath.Join(".minik8s", "state", "replicasets.json")
}

func DefaultHPAStatePath() string {
	if dir := os.Getenv("MINIK8S_STATE_DIR"); dir != "" {
		return filepath.Join(dir, "hpas.json")
	}
	return filepath.Join(".minik8s", "state", "hpas.json")
}

func DefaultNodeStatePath() string {
	if dir := os.Getenv("MINIK8S_STATE_DIR"); dir != "" {
		return filepath.Join(dir, "nodes.json")
	}
	return filepath.Join(".minik8s", "state", "nodes.json")
}

func DefaultBootstrapTokenPath() string {
	return tokens.DefaultBootstrapTokenPath()
}

func DefaultSailerConfigPath() string {
	if dir := os.Getenv("MINIK8S_STATE_DIR"); dir != "" {
		return filepath.Join(dir, "sailer.json")
	}
	return filepath.Join(".minik8s", "state", "sailer.json")
}

func DefaultLocalConfigPath() string {
	if path := os.Getenv("MINIK8S_CONFIG"); path != "" {
		return path
	}
	return filepath.Join(".minik8s", "config.json")
}

func DefaultFunctionStatePath() string {
	if dir := os.Getenv("MINIK8S_STATE_DIR"); dir != "" {
		return filepath.Join(dir, "functions.json")
	}
	return filepath.Join(".minik8s", "state", "functions.json")
}

func DefaultEventTriggerStatePath() string {
	if dir := os.Getenv("MINIK8S_STATE_DIR"); dir != "" {
		return filepath.Join(dir, "eventtriggers.json")
	}
	return filepath.Join(".minik8s", "state", "eventtriggers.json")
}

func DefaultWorkflowStatePath() string {
	if dir := os.Getenv("MINIK8S_STATE_DIR"); dir != "" {
		return filepath.Join(dir, "workflows.json")
	}
	return filepath.Join(".minik8s", "state", "workflows.json")
}

// DefaultCNIBinDir returns the default CNI plugin directory.
func DefaultCNIBinDir() string {
	if dir := os.Getenv("MINIK8S_CNI_BIN_DIR"); dir != "" {
		return dir
	}
	return filepath.Join(string(os.PathSeparator), "opt", "cni", "bin")
}

// DefaultCNIConfDir returns the default CNI config directory.
func DefaultCNIConfDir() string {
	if dir := os.Getenv("MINIK8S_CNI_CONF_DIR"); dir != "" {
		return dir
	}
	return filepath.Join(string(os.PathSeparator), "etc", "cni", "net.d")
}

type cniNetworkManager struct {
	runner *cni.Runner
}

func (m cniNetworkManager) Add(ctx context.Context, req nodeSailer.PodNetworkRequest) (nodeSailer.PodNetworkResult, error) {
	result, err := m.runner.Add(ctx, cni.PodNetwork{
		ContainerID: req.SandboxID,
		NetNS:       req.NetNSPath,
		IfName:      "eth0",
		PodName:     req.Pod.Name,
		Namespace:   req.Pod.Namespace,
	})
	if err != nil {
		return nodeSailer.PodNetworkResult{}, err
	}
	return nodeSailer.PodNetworkResult{PodIP: result.PodIP, CNIResult: result.Raw}, nil
}

func (m cniNetworkManager) Del(ctx context.Context, req nodeSailer.PodNetworkRequest) error {
	return m.runner.Del(ctx, cni.PodNetwork{
		ContainerID: req.SandboxID,
		NetNS:       req.NetNSPath,
		IfName:      "eth0",
		PodName:     req.Pod.Name,
		Namespace:   req.Pod.Namespace,
	})
}

func defaultNetworkManager() nodeSailer.PodNetworkManager {
	if os.Getenv("MINIK8S_CNI_DISABLED") == "1" {
		return nil
	}
	if _, err := os.Stat(DefaultCNIConfDir()); err != nil {
		return nil
	}
	return cniNetworkManager{runner: cni.NewRunner(cni.Config{
		BinDir:  DefaultCNIBinDir(),
		ConfDir: DefaultCNIConfDir(),
	})}
}
