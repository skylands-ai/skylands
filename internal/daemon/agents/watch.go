package agents

import "context"

// Watch is told about every agent process group as it starts and once it
// has ended, so something outside the daemon, the watchdog, can end the
// groups still running if the daemon dies without ending them.
type Watch interface {
	Started(pgid int)
	Ended(pgid int)
}

type watchKey struct{}

// WithWatch returns a context under which every agent launched registers
// its process group with w. The daemon puts its watchdog on the context
// everything it runs derives from.
func WithWatch(ctx context.Context, w Watch) context.Context {
	return context.WithValue(ctx, watchKey{}, w)
}

func watchFrom(ctx context.Context) Watch {
	w, _ := ctx.Value(watchKey{}).(Watch)
	return w
}
