package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	bridge "minik8s/internal/bridge"
	store "minik8s/internal/bridge/logbook"
	"minik8s/internal/cli"
	"minik8s/internal/cliui"
	"minik8s/internal/config"
	dockerruntime "minik8s/internal/runtime/docker"
)

func main() {
	if err := loadRuntimeConfig(".env"); err != nil {
		fmt.Fprint(os.Stderr, cliui.ErrorLine("loading runtime config: %v", err))
		os.Exit(1)
	}

	ctx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	dependencyCleanup, err := prepareBridgeDependencies(ctx, os.Args[1:], os.Stdout, cli.StartBridgeDependencies)
	if err != nil {
		fmt.Fprint(os.Stderr, cliui.ErrorLine("starting bridge dependencies: %v", err))
		os.Exit(1)
	}
	defer dependencyCleanup()

	podStore, serviceStore, dnsStore, replicaSetStore, hpaStore, metricsStore, nodeStore, functionStore, eventTriggerStore, workflowStore, closeStores, err := openStores()
	if err != nil {
		fmt.Fprint(os.Stderr, cliui.ErrorLine("opening stores: %v", err))
		os.Exit(1)
	}
	defer closeStores()
	controlBridge := bridge.New(newBridgeConfig(podStore, serviceStore, dnsStore, replicaSetStore, hpaStore, metricsStore, nodeStore, functionStore, eventTriggerStore, workflowStore))

	config := cli.Config{
		Store:             podStore,
		ServiceStore:      serviceStore,
		DNSStore:          dnsStore,
		ReplicaSetStore:   replicaSetStore,
		HPAStore:          hpaStore,
		MetricsStore:      metricsStore,
		NodeStore:         nodeStore,
		FunctionStore:     functionStore,
		EventTriggerStore: eventTriggerStore,
		WorkflowStore:     workflowStore,
		Bridge:            controlBridge,
	}
	if needsDockerRuntime(os.Args[1:]) {
		runtime, err := dockerruntime.NewDockerRuntime()
		if err != nil {
			fmt.Fprint(os.Stderr, cliui.ErrorLine("creating docker runtime: %v", err))
			os.Exit(1)
		}
		defer func() {
			if err := runtime.Close(); err != nil {
				fmt.Fprint(os.Stderr, cliui.ErrorLine("closing docker runtime: %v", err))
			}
		}()
		config.Runtime = runtime
	}

	app := cli.New(config)
	cmd := cli.NewRootCommand(app, os.Stdout)
	cmd.SetArgs(os.Args[1:])
	if err := cmd.ExecuteContext(ctx); err != nil {
		if ctx.Err() != nil {
			return
		}
		fmt.Fprint(os.Stderr, cliui.ErrorLine("minik8s: %v", err))
		os.Exit(1)
	}
}

func loadRuntimeConfig(path string) error {
	return config.LoadDotEnv(path)
}

type bridgeDependencyStarter func(context.Context, []string, io.Writer) (func(), error)

func prepareBridgeDependencies(ctx context.Context, args []string, out io.Writer, starter bridgeDependencyStarter) (func(), error) {
	mode, err := cli.BridgeDependencyMode(args)
	if err != nil {
		return func() {}, err
	}
	if mode != "internal" {
		return func() {}, nil
	}
	cleanup, err := starter(ctx, args, out)
	if err != nil {
		return func() {}, err
	}
	if os.Getenv("MINIK8S_LOGBOOK_ENDPOINTS") == "" {
		if err := os.Setenv("MINIK8S_LOGBOOK_ENDPOINTS", "http://127.0.0.1:2379"); err != nil {
			cleanup()
			return func() {}, err
		}
	}
	if os.Getenv("MINIK8S_NATS_URL") == "" {
		if err := os.Setenv("MINIK8S_NATS_URL", "nats://127.0.0.1:4222"); err != nil {
			cleanup()
			return func() {}, err
		}
	}
	return cleanup, nil
}

