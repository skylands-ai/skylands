// Command skylands is the one binary behind every Skylands role: "devworld
// up" for a one-machine world, "citadel up" and "daemon up" for the two
// roles, "world init" and "daemon enroll" for setting up a world, and the
// local CLI that talks to the daemon on this host.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/skylands-ai/skylands/internal/cli"
	"github.com/skylands-ai/skylands/internal/watchdog"
)

func main() {
	// A daemon runs this binary as its watchdog, too.
	watchdog.MainHook()
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	// A closed terminal's hangup stops the daemon the way Ctrl-C does, so
	// it ends its agents rather than dying with them running.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	code := cli.Main(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}
