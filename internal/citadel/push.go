package citadel

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nuid"

	v1 "github.com/skylands-ai/skylands/api/v1"
	"github.com/skylands-ai/skylands/internal/approval"
	"github.com/skylands-ai/skylands/internal/citadel/projections"
	"github.com/skylands-ai/skylands/internal/citadel/web"
	"github.com/skylands-ai/skylands/internal/webpush"
)

// Web Push to approvers' browsers. The world's VAPID key and every
// subscription are kept in the pushBucket, so every citadel sends with the
// same key to the same browsers. Each island's mirror has one durable
// consumer, pushConsumer, that every citadel sending pushes pulls from, so
// one citadel handles each event and the consumer's acks track progress.
// A page in sight is kept in the presenceBucket, refreshed by the citadel
// holding its WebSocket, so whichever citadel sends a push knows to hold
// it.
const (
	pushBucket     = "skylands-push"
	presenceBucket = "skylands-presence"
	vapidKey       = "vapid"
	pushConsumer   = "push"
	// pushLife is how long a request waits on its host before it expires,
	// by the daemon's default. A push expires with it, and an event older
	// than that, such as one a stopped citadel left, isn't pushed.
	pushLife = 30 * time.Minute
	// pushHold is how long a push waits while its approver has the world's
	// page in sight, in case they answer there.
	pushHold = 30 * time.Second
	// A page counts as in sight for presenceTTL after its citadel last said
	// so, which it does every presenceEvery while it is.
	presenceTTL   = time.Minute
	presenceEvery = 20 * time.Second
)

// pusher sends Web Push notifications for what waits on approvers.
type pusher struct {
	c      *citadel
	client *http.Client
	hold   time.Duration
	// pruning keeps one prune at a time, so each deletion is logged once.
	pruning sync.Mutex

	mu       sync.Mutex
	starting bool
	sender   *webpush.Sender
	subs     jetstream.KeyValue
	presence jetstream.KeyValue
	js       jetstream.JetStream
	// hosts are the islands whose mirrors the citadel follows, and
	// consuming the ones whose push consumer it pulls from.
	hosts     map[string]bool
	consuming map[string]bool
}

// pushOn says whether a citadel sends pushes, and why not when it doesn't.
func pushOn(cfg Config, publicURL string) (bool, string) {
	if cfg.NoPush {
		return false, "--no-push"
	}
	if !strings.HasPrefix(publicURL, "https://") {
		return false, "--url isn't HTTPS"
	}
	return true, ""
}

func newPusher(c *citadel) *pusher {
	hold := c.cfg.PushHold
	if hold <= 0 {
		hold = pushHold
	}
	return &pusher{c: c, client: c.cfg.PushClient, hold: hold, hosts: map[string]bool{}, consuming: map[string]bool{}}
}

// start makes the push buckets and the world's VAPID key, if they don't
// exist yet, and starts pulling from the push consumers of the islands
// already followed. It runs on every connect, does its work once, and
// tries again until it has.
func (p *pusher) start(ctx context.Context, js jetstream.JetStream) {
	p.mu.Lock()
	if p.sender != nil || p.starting {
		p.mu.Unlock()
		return
	}
	p.starting = true
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		p.starting = false
		p.mu.Unlock()
	}()
	for !p.startOnce(ctx, js) {
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}

func (p *pusher) startOnce(ctx context.Context, js jetstream.JetStream) bool {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	subs, err := js.CreateOrUpdateKeyValue(ctx, jetstream.KeyValueConfig{Bucket: pushBucket, History: 1, Description: "The Skylands world's push key and approvers' push subscriptions"})
	if err != nil {
		slog.Warn("citadel: create the push bucket", "err", err)
		return false
	}
	presence, err := js.CreateOrUpdateKeyValue(ctx, jetstream.KeyValueConfig{Bucket: presenceBucket, History: 1, TTL: presenceTTL, Storage: jetstream.MemoryStorage, Description: "Skylands approvers with the world's page in sight"})
	if err != nil {
		slog.Warn("citadel: create the presence bucket", "err", err)
		return false
	}
	keys, err := loadVAPID(ctx, subs)
	if err != nil {
		slog.Warn("citadel: the world's push key", "err", err)
		return false
	}
	p.mu.Lock()
	p.sender = &webpush.Sender{Keys: keys, Subject: p.c.publicURL(), Client: p.client}
	p.subs, p.presence, p.js = subs, presence, js
	var hosts []string
	for h := range p.hosts {
		hosts = append(hosts, h)
	}
	p.mu.Unlock()
	slog.Info("citadel: push notifications on", "url", p.c.publicURL())
	for _, h := range hosts {
		p.follow(h)
	}
	p.c.web.Broadcast(web.Message{Type: "world", World: p.c.worldInfo()})
	return true
}

