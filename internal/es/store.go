// Package es is the event-sourcing kit over JetStream shared by the citadel
// and the daemon: streams and mirrors created idempotently, appends that
// return the stream sequence, and ordered reads for aggregates and
// projections.
package es

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	v1 "github.com/skylands-ai/skylands/api/v1"
)

// Store is a JetStream connection in one domain: the host's on a daemon,
// the world's on a citadel.
type Store struct {
	js jetstream.JetStream
}

// New wraps nc's JetStream.
func New(nc *nats.Conn) (*Store, error) {
	js, err := jetstream.New(nc)
	if err != nil {
		return nil, fmt.Errorf("jetstream: %w", err)
	}
	return &Store{js: js}, nil
}

// EnsureStream creates the stream that captures subjects, or leaves an
// existing one as it is.
func (s *Store) EnsureStream(ctx context.Context, name string, subjects []string) error {
	_, err := s.js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{Name: name, Subjects: subjects, Storage: jetstream.FileStorage})
	if err != nil {
		return fmt.Errorf("ensure stream %s: %w", name, err)
	}
	return nil
}

// EnsureMirror creates a mirror of sourceName from the JetStream domain
// sourceDomain. A mirror keeps the origin's sequence numbers, so an event
// has the same sequence in both domains.
func (s *Store) EnsureMirror(ctx context.Context, name, sourceName, sourceDomain string) error {
	_, err := s.js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:    name,
		Storage: jetstream.FileStorage,
		Mirror:  &jetstream.StreamSource{Name: sourceName, Domain: sourceDomain},
	})
	if err != nil {
		return fmt.Errorf("ensure mirror %s of %s in %s: %w", name, sourceName, sourceDomain, err)
	}
	return nil
}

// Append stores ev on subject and returns its stream sequence.
func (s *Store) Append(ctx context.Context, subject string, ev v1.Event) (uint64, error) {
	data, err := json.Marshal(ev)
	if err != nil {
		return 0, err
	}
	ack, err := s.js.Publish(ctx, subject, data, jetstream.WithMsgID(ev.ID))
	if err != nil {
		return 0, fmt.Errorf("append %s to %s: %w", ev.Type, subject, err)
	}
	return ack.Sequence, nil
}

// Handler receives one event with its stream sequence.
type Handler func(seq uint64, ev v1.Event) error

// Replay delivers every stored event matching filters, in stream order,
// then returns. Aggregates use it to rebuild their state at startup.
func (s *Store) Replay(ctx context.Context, stream string, filters []string, fn Handler) error {
	st, err := s.js.Stream(ctx, stream)
	if err != nil {
		return fmt.Errorf("stream %s: %w", stream, err)
	}
	info, err := st.Info(ctx)
	if err != nil {
		return err
	}
	if info.State.Msgs == 0 {
		return nil
	}
	cons, err := st.OrderedConsumer(ctx, jetstream.OrderedConsumerConfig{FilterSubjects: filters, DeliverPolicy: jetstream.DeliverAllPolicy})
	if err != nil {
		return err
	}
	for {
		msg, err := cons.Next(jetstream.FetchMaxWait(time.Second))
		if err != nil {
			// A stream with messages but none matching the filters ends here.
			if errors.Is(err, nats.ErrTimeout) || errors.Is(err, jetstream.ErrNoMessages) {
				return nil
			}
			return err
		}
		seq, ev, pending, err := decode(msg)
		if err != nil {
			return err
		}
		if err := fn(seq, ev); err != nil {
			return err
		}
		if pending == 0 {
			return nil
		}
	}
}

// Follow delivers every stored event matching filters, then each new one as
// it arrives, until ctx ends. Projections use it. Delivery is in order and
// at least once, so handlers must tolerate a repeat after a reconnect.
func (s *Store) Follow(ctx context.Context, stream string, filters []string, fn func(seq uint64, ev v1.Event)) error {
	st, err := s.js.Stream(ctx, stream)
	if err != nil {
		return fmt.Errorf("stream %s: %w", stream, err)
	}
	cons, err := st.OrderedConsumer(ctx, jetstream.OrderedConsumerConfig{FilterSubjects: filters, DeliverPolicy: jetstream.DeliverAllPolicy})
	if err != nil {
		return err
	}
	cc, err := cons.Consume(func(msg jetstream.Msg) {
		seq, ev, _, err := decode(msg)
		if err != nil {
			return
		}
		fn(seq, ev)
	})
	if err != nil {
		return err
	}
	<-ctx.Done()
	cc.Stop()
	return nil
}

func decode(msg jetstream.Msg) (seq uint64, ev v1.Event, pending uint64, err error) {
	meta, err := msg.Metadata()
	if err != nil {
		return 0, ev, 0, err
	}
	if err := json.Unmarshal(msg.Data(), &ev); err != nil {
		return 0, ev, 0, fmt.Errorf("decode event at %d on %s: %w", meta.Sequence.Stream, msg.Subject(), err)
	}
	return meta.Sequence.Stream, ev, meta.NumPending, nil
}
