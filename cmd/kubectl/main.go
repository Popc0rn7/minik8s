package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"minik8s/internal/cli"
	"minik8s/internal/cliui"
	"minik8s/internal/config"
)

func main() {
	if err := config.LoadDotEnv(".env"); err != nil {
		fmt.Fprint(os.Stderr, cliui.ErrorLine("loading runtime config: %v", err))
		os.Exit(1)
	}

	ctx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	app := cli.New(cli.Config{})
	cmd := cli.NewKubectlCommand(app, os.Stdout)
	cmd.SetArgs(os.Args[1:])
	if err := cmd.ExecuteContext(ctx); err != nil {
		if ctx.Err() != nil {
			return
		}
		fmt.Fprint(os.Stderr, cliui.ErrorLine("kubectl: %v", err))
		os.Exit(1)
	}
}