// loadVAPID reads the world's VAPID key, or makes it. Two citadels making
// it at once both write create-only, so the first one's key stands.
func loadVAPID(ctx context.Context, kv jetstream.KeyValue) (*webpush.Keys, error) {
	for range 3 {
		if e, err := kv.Get(ctx, vapidKey); err == nil {
			return webpush.ParseKeys(string(e.Value()))
		} else if !errors.Is(err, jetstream.ErrKeyNotFound) {
			return nil, err
		}
		keys, err := webpush.NewKeys()
		if err != nil {
			return nil, err
		}
		if _, err := kv.Create(ctx, vapidKey, []byte(keys.Private())); err == nil {
			slog.Info("citadel: made the world's push key")
			return keys, nil
		} else if !errors.Is(err, jetstream.ErrKeyExists) {
			return nil, err
		}
	}
	return nil, errors.New("the push key was neither readable nor writable")
}

// publicKey is the world's VAPID public key, once the citadel has it.
func (p *pusher) publicKey() string {
	if p == nil {
		return ""
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.sender == nil {
		return ""
	}
	return p.sender.Keys.Public()
}

// bucket is the push bucket, once the citadel has it.
func (p *pusher) bucket() jetstream.KeyValue {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.subs
}

// follow pulls from host's push consumer, once the push key is known and
// the host's mirror exists.
func (p *pusher) follow(host string) {
	p.mu.Lock()
	p.hosts[host] = true
	js := p.js
	if js == nil || p.consuming[host] {
		p.mu.Unlock()
		return
	}
	p.consuming[host] = true
	p.mu.Unlock()
	go func() {
		ctx := p.c.ctx
		for ctx.Err() == nil {
			err := p.consume(ctx, js, host)
			if ctx.Err() != nil {
				return
			}
			slog.Warn("citadel: push consumer", "host", host, "err", err)
			select {
			case <-ctx.Done():
			case <-time.After(2 * time.Second):
			}
		}
	}()
}

// pushConsumerConfig is host's push consumer. It starts at new events, so
// a citadel starting for the first time doesn't push what came before,
// and the server removes it after an hour that no citadel pulls from it,
// so one starting again after that doesn't either.
func pushConsumerConfig(host string) jetstream.ConsumerConfig {
	return jetstream.ConsumerConfig{
		Durable:     pushConsumer,
		Description: "Skylands push notifications for what waits on approvers",
		FilterSubjects: []string{
			v1.SessionEventSubject(host, "*", v1.EventPermissionRequested),
			v1.SessionEventSubject(host, "*", v1.EventQuestionAsked),
		},
		DeliverPolicy:     jetstream.DeliverNewPolicy,
		AckPolicy:         jetstream.AckExplicitPolicy,
		AckWait:           30 * time.Second,
		MaxDeliver:        5,
		MaxAckPending:     256,
		InactiveThreshold: time.Hour,
	}
}

// consume pulls from host's push consumer until ctx ends or the consumer
// goes away.
func (p *pusher) consume(ctx context.Context, js jetstream.JetStream, host string) error {
	cons, err := js.CreateOrUpdateConsumer(ctx, v1.IslandStreamName(host), pushConsumerConfig(host))
	if err != nil {
		return err
	}
	gone := make(chan error, 1)
	cc, err := cons.Consume(func(msg jetstream.Msg) {
		go p.handle(ctx, host, msg)
	}, jetstream.ConsumeErrHandler(func(_ jetstream.ConsumeContext, err error) {
		if errors.Is(err, jetstream.ErrConsumerDeleted) || errors.Is(err, jetstream.ErrConsumerNotFound) {
			select {
			case gone <- err:
			default:
			}
		}
	}))
	if err != nil {
		return err
	}
	defer cc.Stop()
	select {
	case <-ctx.Done():
		return nil
	case err := <-gone:
		return err
	}
}

// notice is a push's payload, which the service worker shows. It names
// who is waiting and where, and carries no tool input, file contents, or
// commands.
type notice struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	// Tag is the request or question it is, as the page's own
	// notifications tag it, so each replaces the other.
	Tag string `json:"tag"`
	// URL is the item in the phone inbox.
	URL string `json:"url"`
}

