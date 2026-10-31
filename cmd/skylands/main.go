// Command skylands is the one binary behind every Skylands role: "devworld
// up" for a one-machine world, "citadel up" and "daemon up" for the two
// roles, "world init" and "daemon enroll" for setting up a world, and the
// local CLI that talks to the daemon on this host.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/skylands-ai/skylands/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := cli.Main(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}
