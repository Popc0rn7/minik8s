package main

import (
	"context"
	"fmt"
	"os"

	"minik8s/internal/cli"
	"minik8s/internal/cliui"
	dockerruntime "minik8s/internal/runtime/docker"
	"minik8s/internal/store"
)

func main() {
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

	podStore, err := store.NewFilePodStore(cli.DefaultStatePath())
	if err != nil {
		fmt.Fprint(os.Stderr, cliui.ErrorLine("opening pod store: %v", err))
		os.Exit(1)
	}

	app := cli.New(cli.Config{
		Runtime: runtime,
		Store:   podStore,
	})
	if err := app.Run(context.Background(), os.Args[1:], os.Stdout); err != nil {
		fmt.Fprint(os.Stderr, cliui.ErrorLine("minik8s: %v", err))
		os.Exit(1)
	}
}