// handle pushes one event, if it still waits on a person, to every
// approver's subscriptions. A push to an approver with the world's page in
// sight is held, the event kept from the consumer meanwhile, and then sent
// if the item still waits. An event delivered again is pushed to everyone
// at once, since nothing says who got it before, and a duplicate replaces
// the first.
func (p *pusher) handle(ctx context.Context, host string, msg jetstream.Msg) {
	meta, err := msg.Metadata()
	var ev v1.Event
	if err != nil || json.Unmarshal(msg.Data(), &ev) != nil {
		_ = msg.Term()
		return
	}
	if time.Since(ev.Time) > pushLife {
		_ = msg.Ack()
		return
	}
	if !p.c.waitApplied(ctx, host, meta.Sequence.Stream, 10*time.Second) {
		_ = msg.NakWithDelay(5 * time.Second)
		return
	}
	n, ok := p.c.waitingNotice(host, ev)
	if !ok {
		_ = msg.Ack()
		return
	}
	targets, err := p.targets(ctx)
	if err != nil {
		slog.Warn("citadel: read push subscriptions", "err", err)
		_ = msg.NakWithDelay(5 * time.Second)
		return
	}
	topic := pushTopic(host, ev)
	var held []string
	for approver, subs := range targets {
		if meta.NumDelivered == 1 && p.inSight(ctx, approver) {
			held = append(held, approver)
			continue
		}
		p.sendAll(ctx, subs, n, topic, ev.Time)
	}
	if len(held) == 0 {
		_ = msg.Ack()
		return
	}
	// Hold the event, telling the consumer it's still being handled.
	done := time.After(p.hold)
	tick := time.NewTicker(10 * time.Second)
	defer tick.Stop()
	for waiting := true; waiting; {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			_ = msg.InProgress()
		case <-done:
			waiting = false
		}
	}
	if n, ok = p.c.waitingNotice(host, ev); ok {
		for _, approver := range held {
			p.sendAll(ctx, targets[approver], n, topic, ev.Time)
		}
	}
	_ = msg.Ack()
}

// pushTopic is a push's topic, from the event's identity: up to 32
// characters of the base64url alphabet, as push services take them.
func pushTopic(host string, ev v1.Event) string {
	sum := sha256.Sum256([]byte(host + "/" + ev.ID))
	return base64.RawURLEncoding.EncodeToString(sum[:])[:32]
}

// stored is one stored push subscription: its key in the bucket and what
// the approver signed.
type stored struct {
	key string
	sub webpush.Subscription
}

// targets is every subscription of an approver that still approves, by
// approver.
func (p *pusher) targets(ctx context.Context) (map[string][]stored, error) {
	kv := p.bucket()
	c := p.c
	c.mu.Lock()
	reg := c.world.registry
	owner := c.world.record.Owner
	c.mu.Unlock()
	if kv == nil || reg == nil {
		return nil, nil
	}
	entries, err := allEntries(ctx, kv, "sub.>")
	if err != nil {
		return nil, err
	}
	out := map[string][]stored{}
	for _, e := range entries {
		var s v1.Signed
		if json.Unmarshal(e.Value(), &s) != nil {
			continue
		}
		ps, err := approval.OpenPushSubscription(s, owner)
		if err != nil || !reg.Valid(s.Signer) || subKey(s.Signer, ps.Endpoint) != e.Key() {
			continue
		}
		out[s.Signer] = append(out[s.Signer], stored{key: e.Key(), sub: subscriptionOf(ps)})
	}
	return out, nil
}