func needsDockerRuntime(args []string) bool {
	if len(args) == 0 {
		return false
	}
	if args[0] == "sailer" {
		return true
	}
	if args[0] == "doctor" && len(args) > 1 && args[1] == "docker" {
		return true
	}
	return false
}

func openStores() (store.PodStore, store.ServiceStore, store.DNSStore, store.ReplicaSetStore, store.HPAStore, store.MetricsStore, store.NodeStore, store.FunctionStore, store.EventTriggerStore, store.WorkflowStore, func(), error) {
	endpoints := store.ParseEndpoints(os.Getenv("MINIK8S_LOGBOOK_ENDPOINTS"))
	if len(endpoints) > 0 {
		client, err := store.NewClient(endpoints)
		if err != nil {
			return nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, func() {}, err
		}
		return store.NewEtcdPodStore(client), store.NewEtcdServiceStore(client), store.NewEtcdDNSStore(client), store.NewEtcdReplicaSetStore(client), store.NewEtcdHPAStore(client), store.NewInMemoryMetricsStore(), store.NewEtcdNodeStore(client), store.NewEtcdFunctionStore(client), store.NewEtcdEventTriggerStore(client), store.NewEtcdWorkflowStore(client), func() { _ = client.Close() }, nil
	}

	podStore, err := store.NewFilePodStore(cli.DefaultStatePath())
	if err != nil {
		return nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, func() {}, fmt.Errorf("opening pod store: %w", err)
	}
	serviceStore, err := store.NewFileServiceStore(cli.DefaultServiceStatePath())
	if err != nil {
		return nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, func() {}, fmt.Errorf("opening service store: %w", err)
	}
	dnsStore, err := store.NewFileDNSStore(cli.DefaultDNSStatePath())
	if err != nil {
		return nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, func() {}, fmt.Errorf("opening dns store: %w", err)
	}
	replicaSetStore, err := store.NewFileReplicaSetStore(cli.DefaultReplicaSetStatePath())
	if err != nil {
		return nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, func() {}, fmt.Errorf("opening replicaset store: %w", err)
	}
	hpaStore, err := store.NewFileHPAStore(cli.DefaultHPAStatePath())
	if err != nil {
		return nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, func() {}, fmt.Errorf("opening hpa store: %w", err)
	}
	nodeStore, err := store.NewFileNodeStore(cli.DefaultNodeStatePath())
	if err != nil {
		return nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, func() {}, fmt.Errorf("opening node store: %w", err)
	}
	functionStore, err := store.NewFileFunctionStore(cli.DefaultFunctionStatePath())
	if err != nil {
		return nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, func() {}, fmt.Errorf("opening function store: %w", err)
	}
	eventTriggerStore, err := store.NewFileEventTriggerStore(cli.DefaultEventTriggerStatePath())
	if err != nil {
		return nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, func() {}, fmt.Errorf("opening eventtrigger store: %w", err)
	}
	workflowStore, err := store.NewFileWorkflowStore(cli.DefaultWorkflowStatePath())
	if err != nil {
		return nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, func() {}, fmt.Errorf("opening workflow store: %w", err)
	}
	return podStore, serviceStore, dnsStore, replicaSetStore, hpaStore, store.NewInMemoryMetricsStore(), nodeStore, functionStore, eventTriggerStore, workflowStore, func() {}, nil
}

func newBridgeConfig(podStore store.PodStore, serviceStore store.ServiceStore, dnsStore store.DNSStore, replicaSetStore store.ReplicaSetStore, hpaStore store.HPAStore, metricsStore store.MetricsStore, nodeStore store.NodeStore, functionStore store.FunctionStore, eventTriggerStore store.EventTriggerStore, workflowStore store.WorkflowStore) bridge.Config {
	return bridge.Config{
		PodStore:          podStore,
		ServiceStore:      serviceStore,
		DNSStore:          dnsStore,
		ReplicaSetStore:   replicaSetStore,
		HPAStore:          hpaStore,
		MetricsStore:      metricsStore,
		NodeStore:         nodeStore,
		FunctionStore:     functionStore,
		EventTriggerStore: eventTriggerStore,
		WorkflowStore:     workflowStore,
	}
}
