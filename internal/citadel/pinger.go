package citadel

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/micro"

	"github.com/skylands-ai/skylands/internal/citadel/projections"
	"github.com/skylands-ai/skylands/internal/daemon"
)

// pingSubject is the NATS micro discovery subject every daemon answers.
const pingSubject = micro.APIPrefix + ".PING." + daemon.ServiceName

var errNATSDown = errors.New("not connected to NATS")

// natsPinger discovers daemons through NATS micro: one request, and every
// reachable daemon replies within the window.
type natsPinger struct {
	nc     *nats.Conn
	window time.Duration
}

func (p natsPinger) Ping(ctx context.Context) ([]projections.Reply, error) {
	if !p.nc.IsConnected() {
		return nil, errNATSDown
	}
	inbox := nats.NewInbox()
	sub, err := p.nc.SubscribeSync(inbox)
	if err != nil {
		return nil, err
	}
	defer func() { _ = sub.Unsubscribe() }()
	if err := p.nc.PublishRequest(pingSubject, inbox, nil); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, p.window)
	defer cancel()
	var replies []projections.Reply
	for {
		msg, err := sub.NextMsgWithContext(ctx)
		if err != nil {
			// The window closing is the normal end of a round.
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, nats.ErrTimeout) {
				return replies, nil
			}
			return replies, err
		}
		var ping micro.Ping
		if err := json.Unmarshal(msg.Data, &ping); err != nil {
			slog.Debug("citadel: bad ping reply", "err", err)
			continue
		}
		replies = append(replies, projections.Reply{ID: ping.ID, Version: ping.Version, Metadata: ping.Metadata})
	}
}
