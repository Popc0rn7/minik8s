package main

import (
	"context"
	"fmt"
	"os"

	"minik8s/internal/cli"
	dockerruntime "minik8s/internal/runtime/docker"
	"minik8s/internal/store"
)

func main() {
	runtime, err := dockerruntime.NewDockerRuntime()
	if err != nil {
		fmt.Fprintf(os.Stderr, "creating docker runtime: %v\n", err)
		os.Exit(1)
	}
	defer func() {
		if err := runtime.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "closing docker runtime: %v\n", err)
		}
	}()

	podStore, err := store.NewFilePodStore(cli.DefaultStatePath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "opening pod store: %v\n", err)
		os.Exit(1)
	}

	app := cli.New(cli.Config{
		Runtime: runtime,
		Store:   podStore,
	})
	if err := app.Run(context.Background(), os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "minik8s: %v\n", err)
		os.Exit(1)
	}
}