// sendAll pushes n to each of subs, and deletes any the push service says
// is gone.
func (p *pusher) sendAll(ctx context.Context, subs []stored, n notice, topic string, at time.Time) {
	p.mu.Lock()
	sender := p.sender
	p.mu.Unlock()
	payload, _ := json.Marshal(n)
	ttl := max(pushLife-time.Since(at), 0)
	for _, s := range subs {
		sctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		err := sender.Send(sctx, s.sub, webpush.Message{Payload: payload, Topic: topic, TTL: ttl, Urgency: "high"})
		cancel()
		switch {
		case errors.Is(err, webpush.ErrGone):
			slog.Info("citadel: a push subscription is gone, so it's deleted", "push", pushServiceOf(s.sub.Endpoint))
			_ = p.bucket().Delete(ctx, s.key)
		case err != nil:
			slog.Warn("citadel: push", "push", pushServiceOf(s.sub.Endpoint), "err", err)
		}
	}
}

// pushServiceOf names an endpoint's push service for the log, without the
// part that names the browser.
func pushServiceOf(endpoint string) string {
	if u, err := url.Parse(endpoint); err == nil {
		return u.Host
	}
	return ""
}

// inSight says whether approver has the world's page in sight through any
// citadel.
func (p *pusher) inSight(ctx context.Context, approver string) bool {
	p.mu.Lock()
	kv := p.presence
	p.mu.Unlock()
	if kv == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	keys, err := kv.ListKeysFiltered(ctx, approver+".*")
	if err != nil {
		return false
	}
	defer func() { _ = keys.Stop() }()
	for range keys.Keys() {
		return true
	}
	return false
}

