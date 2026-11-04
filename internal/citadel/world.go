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

// The KV bucket that holds the world's record, which is written once when
// the world is set up. The approver registry is a stream of its own:
// approval.StreamName.
const (
	worldBucket = "skylands-world"
	worldKey    = "record"
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
	// js reaches the world's JetStream domain, where the approver registry
	// stream is.
	js jetstream.JetStream
	// records is the approver registry as stored, by stream sequence, and
	// registry what it says, once the world's owner is known. Nothing is
	// ever taken out of the stream, so nothing is taken out of records.
	records  map[uint64]approval.Record
	registry *approval.Registry
	// watching is true once the citadel watches the approver registry.
	watching bool
	// skipped is the registry records already logged as not counting.
	skipped map[string]bool
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
	_, aerr := js.CreateOrUpdateStream(ctx, approval.StreamConfig())
	if werr != nil || aerr != nil {
		slog.Warn("citadel: create the world's record bucket and approvers stream", "err", errors.Join(werr, aerr))
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
		w.checked, w.problems, w.worldKV, w.js = true, problems, worldKV, js
	})
	c.followApprovers(js)
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
	c.approversChanged()
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
	first, err := approval.Append(r.Context(), view.js, v1.ApproverSubject("approver", entry.Key), req.Approver)
	if err != nil {
		writeError(w, http.StatusBadGateway, "store the approver: "+err.Error())
		return
	}
	slog.Info("citadel: world set up", "name", rec.Name, "owner", rec.Owner, "approver", entry.Name)
	c.worldReady(req.World)
	c.addRecords([]approval.Record{first})
	writeJSON(w, http.StatusOK, c.worldInfo())
}

// addApprover answers POST /api/world/approvers: an approver entry the
// owner key signed, as setup and the recovery file make one, or one signed
// with the claim key of a voucher, as a browser opening an "Add an
// approver" link makes one.
func (c *citadel) addApprover(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Approver v1.Signed `json:"approver"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "an approver entry is required")
		return
	}
	view, ok := c.readyWorld(w)
	if !ok {
		return
	}
	owner := view.record.Owner
	if req.Approver.Signer == owner {
		entry, err := approval.OpenOwnerApprover(req.Approver, owner)
		if err != nil {
			writeError(w, http.StatusBadRequest, "the approver: "+err.Error())
			return
		}
		if !c.storeRecord(w, r.Context(), v1.ApproverSubject("approver", entry.Key), req.Approver, "this browser's key is already an approver") {
			return
		}
		slog.Info("citadel: approver enrolled with the owner key", "approver", entry.Name, "key", entry.Key)
		writeJSON(w, http.StatusOK, map[string]string{"key": entry.Key})
		return
	}
	entry, err := approval.OpenVouchedApprover(req.Approver, owner)
	if err != nil {
		writeError(w, http.StatusBadRequest, "the approver: "+err.Error())
		return
	}
	reg := view.registry
	v, ok := reg.Voucher(entry.Voucher)
	switch {
	case !ok:
		writeError(w, http.StatusNotFound, "that link isn't one an approver made for this world")
		return
	case v.UsedBy != "":
		writeError(w, http.StatusConflict, "that link was already used; ask an approver for a new one")
		return
	case time.Now().After(v.Expires):
		writeError(w, http.StatusGone, "that link expired; ask an approver for a new one")
		return
	case !reg.Valid(v.By):
		writeError(w, http.StatusForbidden, "the approver that made that link no longer approves for this world")
		return
	}
	if _, known := reg.Approver(entry.Key); known {
		writeError(w, http.StatusConflict, "this browser's key is already an approver")
		return
	}
	if !c.storeRecord(w, r.Context(), v1.ApproverSubject("vouched", entry.Voucher), req.Approver, "that link was already used; ask an approver for a new one") {
		return
	}
	by, _ := reg.Approver(v.By)
	slog.Info("citadel: approver enrolled through a voucher", "approver", entry.Name, "key", entry.Key, "by", by.Name)
	writeJSON(w, http.StatusOK, map[string]string{"key": entry.Key})
}

// addVoucher answers POST /api/world/vouchers: a voucher an approver
// signed, so another browser can become an approver.
func (c *citadel) addVoucher(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Voucher v1.Signed `json:"voucher"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "a voucher is required")
		return
	}
	view, ok := c.readyWorld(w)
	if !ok {
		return
	}
	v, err := approval.OpenVoucher(req.Voucher, view.record.Owner)
	if err != nil {
		writeError(w, http.StatusBadRequest, "the voucher: "+err.Error())
		return
	}
	if !view.registry.Valid(req.Voucher.Signer) {
		writeError(w, http.StatusForbidden, "this browser doesn't approve for this world, so it can't add an approver")
		return
	}
	if !c.storeRecord(w, r.Context(), v1.ApproverSubject("voucher", v.Claim), req.Voucher, "that voucher is already stored") {
		return
	}
	c.voucherInfo(w, v.Claim)
}

