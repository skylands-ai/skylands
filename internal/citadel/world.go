package citadel

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	v1 "github.com/skylands-ai/skylands/api/v1"
	"github.com/skylands-ai/skylands/internal/approval"
	"github.com/skylands-ai/skylands/internal/citadel/web"
)

// The KV buckets that hold the world: its record, which is written once
// when the world is set up, and its approvers, one entry per device.
const (
	worldBucket     = "skylands-world"
	worldKey        = "record"
	approversBucket = "skylands-approvers"
)

// NATSSettingsURL is the page that lists the NATS settings a world needs.
const NATSSettingsURL = "https://github.com/skylands-ai/skylands/blob/main/docs/NATS.md"

// worldView is what the citadel knows about the world it serves.
type worldView struct {
	// checked is true once the citadel has looked for the world since it
	// connected to NATS. Before that, nobody can tell whether there is one.
	checked bool
	// signed is the world's record, and record what it says. Signed is nil
	// while the world has not been set up.
	signed *v1.Signed
	record v1.WorldRecord
	// token is the one-time setup token while there is no world.
	token string
	// problems are the NATS settings the world needs that are missing.
	problems []string
	worldKV  jetstream.KeyValue
	approKV  jetstream.KeyValue
}

// publicURL is where people open this citadel: the configured URL, or the
// listen address, with an unspecified host shown as 127.0.0.1.
func (c *citadel) publicURL() string {
	if c.cfg.PublicURL != "" {
		return strings.TrimRight(c.cfg.PublicURL, "/")
	}
	host, port, err := net.SplitHostPort(c.cfg.HTTPAddr)
	if err != nil {
		return "http://" + c.cfg.HTTPAddr
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port)
}

// checkWorld looks for the world's record and the NATS settings it needs,
// each time the citadel connects. Without a record, the citadel offers
// setup through a link with a fresh one-time token, and watches for the
// record, which another citadel may write.
func (c *citadel) checkWorld(nc *nats.Conn) {
	ctx, cancel := context.WithTimeout(c.ctx, 15*time.Second)
	defer cancel()
	js, err := jetstream.New(nc)
	if err != nil {
		slog.Warn("citadel: JetStream", "err", err)
		return
	}
	var problems []string
	if _, err := js.AccountInfo(ctx); err != nil {
		problems = append(problems, "JetStream isn't available to this NATS account")
	}
	if nc.ConnectedDomain() == "" {
		problems = append(problems, "the NATS server has no JetStream domain, which the leaf nodes of daemons need to keep their streams apart from the world's")
	}
	for _, p := range problems {
		slog.Warn("citadel: NATS setting missing", "problem", p, "settings", NATSSettingsURL)
	}
	worldKV, werr := js.CreateOrUpdateKeyValue(ctx, jetstream.KeyValueConfig{Bucket: worldBucket, History: 1, Description: "The Skylands world's record"})
	approKV, aerr := js.CreateOrUpdateKeyValue(ctx, jetstream.KeyValueConfig{Bucket: approversBucket, History: 5, Description: "The Skylands world's approver devices"})
	if werr != nil || aerr != nil {
		slog.Warn("citadel: create the world's buckets", "err", errors.Join(werr, aerr))
		c.setWorld(func(w *worldView) { w.problems = problems })
		return
	}
	var signed *v1.Signed
	if e, err := worldKV.Get(ctx, worldKey); err == nil {
		if s, ok := parseRecord(e.Value()); ok {
			signed = &s
		}
	} else if !errors.Is(err, jetstream.ErrKeyNotFound) {
		slog.Warn("citadel: read the world's record", "err", err)
		return
	}
	c.setWorld(func(w *worldView) {
		w.checked, w.problems, w.worldKV, w.approKV = true, problems, worldKV, approKV
	})
	if signed != nil {
		c.worldReady(*signed)
		return
	}
	c.offerSetup()
	// Another citadel may set the world up; when it does, this one stops
	// offering setup.
	watcher, err := worldKV.Watch(c.ctx, worldKey, jetstream.UpdatesOnly())
	if err != nil {
		return
	}
	go func() {
		defer func() { _ = watcher.Stop() }()
		for e := range watcher.Updates() {
			if e == nil {
				continue
			}
			if s, ok := parseRecord(e.Value()); ok {
				c.worldReady(s)
				return
			}
		}
	}()
}

// parseRecord reads a stored world record and verifies it.
func parseRecord(data []byte) (v1.Signed, bool) {
	var s v1.Signed
	if json.Unmarshal(data, &s) != nil {
		return s, false
	}
	if _, err := approval.OpenWorld(s); err != nil {
		slog.Warn("citadel: the stored world record doesn't verify", "err", err)
		return s, false
	}
	return s, true
}

// offerSetup makes a one-time setup token, if there isn't one, and says
// where to set the world up.
func (c *citadel) offerSetup() {
	var link string
	c.setWorld(func(w *worldView) {
		if w.signed != nil {
			return
		}
		if w.token == "" {
			b := make([]byte, 18)
			_, _ = rand.Read(b)
			w.token = base64.RawURLEncoding.EncodeToString(b)
		}
		link = c.publicURL() + "/setup#" + w.token
	})
	if link == "" {
		return
	}
	slog.Info("citadel: this world isn't set up yet; open this link to set it up", "url", link)
	if c.cfg.OnSetupLink != nil {
		c.cfg.OnSetupLink(link)
	}
	c.web.Broadcast(web.Message{Type: "world", World: c.worldInfo()})
}

