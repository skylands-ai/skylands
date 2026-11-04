package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"

	v1 "github.com/skylands-ai/skylands/api/v1"
	"github.com/skylands-ai/skylands/internal/approval"
)

// ErrNoWorld is why a daemon outside devworld won't start without the
// world's owner key.
var ErrNoWorld = errors.New("a daemon needs the world it joins: on the citadel, select the citadel and copy the command under \"Add a host\", which names the world's owner key with --world")

// worldPin is the owner key this island belongs to, kept in the data
// directory once known.
type worldPin struct {
	path string
	mu   sync.Mutex
	key  string
}

// pinWorld reads the owner key this island has pinned and reconciles it
// with the one it was started with. A daemon started with no key may run
// only when it is allowed to pin the first world it finds, as devworld's
// own daemon is.
func pinWorld(dataDir, host, world string, trustFirst bool) (*worldPin, error) {
	p := &worldPin{path: filepath.Join(dataDir, "world", host+".owner")}
	if data, err := os.ReadFile(p.path); err == nil {
		p.key = strings.TrimSpace(string(data))
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read the world this island belongs to: %w", err)
	}
	switch {
	case world != "":
		if !nkeys.IsValidPublicOperatorKey(world) {
			return nil, fmt.Errorf("--world %q is not a world's owner key, which starts with O; copy it from \"Add a host\" on the citadel", world)
		}
		if p.key != "" && p.key != world {
			return nil, fmt.Errorf("island %s already belongs to the world whose owner key is %s, and --world names %s; to move it to another world, remove %s", host, p.key, world, p.path)
		}
		if p.key == "" {
			if err := p.set(world); err != nil {
				return nil, err
			}
		}
	case p.key == "" && !trustFirst:
		return nil, ErrNoWorld
	}
	return p, nil
}

func (p *worldPin) get() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.key
}

func (p *worldPin) set(key string) error {
	if err := os.MkdirAll(filepath.Dir(p.path), 0o700); err != nil {
		return fmt.Errorf("pin the world's owner key: %w", err)
	}
	if err := os.WriteFile(p.path, []byte(key+"\n"), 0o600); err != nil {
		return fmt.Errorf("pin the world's owner key: %w", err)
	}
	p.mu.Lock()
	p.key = key
	p.mu.Unlock()
	return nil
}

// worldCheckEvery is how often a daemon asks for the world's record until
// it has one that matches.
var worldCheckEvery = 5 * time.Second

// checkWorld asks a citadel for the world's record until it gets one,
// then pins its owner key if none is pinned yet, or returns an error if
// it names another owner. With no citadel answering, or no world set up
// yet, it keeps asking.
func checkWorld(ctx context.Context, nc *nats.Conn, pin *worldPin) error {
	waiting := false
	for {
		rctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		msg, err := nc.RequestWithContext(rctx, v1.WorldRecordSubject, nil)
		cancel()
		if ctx.Err() != nil {
			return nil
		}
		if err == nil && len(msg.Data) > 0 {
			var s v1.Signed
			rec, oerr := v1.WorldRecord{}, json.Unmarshal(msg.Data, &s)
			if oerr == nil {
				rec, oerr = approval.OpenWorld(s)
			}
			switch {
			case oerr != nil:
				slog.Warn("daemon: the world record a citadel sent doesn't verify", "err", oerr)
			case pin.get() == "":
				if err := pin.set(rec.Owner); err != nil {
					return err
				}
				slog.Info("daemon: joined world", "world", rec.Name, "owner", rec.Owner)
				return nil
			case pin.get() != rec.Owner:
				return fmt.Errorf("the world's record names owner key %s, but this island belongs to the world whose owner key is %s; it won't run in another world", rec.Owner, pin.get())
			default:
				slog.Info("daemon: in world", "world", rec.Name, "owner", rec.Owner)
				return nil
			}
		} else if !waiting {
			waiting = true
			slog.Info("daemon: waiting for the world's record, which a citadel answers with once the world is set up")
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(worldCheckEvery):
		}
	}
}