// inSight is told when a browser signed in as who shows the world's page,
// and the function it returns when the page is hidden or its WebSocket
// closes. Meanwhile the browser's entry in the presence bucket is kept
// fresh.
func (c *citadel) inSight(who string) func() {
	p := c.push
	if p == nil || who == "" {
		return func() {}
	}
	key := who + "." + nuid.Next()
	ctx, cancel := context.WithCancel(c.ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		tick := time.NewTicker(presenceEvery)
		defer tick.Stop()
		for {
			p.mu.Lock()
			kv := p.presence
			p.mu.Unlock()
			if kv != nil {
				pctx, pcancel := context.WithTimeout(ctx, 5*time.Second)
				_, _ = kv.Put(pctx, key, nil)
				pcancel()
			}
			select {
			case <-ctx.Done():
				if kv != nil {
					dctx, dcancel := context.WithTimeout(context.Background(), 5*time.Second)
					_ = kv.Delete(dctx, key)
					dcancel()
				}
				return
			case <-tick.C:
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

// prune deletes the subscriptions of approvers that no longer approve.
// Every citadel does it when the registry changes, and a second delete
// changes nothing.
func (p *pusher) prune(reg *approval.Registry) {
	p.pruning.Lock()
	defer p.pruning.Unlock()
	kv := p.bucket()
	if kv == nil {
		return
	}
	ctx, cancel := context.WithTimeout(p.c.ctx, 15*time.Second)
	defer cancel()
	keys, err := kv.ListKeysFiltered(ctx, "sub.>")
	if err != nil {
		return
	}
	var gone []string
	for k := range keys.Keys() {
		if parts := strings.Split(k, "."); len(parts) == 3 && !reg.Valid(parts[1]) {
			gone = append(gone, k)
		}
	}
	for _, k := range gone {
		if err := kv.Delete(ctx, k); err == nil {
			slog.Info("citadel: deleted a revoked approver's push subscription", "approver", strings.Split(k, ".")[1])
		}
	}
}

// allEntries is every entry under filter.
func allEntries(ctx context.Context, kv jetstream.KeyValue, filter string) ([]jetstream.KeyValueEntry, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	w, err := kv.Watch(ctx, filter, jetstream.IgnoreDeletes())
	if err != nil {
		return nil, err
	}
	defer func() { _ = w.Stop() }()
	var out []jetstream.KeyValueEntry
	for {
		select {
		case e := <-w.Updates():
			if e == nil {
				return out, nil
			}
			out = append(out, e)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// subKey is where approver's subscription for endpoint is kept.
func subKey(approver, endpoint string) string {
	return "sub." + approver + "." + endpointID(endpoint)
}

// endpointID names a subscription by its endpoint, which is the same for
// whichever approver signs it.
func endpointID(endpoint string) string {
	sum := sha256.Sum256([]byte(endpoint))
	return hex.EncodeToString(sum[:16])
}

func subscriptionOf(p v1.PushSubscription) webpush.Subscription {
	s := webpush.Subscription{Endpoint: p.Endpoint}
	s.Keys.P256dh, s.Keys.Auth = p.P256dh, p.Auth
	return s
}

// markApplied records that the projections have taken in host's events
// up to seq.
func (c *citadel) markApplied(host string, seq uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if seq > c.applied[host] {
		c.applied[host] = seq
	}
}

// waitApplied waits up to d for the projections to take in host's events
// up to seq, and says whether they did.
func (c *citadel) waitApplied(ctx context.Context, host string, seq uint64, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for {
		c.mu.Lock()
		at := c.applied[host]
		c.mu.Unlock()
		if at >= seq {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(25 * time.Millisecond):
		}
	}
}

// waitingNotice is the push for a PermissionRequested or QuestionAsked
// event, while what it asked still waits on a person.
func (c *citadel) waitingNotice(host string, ev v1.Event) (notice, bool) {
	var d struct {
		SessionID  string `json:"sessionId"`
		RequestID  string `json:"requestId"`
		QuestionID string `json:"questionId"`
	}
	if ev.Decode(&d) != nil {
		return notice{}, false
	}
	s, ok := c.sessions.Get(d.SessionID)
	if !ok || s.Host != host {
		return notice{}, false
	}
	return noticeFor(s, c.terrain.Name(host, s.RepoID), ev.Type, d.RequestID, d.QuestionID, c.publicURL())
}

// noticeFor words a push as the page's own notifications do (web/src/
// notify.ts), for the request or question of that id in s.
func noticeFor(s projections.Session, repo, eventType, requestID, questionID, base string) (notice, bool) {
	if repo == "" {
		repo = s.RepoID
	}
	who := agentLabel(s.Agent) + " in " + repo + " on " + s.Host
	inbox := base + "/inbox/" + url.PathEscape(s.Host) + "/" + url.PathEscape(s.ID)
	switch eventType {
	case v1.EventPermissionRequested:
		for _, r := range s.Pending {
			if r.RequestID == requestID {
				return notice{Title: who + " " + wants(r.Kind), Body: "Approve or deny it in Skylands.", Tag: "request:" + requestID, URL: inbox + "/requests/" + url.PathEscape(requestID)}, true
			}
		}
	case v1.EventQuestionAsked:
		for _, q := range s.Questions {
			if q.QuestionID == questionID {
				return notice{Title: who + " has a question", Body: "Answer it in Skylands.", Tag: "question:" + questionID, URL: inbox + "/questions/" + url.PathEscape(questionID)}, true
			}
		}
	}
	return notice{}, false
}

// agentLabel names an agent type as the page does.
func agentLabel(agent string) string {
	switch agent {
	case "claude":
		return "Claude Code"
	case "codex":
		return "Codex"
	case "gemini":
		return "Gemini CLI"
	case "goose":
		return "goose"
	case "fake":
		return "Fake agent"
	}
	return agent
}

// wants is what a tool call of this kind wants, in words that name no
// input.
func wants(kind string) string {
	switch kind {
	case "execute":
		return "wants to run a command"
	case "edit":
		return "wants to edit a file"
	case "delete":
		return "wants to delete a file"
	case "move":
		return "wants to move a file"
	case "read":
		return "wants to read a file"
	case "search":
		return "wants to search"
	case "fetch":
		return "wants to fetch from the web"
	case "switch_mode":
		return "wants to switch modes"
	}
	return "wants your approval"
}

// subscribePush answers POST /api/push/subscriptions: a push subscription
// signed with the approver key this browser signed in with, made at the
// citadel's --url.
func (c *citadel) subscribePush(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Subscription v1.Signed `json:"subscription"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "a signed push subscription is required")
		return
	}
	kv, view, me, ok := c.pushReady(w, r)
	if !ok {
		return
	}
	ps, err := approval.OpenPushSubscription(req.Subscription, view.record.Owner)
	switch {
	case err != nil:
		writeError(w, http.StatusBadRequest, "the push subscription: "+err.Error())
		return
	case req.Subscription.Signer != me:
		writeError(w, http.StatusForbidden, "sign the push subscription with the key this browser signed in with")
		return
	case !sameOriginURL(ps.Origin, c.publicURL()):
		writeError(w, http.StatusBadRequest, "this citadel sends push notifications only to pages opened at "+c.publicURL())
		return
	}
	data, _ := json.Marshal(req.Subscription)
	key := subKey(me, ps.Endpoint)
	if _, err := kv.Put(r.Context(), key, data); err != nil {
		writeError(w, http.StatusBadGateway, "store the push subscription: "+err.Error())
		return
	}
	// A browser has one subscription however its key changes, so an older
	// approver's copy of it goes.
	if keys, err := kv.ListKeysFiltered(r.Context(), "sub.*."+endpointID(ps.Endpoint)); err == nil {
		var other []string
		for k := range keys.Keys() {
			if k != key {
				other = append(other, k)
			}
		}
		for _, k := range other {
			_ = kv.Delete(r.Context(), k)
		}
	}
	slog.Info("citadel: push subscription stored", "approver", me, "push", pushServiceOf(ps.Endpoint))
	writeJSON(w, http.StatusOK, map[string]string{"id": endpointID(ps.Endpoint)})
}

// sameOriginURL says whether two URLs have the same origin, as a browser
// writes it: scheme and host, without the scheme's default port.
func sameOriginURL(a, b string) bool {
	origin := func(raw string) string {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			return ""
		}
		scheme, host := strings.ToLower(u.Scheme), strings.ToLower(u.Hostname())
		port := u.Port()
		if (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
			port = ""
		}
		if port != "" {
			return scheme + "://" + net.JoinHostPort(host, port)
		}
		if strings.Contains(host, ":") {
			host = "[" + host + "]"
		}
		return scheme + "://" + host
	}
	o := origin(a)
	return o != "" && o == origin(b)
}

// unsubscribePush answers POST /api/push/unsubscribe: deletes this
// approver's subscription for an endpoint.
func (c *citadel) unsubscribePush(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Endpoint string `json:"endpoint"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil || req.Endpoint == "" {
		writeError(w, http.StatusBadRequest, "the subscription's endpoint is required")
		return
	}
	kv, _, me, ok := c.pushReady(w, r)
	if !ok {
		return
	}
	if err := kv.Delete(r.Context(), subKey(me, req.Endpoint)); err != nil {
		writeError(w, http.StatusBadGateway, "delete the push subscription: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": endpointID(req.Endpoint)})
}

// pushReady is the push bucket, the world, and the approver r signed in
// as, or false after writing why push isn't available.
func (c *citadel) pushReady(w http.ResponseWriter, r *http.Request) (jetstream.KeyValue, worldView, string, bool) {
	if c.push == nil {
		writeError(w, http.StatusConflict, "this citadel doesn't send push notifications")
		return nil, worldView{}, "", false
	}
	view, ok := c.readyWorld(w)
	if !ok {
		return nil, view, "", false
	}
	kv := c.push.bucket()
	if kv == nil {
		writeError(w, http.StatusServiceUnavailable, "the citadel hasn't read the world's push key yet")
		return nil, view, "", false
	}
	me, _, _ := c.signedInAs(r)
	return kv, view, me, true
}