// getVoucher answers GET /api/world/vouchers/{claim}: who made a voucher,
// until when it works, and whether it was used.
func (c *citadel) getVoucher(w http.ResponseWriter, r *http.Request) {
	if _, ok := c.readyWorld(w); !ok {
		return
	}
	c.voucherInfo(w, r.PathValue("claim"))
}

// voucherInfo writes what browsers see of a voucher.
func (c *citadel) voucherInfo(w http.ResponseWriter, claim string) {
	c.mu.Lock()
	reg := c.world.registry
	c.mu.Unlock()
	if reg == nil {
		writeError(w, http.StatusServiceUnavailable, "the citadel hasn't read the approvers yet")
		return
	}
	v, ok := reg.Voucher(claim)
	if !ok {
		writeError(w, http.StatusNotFound, "that link isn't one an approver made for this world")
		return
	}
	by, _ := reg.Approver(v.By)
	writeJSON(w, http.StatusOK, web.VoucherInfo{
		Claim:   v.Claim,
		By:      v.By,
		ByName:  by.Name,
		Expires: v.Expires.UTC().Format(time.RFC3339),
		Expired: time.Now().After(v.Expires),
		UsedBy:  v.UsedBy,
		Valid:   reg.Valid(v.By),
	})
}

// revokeApprover answers POST /api/world/revocations: a revocation the
// owner key or an approver signed.
func (c *citadel) revokeApprover(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Revocation v1.Signed `json:"revocation"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "a revocation is required")
		return
	}
	view, ok := c.readyWorld(w)
	if !ok {
		return
	}
	reg := view.registry
	rv, err := approval.OpenRevocation(req.Revocation, view.record.Owner)
	if err != nil {
		writeError(w, http.StatusBadRequest, "the revocation: "+err.Error())
		return
	}
	if req.Revocation.Signer != reg.Owner() && !reg.Valid(req.Revocation.Signer) {
		writeError(w, http.StatusForbidden, "this browser doesn't approve for this world, so it can't revoke an approver")
		return
	}
	target, known := reg.Approver(rv.Key)
	if !known {
		writeError(w, http.StatusNotFound, "that key was never an approver for this world")
		return
	}
	if target.Revoked.IsZero() {
		rec, err := approval.Append(r.Context(), view.js, v1.ApproverSubject("revoked", rv.Key), req.Revocation)
		switch {
		case err == nil:
			c.addRecords([]approval.Record{rec})
		case !errors.Is(err, approval.ErrTaken):
			writeError(w, http.StatusBadGateway, "store the revocation: "+err.Error())
			return
		}
		slog.Info("citadel: approver revoked", "approver", target.Name, "key", rv.Key, "through", len(reg.Through(rv.Key)))
	}
	writeJSON(w, http.StatusOK, map[string]string{"key": rv.Key})
}

// getApprovers answers GET /api/world/approvers.
func (c *citadel) getApprovers(w http.ResponseWriter, _ *http.Request) {
	if _, ok := c.readyWorld(w); !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"approvers": c.approverInfo()})
}

// readyWorld is the world, once it is set up and its approvers are read,
// or false after writing why not.
func (c *citadel) readyWorld(w http.ResponseWriter) (worldView, bool) {
	c.mu.Lock()
	view := c.world
	c.mu.Unlock()
	switch {
	case view.signed == nil || view.js == nil:
		writeError(w, http.StatusConflict, "this world isn't set up yet")
		return view, false
	case view.registry == nil:
		writeError(w, http.StatusServiceUnavailable, "the citadel hasn't read the approvers yet")
		return view, false
	}
	return view, true
}

// storeRecord writes a registry record to its own subject, once, and
// takes it into the registry at once rather than waiting for the stream to
// deliver it. If the subject already holds a record, it answers 409 with
// taken.
func (c *citadel) storeRecord(w http.ResponseWriter, ctx context.Context, subject string, s v1.Signed, taken string) bool {
	c.mu.Lock()
	js := c.world.js
	c.mu.Unlock()
	rec, err := approval.Append(ctx, js, subject, s)
	if err != nil {
		if errors.Is(err, approval.ErrTaken) {
			writeError(w, http.StatusConflict, taken)
			return false
		}
		writeError(w, http.StatusBadGateway, "store it: "+err.Error())
		return false
	}
	c.addRecords([]approval.Record{rec})
	return true
}

// followApprovers reads the approver registry stream, which approvers
// change through any citadel, and keeps this citadel's view of it current.
// It starts once, and reads again if reading stops.
func (c *citadel) followApprovers(js jetstream.JetStream) {
	start := false
	c.setWorld(func(w *worldView) {
		if !w.watching {
			w.watching, start = true, true
		}
	})
	if !start {
		return
	}
	go func() {
		for c.ctx.Err() == nil {
			approval.Follow(c.ctx, js, c.addRecords)
			select {
			case <-c.ctx.Done():
			case <-time.After(2 * time.Second):
			}
		}
	}()
}

// addRecords takes registry records into the citadel's copy of the
// registry, and rebuilds it.
func (c *citadel) addRecords(records []approval.Record) {
	c.setWorld(func(w *worldView) {
		if w.records == nil {
			w.records = map[uint64]approval.Record{}
		}
		for _, r := range records {
			w.records[r.Revision] = r
		}
	})
	c.approversChanged()
}

// approversChanged rebuilds the registry, once the world's owner is known,
// and tells browsers.
func (c *citadel) approversChanged() {
	var skipped []string
	built := false
	c.setWorld(func(w *worldView) {
		if w.signed == nil {
			return
		}
		records := make([]approval.Record, 0, len(w.records))
		for _, r := range w.records {
			records = append(records, r)
		}
		w.registry = approval.NewRegistry(w.record.Owner, records)
		built = true
		if w.skipped == nil {
			w.skipped = map[string]bool{}
		}
		for _, s := range w.registry.Skipped {
			if !w.skipped[s] {
				w.skipped[s] = true
				skipped = append(skipped, s)
			}
		}
	})
	if !built {
		return
	}
	for _, s := range skipped {
		slog.Warn("citadel: an approver registry record doesn't count", "why", s)
	}
	c.web.Broadcast(web.Message{Type: "approvers", Approvers: c.approverInfo()})
}

// approverInfo is the approver registry as browsers see it: every key ever
// enrolled, in the order they were enrolled.
func (c *citadel) approverInfo() []web.ApproverInfo {
	c.mu.Lock()
	reg := c.world.registry
	c.mu.Unlock()
	if reg == nil {
		return nil
	}
	who := func(key string) string {
		if key == reg.Owner() {
			return "owner"
		}
		return key
	}
	var out []web.ApproverInfo
	for _, a := range reg.Approvers() {
		info := web.ApproverInfo{Key: a.Key, Name: a.Name, Enrolled: a.Enrolled.UTC().Format(time.RFC3339), By: who(a.By), Voucher: a.Voucher, Valid: reg.Valid(a.Key)}
		if !a.Revoked.IsZero() {
			info.Revoked, info.RevokedBy = a.Revoked.UTC().Format(time.RFC3339), who(a.RevokedBy)
		}
		if info.Valid {
			info.Through = reg.Through(a.Key)
		}
		out = append(out, info)
	}
	return out
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
