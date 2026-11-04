package approval

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	v1 "github.com/skylands-ai/skylands/api/v1"
)

// StreamName is the JetStream stream, in the world's domain, that holds
// the world's approver registry. Every record has a subject of its own,
// under v1.ApproversSubjectPrefix, and is written once: the stream takes no
// second message on a subject, and refuses deletes and purges, so nothing
// written to it can be taken back. A revocation, once stored, stays.
const StreamName = "skylands-approvers"

// StreamConfig is the registry stream's configuration.
func StreamConfig() jetstream.StreamConfig {
	return jetstream.StreamConfig{
		Name:                 StreamName,
		Description:          "The Skylands world's approvers: owner entries, vouchers, vouched entries, and revocations",
		Subjects:             []string{v1.ApproversSubjectPrefix + ".>"},
		Storage:              jetstream.FileStorage,
		Retention:            jetstream.LimitsPolicy,
		MaxMsgsPerSubject:    1,
		Discard:              jetstream.DiscardNew,
		DiscardNewPerSubject: true,
		DenyDelete:           true,
		DenyPurge:            true,
	}
}

// ErrTaken is what Append returns when its subject already holds a record.
var ErrTaken = errors.New("that registry record is already stored")

// Append stores a signed record on its subject, once. It returns the
// record as stored, with the sequence and time the server gave it.
func Append(ctx context.Context, js jetstream.JetStream, subject string, s v1.Signed) (Record, error) {
	data, err := json.Marshal(s)
	if err != nil {
		return Record{}, err
	}
	ack, err := js.Publish(ctx, subject, data, jetstream.WithExpectStream(StreamName), jetstream.WithExpectLastSequencePerSubject(0))
	if err != nil {
		var api *jetstream.APIError
		if errors.As(err, &api) && (api.ErrorCode == jetstream.JSErrCodeStreamWrongLastSequence || api.ErrorCode == jetstream.JSErrCodeStreamWrongLastSequenceConstant) {
			return Record{}, ErrTaken
		}
		return Record{}, err
	}
	stream, err := js.Stream(ctx, StreamName)
	if err != nil {
		return Record{}, err
	}
	msg, err := stream.GetMsg(ctx, ack.Sequence)
	if err != nil {
		return Record{}, err
	}
	return Record{Signed: s, Revision: msg.Sequence, Stored: msg.Time}, nil
}

// Follow reads the registry stream from its first record and keeps
// reading. Once it has read what the stream held when it started, and
// after every record after that, it calls changed with every record so
// far. It waits for the stream to exist, and returns when ctx ends.
func Follow(ctx context.Context, js jetstream.JetStream, changed func([]Record)) {
	var stream jetstream.Stream
	for {
		var err error
		if stream, err = js.Stream(ctx, StreamName); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
	info, err := stream.Info(ctx)
	if err != nil {
		return
	}
	caughtUp := info.State.LastSeq
	cons, err := stream.OrderedConsumer(ctx, jetstream.OrderedConsumerConfig{DeliverPolicy: jetstream.DeliverAllPolicy})
	if err != nil {
		return
	}
	it, err := cons.Messages()
	if err != nil {
		return
	}
	go func() {
		<-ctx.Done()
		it.Stop()
	}()
	var records []Record
	if caughtUp == 0 {
		changed(nil)
	}
	for {
		msg, err := it.Next()
		if err != nil {
			return
		}
		meta, err := msg.Metadata()
		if err != nil {
			continue
		}
		var s v1.Signed
		if json.Unmarshal(msg.Data(), &s) == nil {
			records = append(records, Record{Signed: s, Revision: meta.Sequence.Stream, Stored: meta.Timestamp})
		}
		if meta.Sequence.Stream >= caughtUp {
			changed(append([]Record(nil), records...))
		}
	}
}
