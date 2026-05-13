package main

import (
	"context"
	"fmt"
	"os"

	"minik8s/internal/cli"
	"minik8s/internal/cliui"
	kubebridge "minik8s/internal/kubebridge"
	store "minik8s/internal/kubebridge/etcd"
	dockerruntime "minik8s/internal/runtime/docker"
)

func main() {
	podStore, serviceStore, closeStores, err := openStores()
	if err != nil {
		fmt.Fprint(os.Stderr, cliui.ErrorLine("opening stores: %v", err))
		os.Exit(1)
	}
	defer closeStores()
	nodeStore, err := store.NewFileNodeStore(cli.DefaultNodeStatePath())
	if err != nil {
		fmt.Fprint(os.Stderr, cliui.ErrorLine("opening node store: %v", err))
		os.Exit(1)
	}
	bridge := kubebridge.New(kubebridge.Config{
		PodStore:     podStore,
		ServiceStore: serviceStore,
		NodeStore:    nodeStore,
	})

	config := cli.Config{
		Store:        podStore,
		ServiceStore: serviceStore,
		NodeStore:    nodeStore,
		Bridge:       bridge,
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
	if args[0] == "kubesailer" {
		return true
	}
	if args[0] == "doctor" && len(args) > 1 && args[1] == "docker" {
		return true
	}
	return false
}

func openStores() (store.PodStore, store.ServiceStore, func(), error) {
	endpoints := store.ParseEndpoints(os.Getenv("MINIK8S_ETCD_ENDPOINTS"))
	if len(endpoints) > 0 {
		client, err := store.NewClient(endpoints)
		if err != nil {
			return nil, nil, func() {}, err
		}
		return store.NewEtcdPodStore(client), store.NewEtcdServiceStore(client), func() { _ = client.Close() }, nil
	}

	podStore, err := store.NewFilePodStore(cli.DefaultStatePath())
	if err != nil {
		return nil, nil, func() {}, fmt.Errorf("opening pod store: %w", err)
	}
	serviceStore, err := store.NewFileServiceStore(cli.DefaultServiceStatePath())
	if err != nil {
		return nil, nil, func() {}, fmt.Errorf("opening service store: %w", err)
	}
	return podStore, serviceStore, func() {}, nil
}
