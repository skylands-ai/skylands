package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/skylands-ai/skylands/internal/service"
)

// serviceBinary is the path the running service was started from, which
// install wrote into the service's definition. Tests replace it.
var serviceBinary = func() string {
	if filepath.IsAbs(os.Args[0]) {
		return os.Args[0]
	}
	exe, _ := os.Executable()
	return exe
}

// How often a service checks its binary, and how long the binary must be
// missing before the service takes it as uninstalled. Tests shorten them.
var (
	binaryCheck = 30 * time.Second
	binaryGrace = 5 * time.Minute
)

// runService runs run, a role's "up", which is to stop once stop is closed
// and no agent is working on a turn.
//
// Run by a service that install wrote, it also watches the binary the
// service was started from, so the service follows the package manager
// with no one restarting or removing it by hand. When the binary is
// replaced, as an upgrade replaces it, the daemon stops once idle and the
// command fails, so the service manager starts it again on the new binary.
// When the binary is gone for good, as after an uninstall, the daemon stops
// once idle and the service's definition is removed, so it does not start
// again. Run any other way, it only runs run.
func runService(ctx context.Context, run func(ctx context.Context, stop <-chan struct{}) error) error {
	name := os.Getenv(service.EnvName)
	if name == "" {
		return run(ctx, nil)
	}
	// The agents the daemon launches are not the service.
	_ = os.Unsetenv(service.EnvName)
	path, check, grace := serviceBinary(), binaryCheck, binaryGrace

	watchCtx, stopWatching := context.WithCancel(ctx)
	watched := make(chan struct{})
	defer func() {
		stopWatching()
		<-watched
	}()
	stop := make(chan struct{})
	changed := make(chan service.Change, 1)
	go func() {
		defer close(watched)
		c, err := service.WatchBinary(watchCtx, path, check, grace)
		if err != nil {
			if watchCtx.Err() == nil {
				slog.Warn("service: not watching its binary, so it will not follow an upgrade or uninstall by itself", "service", name, "binary", path, "err", err)
			}
			return
		}
		slog.Info("service: its binary was "+c.String()+"; stopping once no agent is working on a turn", "service", name, "binary", path)
		changed <- c
		close(stop)
	}()

	err := run(ctx, stop)
	var c service.Change
	select {
	case c = <-changed:
	default:
		return err
	}
	if err != nil {
		return err
	}
	// Installed again while the daemon waited to be idle: follow the new
	// binary rather than remove the service.
	if _, statErr := os.Stat(path); c == service.Removed && statErr == nil {
		c = service.Replaced
	}
	if c == service.Replaced {
		// A failure is what makes the service manager start it again.
		return fmt.Errorf("restarting on the new binary at %s", path)
	}
	m, err := newServiceManager()
	if err != nil {
		return err
	}
	// ctx may be cancelled by now, and removing must still finish.
	switch err := m.Remove(context.WithoutCancel(ctx), name); {
	case errors.Is(err, service.ErrNotInstalled):
		slog.Info("service: its binary is gone, and it has no definition left to remove", "service", name, "definition", m.Path(name))
		return nil
	case err != nil:
		return fmt.Errorf("%s is gone, but removing the service %s failed: %w", path, name, err)
	}
	slog.Info("service: removed, now that its binary is gone; its data directory is untouched", "service", name, "definition", m.Path(name))
	return nil
}
