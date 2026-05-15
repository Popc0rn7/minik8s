package main

import (
	"context"
	"fmt"
	"os"

	bridge "minik8s/internal/bridge"
	store "minik8s/internal/bridge/logbook"
	"minik8s/internal/cli"
	"minik8s/internal/cliui"
	"minik8s/internal/kubeproxy"
	dockerruntime "minik8s/internal/runtime/docker"
)

func main() {
	podStore, serviceStore, nodeStore, closeStores, err := openStores()
	if err != nil {
		fmt.Fprint(os.Stderr, cliui.ErrorLine("opening stores: %v", err))
		os.Exit(1)
	}
	defer closeStores()
	controlBridge := bridge.New(newBridgeConfig(podStore, serviceStore, nodeStore))

	config := cli.Config{
		Store:        podStore,
		ServiceStore: serviceStore,
		NodeStore:    nodeStore,
		Bridge:       controlBridge,
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
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		fmt.Fprint(os.Stderr, cliui.ErrorLine("minik8s: %v", err))
		os.Exit(1)
	}
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

func openStores() (store.PodStore, store.ServiceStore, store.NodeStore, func(), error) {
	endpoints := store.ParseEndpoints(os.Getenv("MINIK8S_LOGBOOK_ENDPOINTS"))
	if len(endpoints) > 0 {
		client, err := store.NewClient(endpoints)
		if err != nil {
			return nil, nil, nil, func() {}, err
		}
		return store.NewEtcdPodStore(client), store.NewEtcdServiceStore(client), store.NewEtcdNodeStore(client), func() { _ = client.Close() }, nil
	}

	podStore, err := store.NewFilePodStore(cli.DefaultStatePath())
	if err != nil {
		return nil, nil, nil, func() {}, fmt.Errorf("opening pod store: %w", err)
	}
	serviceStore, err := store.NewFileServiceStore(cli.DefaultServiceStatePath())
	if err != nil {
		return nil, nil, nil, func() {}, fmt.Errorf("opening service store: %w", err)
	}
	nodeStore, err := store.NewFileNodeStore(cli.DefaultNodeStatePath())
	if err != nil {
		return nil, nil, nil, func() {}, fmt.Errorf("opening node store: %w", err)
	}
	return podStore, serviceStore, nodeStore, func() {}, nil
}

func newBridgeConfig(podStore store.PodStore, serviceStore store.ServiceStore, nodeStore store.NodeStore) bridge.Config {
	config := bridge.Config{
		PodStore:     podStore,
		ServiceStore: serviceStore,
		NodeStore:    nodeStore,
	}
	if os.Getenv("MINIK8S_SERVICE_PROXY_DISABLED") != "1" {
		config.ServiceProxy = kubeproxy.NewIPTablesProxy(nil)
	}
	return config
}