// worldReady records the world's record, ends setup, and tells browsers.
// The setup request and the watch on the record both report a new world,
// so the second report changes nothing.
func (c *citadel) worldReady(s v1.Signed) {
	rec, err := approval.OpenWorld(s)
	if err != nil {
		return
	}
	known := false
	c.setWorld(func(w *worldView) {
		if known = w.signed != nil && *w.signed == s; !known {
			w.signed, w.record, w.token = &s, rec, ""
		}
	})
	if known {
		return
	}
	slog.Info("citadel: world", "name", rec.Name, "owner", rec.Owner)
	if c.cfg.OnSetupLink != nil {
		c.cfg.OnSetupLink("")
	}
	c.web.Broadcast(web.Message{Type: "world", World: c.worldInfo()})
}

func (c *citadel) setWorld(f func(w *worldView)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	f(&c.world)
}

// worldInfo is the world as browsers see it.
func (c *citadel) worldInfo() *web.WorldInfo {
	c.mu.Lock()
	defer c.mu.Unlock()
	w := c.world
	info := &web.WorldInfo{Checked: w.checked, Ready: w.signed != nil, Problems: w.problems, LeafURL: c.cfg.LeafURL, NATSSettingsURL: NATSSettingsURL}
	if w.signed != nil {
		info.Name, info.Owner = w.record.Name, w.record.Owner
	}
	return info
}

// needsSetup is true once the citadel knows there is no world.
func (c *citadel) needsSetup() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.world.checked && c.world.signed == nil
}

// answerWorldRecord replies to a daemon asking for the world's record:
// the signed record, or nothing while the world isn't set up.
func (c *citadel) answerWorldRecord(m *nats.Msg) {
	c.mu.Lock()
	s := c.world.signed
	c.mu.Unlock()
	if s == nil {
		_ = m.Respond(nil)
		return
	}
	data, _ := json.Marshal(s)
	_ = m.Respond(data)
}

// setupRequest is the body of POST /api/world/setup: the token from the
// setup link, the world's record signed by its new owner key, and the
// first approver, the browser doing the setup, signed by the same key.
type setupRequest struct {
	Token    string    `json:"token"`
	World    v1.Signed `json:"world"`
	Approver v1.Signed `json:"approver"`
}

// getWorld answers GET /api/world.
func (c *citadel) getWorld(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, c.worldInfo())
}

// setupWorld answers POST /api/world/setup. The owner key never reaches
// the citadel: only what it signed does.
func (c *citadel) setupWorld(w http.ResponseWriter, r *http.Request) {
	var req setupRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "a setup request needs a token, a world record, and an approver")
		return
	}
	c.mu.Lock()
	view := c.world
	c.mu.Unlock()
	switch {
	case view.signed != nil:
		writeError(w, http.StatusConflict, "this world is already set up")
		return
	case !view.checked || view.worldKV == nil:
		writeError(w, http.StatusServiceUnavailable, "the citadel hasn't reached NATS yet, so it can't tell whether a world exists")
		return
	case view.token == "" || subtle.ConstantTimeCompare([]byte(req.Token), []byte(view.token)) != 1:
		writeError(w, http.StatusForbidden, "that setup link isn't valid; open the one the citadel printed when it started")
		return
	}
	rec, err := approval.OpenWorld(req.World)
	if err != nil {
		writeError(w, http.StatusBadRequest, "the world record: "+err.Error())
		return
	}
	entry, err := approval.OpenOwnerApprover(req.Approver, rec.Owner)
	if err != nil {
		writeError(w, http.StatusBadRequest, "the approver: "+err.Error())
		return
	}
	recordData, _ := json.Marshal(req.World)
	if _, err := view.worldKV.Create(r.Context(), worldKey, recordData); err != nil {
		if errors.Is(err, jetstream.ErrKeyExists) {
			writeError(w, http.StatusConflict, "this world was just set up from another browser")
			return
		}
		writeError(w, http.StatusBadGateway, "store the world's record: "+err.Error())
		return
	}
	if err := putApprover(r.Context(), view.approKV, entry, req.Approver); err != nil {
		writeError(w, http.StatusBadGateway, "store the approver: "+err.Error())
		return
	}
	slog.Info("citadel: world set up", "name", rec.Name, "owner", rec.Owner, "approver", entry.Name)
	c.worldReady(req.World)
	writeJSON(w, http.StatusOK, c.worldInfo())
}

// addOwnerApprover answers POST /api/world/approvers: an approver entry
// signed by the owner key, as a recovery file makes one.
func (c *citadel) addOwnerApprover(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Approver v1.Signed `json:"approver"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "an approver entry is required")
		return
	}
	c.mu.Lock()
	view := c.world
	c.mu.Unlock()
	if view.signed == nil || view.approKV == nil {
		writeError(w, http.StatusConflict, "this world isn't set up yet")
		return
	}
	entry, err := approval.OpenOwnerApprover(req.Approver, view.record.Owner)
	if err != nil {
		writeError(w, http.StatusBadRequest, "the approver: "+err.Error())
		return
	}
	if err := putApprover(r.Context(), view.approKV, entry, req.Approver); err != nil {
		writeError(w, http.StatusBadGateway, "store the approver: "+err.Error())
		return
	}
	slog.Info("citadel: approver enrolled with the owner key", "approver", entry.Name, "key", entry.Key)
	writeJSON(w, http.StatusOK, map[string]string{"key": entry.Key})
}

func putApprover(ctx context.Context, kv jetstream.KeyValue, entry v1.ApproverEntry, s v1.Signed) error {
	data, _ := json.Marshal(s)
	_, err := kv.Put(ctx, entry.Key, data)
	return err
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
