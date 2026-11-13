// Package daemon implements the daemon role, which turns one host into an
// island. A daemon launches agents over the Agent Client Protocol, owns their
// event-sourced sessions, enforces the world's rules locally, verifies
// approval signatures, snapshots the terrain of registered repos, and buffers
// events in an embedded NATS leaf node while the world's server is
// unreachable. A local CLI reaches it over a Unix socket.
package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/micro"

	v1 "github.com/skylands-ai/skylands/api/v1"
	"github.com/skylands-ai/skylands/internal/approval"
	"github.com/skylands-ai/skylands/internal/daemon/agents"
	"github.com/skylands-ai/skylands/internal/daemon/blobs"
	"github.com/skylands-ai/skylands/internal/daemon/fetch"
	"github.com/skylands-ai/skylands/internal/daemon/host"
	"github.com/skylands-ai/skylands/internal/daemon/session"
	"github.com/skylands-ai/skylands/internal/daemon/socket"
	"github.com/skylands-ai/skylands/internal/es"
	"github.com/skylands-ai/skylands/internal/filelock"
	"github.com/skylands-ai/skylands/internal/natsembed"
	"github.com/skylands-ai/skylands/internal/watchdog"
)

// ServiceName is the NATS micro service every daemon registers. Citadels
// ping it to learn which hosts they can reach.
const ServiceName = "skylands-daemon"

// Config holds what a daemon needs to join a world and run agents on this
// host.
type Config struct {
	// Host names this island. It must be unique in the world and is used in
	// subjects, so it holds only letters, digits, hyphens, and underscores.
	// Empty means DefaultHost.
	Host string
	// NATSURL is the URL of the world's NATS leaf node listener that the
	// embedded leaf node dials.
	NATSURL string
	// CredsFile is the path to the daemon's NATS credentials file.
	CredsFile string
	// DataDir holds the leaf node's JetStream store and agent worktrees. The
	// leaf store lives under DataDir/leaf/<host>, so daemons for different
	// hosts can share a DataDir.
	DataDir string
	// SocketPath is the Unix socket the local CLI connects to. Empty runs
	// no local API.
	SocketPath string
	// Version is the skylands version this daemon reports to the world.
	Version string
	// Adapters are the agents this daemon can launch. Nil means the default
	// registry: the built-in fake agent and Claude Code.
	Adapters agents.Registry
	// AgentCheckInterval is how often the daemon checks again which agents
	// it can launch, after its check at startup. Zero means
	// DefaultAgentCheckInterval.
	AgentCheckInterval time.Duration
	// World is the owner key of the world this daemon joins, as the
	// citadel shows it under "Add a host". The daemon pins it the first
	// time, refuses to start later with another, and stops if the world's
	// record names a different owner.
	World string
	// TrustFirstWorld lets a daemon with no World pin the owner key from
	// the first world record it receives. devworld's own daemon does, as it
	// runs in the same process as the citadel and the NATS server.
	TrustFirstWorld bool
	// SetupURL, if set, is the link to set the world up while there is no
	// world yet, which the daemon reports in its status.
	SetupURL func() string
	// WorldDomain is the JetStream domain of the world's NATS server, where
	// the world's approver registry is. Empty means DefaultWorldDomain.
	WorldDomain string
	// StopWhenIdle, once closed, stops the daemon the way cancelling ctx
	// does, at the first moment no agent is working on a turn, so stopping
	// cuts no turn off. A turn waiting for a person's approval is still
	// being worked on. Nil never stops the daemon.
	StopWhenIdle <-chan struct{}
}

// idlePoll is how often a daemon told to stop when idle checks whether an
// agent is still working on a turn.
const idlePoll = 5 * time.Second

// Run joins the world and runs agents until ctx is cancelled. The daemon
// keeps running while the world's server is unreachable: its leaf node
// retries the link on its own, and its events wait in the leaf's stream.
func Run(ctx context.Context, cfg Config) error {
	// Stopping when idle cancels this, which stops everything below the
	// way cancelling the caller's ctx does.
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	hostName := cleanHost(cfg.Host)
	if hostName == "" {
		hostName = DefaultHost()
	}
	// The socket is claimed before anything else starts, so a second daemon
	// started on it stops here, before it opens the data directory it may
	// share with the first.
	var ln net.Listener
	if cfg.SocketPath != "" {
		var err error
		if ln, err = socket.Listen(ctx, cfg.SocketPath); err != nil {
			return err
		}
		defer func() { _ = ln.Close() }()
	}
	// So is the island, in the data directory: two daemons for one island
	// there would share a store and resume the same sessions, on whichever
	// sockets they listen.
	island, err := claimIsland(cfg.DataDir, hostName)
	if err != nil {
		return err
	}
	defer func() { _ = island.Release() }()
	pin, err := pinWorld(cfg.DataDir, hostName, cfg.World, cfg.TrustFirstWorld)
	if err != nil {
		return err
	}
	worldDomain := cfg.WorldDomain
	if worldDomain == "" {
		worldDomain = DefaultWorldDomain
	}
	// The island's leaf node holds its stream in a JetStream domain named
	// after the host, which must not be the world's.
	if hostName == worldDomain {
		return fmt.Errorf("an island can't be named %q, the world's JetStream domain; give it another --host", hostName)
	}
	appr := &approvers{pin: pin}
	// The watchdog ends the agents' process groups if this process dies
	// without ending them. Every agent is launched under ctx, so each
	// registers with it. It closes after everything else here has stopped,
	// with nothing left to end.
	if exe, err := os.Executable(); err == nil {
		if wd, err := watchdog.Start(exe); err == nil {
			defer func() { _ = wd.Close() }()
			ctx = agents.WithWatch(ctx, wd)
		} else {
			slog.Warn("daemon: no watchdog, so if the daemon dies abruptly, what its agents started may keep running", "err", err)
		}
	}
	leaf, err := natsembed.StartLeaf(ctx, natsembed.LeafConfig{
		Name:      ServiceName + "-" + hostName,
		RemoteURL: cfg.NATSURL,
		CredsFile: cfg.CredsFile,
		StoreDir:  filepath.Join(cfg.DataDir, "leaf", hostName),
		Domain:    hostName,
	})
	if err != nil {
		return err
	}
	defer leaf.Shutdown()

	nc, err := nats.Connect("", leaf.InProcess(), nats.Name(ServiceName), nats.MaxReconnects(-1))
	if err != nil {
		return fmt.Errorf("connect to leaf node: %w", err)
	}
	defer nc.Close()

	// The host's stream lives in the leaf's JetStream domain, and the
	// daemon is its only writer.
	store, err := es.New(nc)
	if err != nil {
		return err
	}
	if err := store.EnsureStream(ctx, v1.IslandStreamName(hostName), v1.IslandSubjects(hostName)); err != nil {
		return err
	}
	agg, err := host.Load(ctx, store, hostName)
	if err != nil {
		return err
	}
	transcriptsDir := filepath.Join(cfg.DataDir, "transcripts", hostName)
	kept := blobs.New(filepath.Join(cfg.DataDir, "blobs", hostName))
	uploads := blobs.NewUploads(filepath.Join(cfg.DataDir, "uploads", hostName), kept)
	sessions, err := session.Load(ctx, store, session.Config{
		Host:           hostName,
		WorktreesDir:   filepath.Join(cfg.DataDir, "worktrees", hostName),
		TranscriptsDir: transcriptsDir,
		Blobs:          kept,
		Uploads:        uploads,
		Agents:         func(name string) (v1.AgentAvailability, bool) { return agentNamed(agg, name) },
		Registry:       cfg.Adapters,
		Repos:          agg,
		Publish:        nc.Publish,
		World:          pin.get,
		VerifyDecision: appr.verify,
		Valid:          appr.valid,
	})
	if err != nil {
		return err
	}
	defer sessions.Wait()
	// Worktrees deleted by hand, or with a throwaway world's data
	// directory, leave records in their repositories until git is told.
	sessions.PruneWorktreeRecords(ctx)
	if cfg.StopWhenIdle != nil {
		go func() {
			if whenIdle(ctx, cfg.StopWhenIdle, func() bool { return working(sessions) }, idlePoll) {
				slog.Info("daemon: stopping, now that no agent is working on a turn", "host", hostName)
				stop()
			}
		}()
	}
	agg.RequireSignatures(pin.get, appr.verify)
	// What a revoked approver approved in advance lapses as soon as the
	// daemon reads the revocation: trust it turned on, and rule lists it
	// signed.
	appr.changed = func(reg *approval.Registry) {
		sessions.Lapse(ctx, reg.Valid)
		agg.LapseRules(ctx, reg.Valid)
	}

	// On-demand reads of transcripts and diffs leave the host only when asked.
	responder := fetch.New(hostName, sessions, agg, transcriptsDir)
	responder.MaxPiece = v1.PieceSize(nc.MaxPayload())
	fetchSub, err := nc.Subscribe(v1.IslandFetchFilter(hostName), func(msg *nats.Msg) { go responder.Handle(ctx, msg) })
	if err != nil {
		return fmt.Errorf("subscribe to fetch requests: %w", err)
	}
	defer func() { _ = fetchSub.Unsubscribe() }()

	// Images for prompts arrive in pieces, and wait until a prompt names
	// them. One that none names within the hour is removed.
	uploadSub, err := nc.Subscribe(v1.IslandUploadSubject(hostName), func(msg *nats.Msg) { receiveUpload(uploads, msg) })
	if err != nil {
		return fmt.Errorf("subscribe to uploads: %w", err)
	}
	defer func() { _ = uploadSub.Unsubscribe() }()
	go sweepUploads(ctx, uploads)

	// Session and repository commands arrive by request-reply, one subject
	// per session or repository.
	cmds := &commands{host: hostName, sessions: sessions, agg: agg}
	sub, err := nc.Subscribe(v1.SessionCommandFilter(hostName), func(msg *nats.Msg) { go cmds.handle(ctx, msg) })
	if err != nil {
		return fmt.Errorf("subscribe to session commands: %w", err)
	}
	defer func() { _ = sub.Unsubscribe() }()
	repoSub, err := nc.Subscribe(v1.RepoCommandFilter(hostName), func(msg *nats.Msg) { go cmds.handleRepo(ctx, msg) })
	if err != nil {
		return fmt.Errorf("subscribe to repository commands: %w", err)
	}
	defer func() { _ = repoSub.Unsubscribe() }()
	// What people type into terminals is handled in the order it arrives,
	// so not on goroutines of its own; nothing here waits on the shell.
	inSub, err := nc.Subscribe(v1.TerminalInputFilter(hostName), func(msg *nats.Msg) { cmds.terminalInput(ctx, msg) })
	if err != nil {
		return fmt.Errorf("subscribe to terminal input: %w", err)
	}
	defer func() { _ = inSub.Unsubscribe() }()
	sizeSub, err := nc.Subscribe(v1.TerminalSizeFilter(hostName), cmds.terminalSize)
	if err != nil {
		return fmt.Errorf("subscribe to terminal sizes: %w", err)
	}
	defer func() { _ = sizeSub.Unsubscribe() }()

	started := time.Now().UTC()
	svc, err := micro.AddService(nc, micro.Config{
		Name:        ServiceName,
		Version:     semver(cfg.Version),
		Description: "Skylands daemon on " + hostName,
		Metadata: map[string]string{
			"host":             hostName,
			"os":               runtime.GOOS,
			"arch":             runtime.GOARCH,
			"started":          started.Format(time.RFC3339),
			"skylands_version": cfg.Version,
		},
	})
	if err != nil {
		return fmt.Errorf("register %s service: %w", ServiceName, err)
	}
	defer func() { _ = svc.Stop() }()

	slog.Info("daemon: island up", "host", hostName, "world", cfg.NATSURL, "version", cfg.Version, "repos", len(agg.Repos()), "sessions", len(sessions.List()))

	// Which agents this host can launch is checked in the background, so
	// a slow agent never holds the island up, and the island panel shows
	// the outcome once it is recorded.
	checker := newAgentChecker(agg, cfg.Adapters)
	every := cfg.AgentCheckInterval
	if every <= 0 {
		every = DefaultAgentCheckInterval
	}
	checkCtx, stopChecks := context.WithCancel(ctx)
	checksDone := make(chan struct{})
	go func() {
		defer close(checksDone)
		checker.run(checkCtx, every)
	}()
	// Runs before the deferred closes above, so no check outlives the store.
	defer func() {
		stopChecks()
		<-checksDone
	}()

	api := &localAPI{host: hostName, world: cfg.NATSURL, version: cfg.Version, started: started, agg: agg, sessions: sessions, checker: checker, pin: pin, setupURL: cfg.SetupURL, approvers: appr}
	errc := make(chan error, 1)
	if cfg.SocketPath != "" {
		go func() { errc <- socket.Serve(ctx, ln, api) }()
	}
	// The world's record is checked against the owner key this island
	// belongs to, once a citadel can answer.
	worldErr := make(chan error, 1)
	go func() { worldErr <- checkWorld(ctx, nc, pin) }()
	go appr.follow(ctx, nc, worldDomain)
	select {
	case err := <-worldErr:
		if err != nil {
			slog.Error("daemon: wrong world", "err", err)
			return err
		}
		select {
		case <-ctx.Done():
			if cfg.SocketPath != "" {
				if err := <-errc; err != nil {
					slog.Warn("daemon: local API stopped", "err", err)
				}
			}
			slog.Info("daemon: stopping", "host", hostName)
			return nil
		case err := <-errc:
			return err
		}
	case <-ctx.Done():
		if cfg.SocketPath != "" {
			if err := <-errc; err != nil {
				slog.Warn("daemon: local API stopped", "err", err)
			}
		}
		slog.Info("daemon: stopping", "host", hostName)
		return nil
	case err := <-errc:
		return err
	}
}

// agentNamed is what the host last found of one agent.
func agentNamed(agg *host.Host, name string) (v1.AgentAvailability, bool) {
	for _, a := range agg.Agents() {
		if a.Name == name {
			return a, true
		}
	}
	return v1.AgentAvailability{}, false
}

// receiveUpload takes one piece of an image for a prompt. Pieces of one
// image come one at a time, each after the last was answered, so they
// are taken in the order they arrive.
func receiveUpload(uploads *blobs.Uploads, msg *nats.Msg) {
	var p v1.UploadPiece
	reply := v1.Reply{}
	if err := json.Unmarshal(msg.Data, &p); err != nil {
		reply.Error = "bad UploadPiece: " + err.Error()
	} else if res, err := uploads.Receive(p); err != nil {
		reply.Error = err.Error()
	} else {
		reply.Result, _ = json.Marshal(res)
		if res.Hash != "" {
			slog.Info("daemon: image uploaded", "hash", res.Hash, "size", res.Received, "type", p.MimeType)
		}
	}
	data, _ := json.Marshal(reply)
	if err := msg.Respond(data); err != nil {
		slog.Warn("daemon: reply to an upload", "err", err)
	}
}

// sweepEvery is how often the uploads nobody sent are looked for.
const sweepEvery = 10 * time.Minute

// sweepUploads removes the uploads no prompt named within the hour, now
// and then every sweepEvery, until ctx ends.
func sweepUploads(ctx context.Context, uploads *blobs.Uploads) {
	for {
		if n := uploads.Sweep(time.Now(), blobs.UploadsExpire); n > 0 {
			slog.Info("daemon: removed images uploaded for prompts never sent", "count", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(sweepEvery):
		}
	}
}

// whenIdle waits for stop to close and then for busy to report false,
// checking every poll, and reports true then. It reports false if ctx ends
// first.
func whenIdle(ctx context.Context, stop <-chan struct{}, busy func() bool, poll time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-stop:
	}
	for waited := false; busy(); waited = true {
		if !waited {
			slog.Info("daemon: will stop once no agent is working on a turn")
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(poll):
		}
	}
	return true
}

// working reports whether an agent is working on a turn in any session.
func working(sessions *session.Manager) bool {
	for _, s := range sessions.List() {
		if s.Status == session.StatusRunning {
			return true
		}
	}
	return false
}

// claimIsland takes the lock that a daemon for host holds in dataDir while
// it runs. Daemons for different islands can share a data directory.
func claimIsland(dataDir, host string) (*filelock.Lock, error) {
	dir := filepath.Join(dataDir, "locks")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create %s: %w", dir, err)
	}
	lock, err := filelock.Acquire(filepath.Join(dir, host+".lock"))
	if held := (*filelock.HeldError)(nil); errors.As(err, &held) {
		who := "the daemon for island " + host
		if held.PID > 0 {
			who += fmt.Sprintf(" (process %d)", held.PID)
		}
		return nil, fmt.Errorf("%s is already running with data directory %s; stop it first, or give this one its own --host or --data-dir", who, dataDir)
	}
	return lock, err
}

// commands handles session and repository commands sent to this host.
type commands struct {
	host     string
	sessions *session.Manager
	agg      *host.Host
}

// handleRepo answers one repository command.
func (c *commands) handleRepo(ctx context.Context, msg *nats.Msg) {
	// v1.island.<host>.repo.<repo>.cmd.<type>
	parts := strings.Split(msg.Subject, ".")
	if len(parts) != 7 {
		c.reply(msg, v1.Reply{Error: "malformed command subject " + msg.Subject})
		return
	}
	repoID, commandType := parts[4], parts[6]
	ctx, cancel := context.WithTimeout(ctx, v1.CommandTimeoutFor(commandType))
	defer cancel()
	switch commandType {
	case v1.CommandSetRepoRules:
		var cmd v1.SetRepoRules
		if err := json.Unmarshal(msg.Data, &cmd); err != nil {
			c.reply(msg, v1.Reply{Error: "bad SetRepoRules: " + err.Error()})
			return
		}
		cmd.RepoID = repoID
		r, err := c.agg.SetRules(ctx, cmd, "citadel")
		if err != nil {
			c.reply(msg, v1.Reply{Error: err.Error()})
			return
		}
		c.replyResult(msg, map[string]any{"repoId": r.ID, "rules": r.Rules})
	default:
		c.reply(msg, v1.Reply{Error: "unknown command " + commandType})
	}
}

func (c *commands) handle(ctx context.Context, msg *nats.Msg) {
	// v1.island.<host>.session.<session>.cmd.<type>
	parts := strings.Split(msg.Subject, ".")
	if len(parts) != 7 {
		c.reply(msg, v1.Reply{Error: "malformed command subject " + msg.Subject})
		return
	}
	sessionID, commandType := parts[4], parts[6]
	ctx, cancel := context.WithTimeout(ctx, v1.CommandTimeoutFor(commandType))
	defer cancel()
	switch commandType {
	case v1.CommandStartSession:
		var cmd v1.StartSession
		if err := json.Unmarshal(msg.Data, &cmd); err != nil {
			c.reply(msg, v1.Reply{Error: "bad StartSession: " + err.Error()})
			return
		}
		if cmd.SessionID == "" {
			cmd.SessionID = sessionID
		} else if cmd.SessionID != sessionID {
			c.reply(msg, v1.Reply{Error: "session id in the command does not match the subject"})
			return
		}
		s, err := c.sessions.Start(ctx, cmd)
		if err != nil {
			c.reply(msg, v1.Reply{Error: err.Error()})
			return
		}
		c.replyResult(msg, v1.StartSessionResult{SessionID: s.ID, Worktree: s.Worktree, Branch: s.Branch})
	case v1.CommandSendPrompt:
		var cmd v1.SendPrompt
		if err := json.Unmarshal(msg.Data, &cmd); err != nil {
			c.reply(msg, v1.Reply{Error: "bad SendPrompt: " + err.Error()})
			return
		}
		cmd.SessionID = sessionID
		s, err := c.sessions.Prompt(ctx, cmd)
		if err != nil {
			c.reply(msg, v1.Reply{Error: err.Error()})
			return
		}
		c.replyResult(msg, map[string]any{"sessionId": s.ID, "turns": s.Turns, "status": s.Status})
	case v1.CommandCancelTurn:
		var cmd v1.CancelTurn
		if err := json.Unmarshal(msg.Data, &cmd); err != nil {
			c.reply(msg, v1.Reply{Error: "bad CancelTurn: " + err.Error()})
			return
		}
		cmd.SessionID = sessionID
		s, err := c.sessions.Cancel(ctx, cmd, "citadel")
		if err != nil {
			c.reply(msg, v1.Reply{Error: err.Error()})
			return
		}
		c.replyResult(msg, map[string]any{"sessionId": s.ID, "status": s.Status, "cancelling": s.Cancelling})
	case v1.CommandSetSessionMode:
		var cmd v1.SetSessionMode
		if err := json.Unmarshal(msg.Data, &cmd); err != nil {
			c.reply(msg, v1.Reply{Error: "bad SetSessionMode: " + err.Error()})
			return
		}
		cmd.SessionID = sessionID
		s, err := c.sessions.SetMode(ctx, cmd, "citadel")
		if err != nil {
			c.reply(msg, v1.Reply{Error: err.Error()})
			return
		}
		c.replyResult(msg, map[string]any{"sessionId": s.ID, "mode": s.Mode})
	case v1.CommandSetSessionModel:
		var cmd v1.SetSessionModel
		if err := json.Unmarshal(msg.Data, &cmd); err != nil {
			c.reply(msg, v1.Reply{Error: "bad SetSessionModel: " + err.Error()})
			return
		}
		cmd.SessionID = sessionID
		s, err := c.sessions.SetModel(ctx, cmd, "citadel")
		if err != nil {
			c.reply(msg, v1.Reply{Error: err.Error()})
			return
		}
		c.replyResult(msg, map[string]any{"sessionId": s.ID, "model": s.Model})
	case v1.CommandStopAgent:
		var cmd v1.StopAgent
		if err := json.Unmarshal(msg.Data, &cmd); err != nil {
			c.reply(msg, v1.Reply{Error: "bad StopAgent: " + err.Error()})
			return
		}
		cmd.SessionID = sessionID
		s, err := c.sessions.Stop(ctx, cmd)
		if err != nil {
			c.reply(msg, v1.Reply{Error: err.Error()})
			return
		}
		c.replyResult(msg, map[string]any{"sessionId": s.ID, "status": s.Status})
	case v1.CommandApproveToolCall:
		var cmd v1.ApproveToolCall
		if err := json.Unmarshal(msg.Data, &cmd); err != nil {
			c.reply(msg, v1.Reply{Error: "bad ApproveToolCall: " + err.Error()})
			return
		}
		cmd.SessionID = sessionID
		s, err := c.sessions.Approve(ctx, cmd, "citadel")
		if err != nil {
			c.reply(msg, v1.Reply{Error: err.Error()})
			return
		}
		c.replyResult(msg, map[string]any{"sessionId": s.ID, "pending": len(s.Pending)})
	case v1.CommandDenyToolCall:
		var cmd v1.DenyToolCall
		if err := json.Unmarshal(msg.Data, &cmd); err != nil {
			c.reply(msg, v1.Reply{Error: "bad DenyToolCall: " + err.Error()})
			return
		}
		cmd.SessionID = sessionID
		s, err := c.sessions.Deny(ctx, cmd, "citadel")
		if err != nil {
			c.reply(msg, v1.Reply{Error: err.Error()})
			return
		}
		c.replyResult(msg, map[string]any{"sessionId": s.ID, "pending": len(s.Pending)})
	case v1.CommandAnswerQuestion:
		var cmd v1.AnswerQuestion
		if err := json.Unmarshal(msg.Data, &cmd); err != nil {
			c.reply(msg, v1.Reply{Error: "bad AnswerQuestion: " + err.Error()})
			return
		}
		cmd.SessionID = sessionID
		s, err := c.sessions.Answer(ctx, cmd, "citadel")
		if err != nil {
			c.reply(msg, v1.Reply{Error: err.Error()})
			return
		}
		c.replyResult(msg, map[string]any{"sessionId": s.ID, "questions": len(s.Questions)})
	case v1.CommandTrustSession:
		var cmd v1.TrustSession
		if err := json.Unmarshal(msg.Data, &cmd); err != nil {
			c.reply(msg, v1.Reply{Error: "bad TrustSession: " + err.Error()})
			return
		}
		cmd.SessionID = sessionID
		s, err := c.sessions.Trust(ctx, cmd, "citadel")
		if err != nil {
			c.reply(msg, v1.Reply{Error: err.Error()})
			return
		}
		c.replyResult(msg, map[string]any{"sessionId": s.ID, "trusted": s.Trusted, "pending": len(s.Pending)})
	case v1.CommandSetSessionRules:
		var cmd v1.SetSessionRules
		if err := json.Unmarshal(msg.Data, &cmd); err != nil {
			c.reply(msg, v1.Reply{Error: "bad SetSessionRules: " + err.Error()})
			return
		}
		cmd.SessionID = sessionID
		s, err := c.sessions.SetRules(ctx, cmd, "citadel")
		if err != nil {
			c.reply(msg, v1.Reply{Error: err.Error()})
			return
		}
		c.replyResult(msg, map[string]any{"sessionId": s.ID, "rules": s.Rules})
	case v1.CommandCommitChanges:
		var cmd v1.CommitChanges
		if err := json.Unmarshal(msg.Data, &cmd); err != nil {
			c.reply(msg, v1.Reply{Error: "bad CommitChanges: " + err.Error()})
			return
		}
		cmd.SessionID = sessionID
		s, err := c.sessions.Commit(ctx, cmd)
		if err != nil {
			c.reply(msg, v1.Reply{Error: err.Error()})
			return
		}
		c.replyResult(msg, map[string]any{"sessionId": s.ID, "commit": s.Commit, "commits": s.Commits})
	case v1.CommandMergeChanges:
		var cmd v1.MergeChanges
		if err := json.Unmarshal(msg.Data, &cmd); err != nil {
			c.reply(msg, v1.Reply{Error: "bad MergeChanges: " + err.Error()})
			return
		}
		cmd.SessionID = sessionID
		s, err := c.sessions.Merge(ctx, cmd)
		if err != nil {
			c.reply(msg, v1.Reply{Error: err.Error()})
			return
		}
		c.replyResult(msg, map[string]any{"sessionId": s.ID, "mergedInto": s.MergedInto, "commit": s.MergedCommit})
	case v1.CommandOpenPullRequest:
		var cmd v1.OpenPullRequest
		if err := json.Unmarshal(msg.Data, &cmd); err != nil {
			c.reply(msg, v1.Reply{Error: "bad OpenPullRequest: " + err.Error()})
			return
		}
		cmd.SessionID = sessionID
		s, err := c.sessions.OpenPullRequest(ctx, cmd)
		if err != nil {
			c.reply(msg, v1.Reply{Error: err.Error()})
			return
		}
		c.replyResult(msg, map[string]any{"sessionId": s.ID, "pullRequest": s.PullRequest})
	case v1.CommandOpenTerminal:
		var cmd v1.OpenTerminal
		if err := json.Unmarshal(msg.Data, &cmd); err != nil {
			c.reply(msg, v1.Reply{Error: "bad OpenTerminal: " + err.Error()})
			return
		}
		cmd.SessionID = sessionID
		t, err := c.sessions.OpenTerminal(ctx, cmd, "citadel")
		if err != nil {
			c.reply(msg, v1.Reply{Error: err.Error()})
			return
		}
		c.replyResult(msg, v1.OpenTerminalResult{SessionID: sessionID, TerminalID: t.ID, Shell: t.Shell})
	case v1.CommandCloseTerminal:
		var cmd v1.CloseTerminal
		if err := json.Unmarshal(msg.Data, &cmd); err != nil {
			c.reply(msg, v1.Reply{Error: "bad CloseTerminal: " + err.Error()})
			return
		}
		cmd.SessionID = sessionID
		s, err := c.sessions.CloseTerminal(ctx, cmd, "citadel")
		if err != nil {
			c.reply(msg, v1.Reply{Error: err.Error()})
			return
		}
		c.replyResult(msg, map[string]any{"sessionId": s.ID, "terminals": len(s.Terminals)})
	case v1.CommandRemoveWorktree:
		var cmd v1.RemoveWorktree
		if err := json.Unmarshal(msg.Data, &cmd); err != nil {
			c.reply(msg, v1.Reply{Error: "bad RemoveWorktree: " + err.Error()})
			return
		}
		cmd.SessionID = sessionID
		s, branchDeleted, err := c.sessions.RemoveWorktree(ctx, cmd)
		if err != nil {
			c.reply(msg, v1.Reply{Error: err.Error()})
			return
		}
		c.replyResult(msg, map[string]any{"sessionId": s.ID, "worktreeRemoved": s.WorktreeRemoved, "branchDeleted": branchDeleted})
	default:
		c.reply(msg, v1.Reply{Error: "unknown command " + commandType})
	}
}

// terminalSubject reads the session and terminal from a terminal's input or
// size subject: v1.island.<host>.session.<session>.term.<terminal>.<kind>.
func terminalSubject(subject string) (sessionID, terminalID string, ok bool) {
	parts := strings.Split(subject, ".")
	if len(parts) != 8 || parts[3] != "session" || parts[5] != "term" {
		return "", "", false
	}
	return parts[4], parts[6], true
}

// terminalInput takes one input for a terminal. Nothing answers it: an
// input refused, such as one whose signature doesn't verify, is logged.
func (c *commands) terminalInput(ctx context.Context, msg *nats.Msg) {
	sessionID, terminalID, ok := terminalSubject(msg.Subject)
	if !ok {
		return
	}
	var in v1.TerminalInput
	if err := json.Unmarshal(msg.Data, &in); err != nil {
		slog.Debug("daemon: bad terminal input", "subject", msg.Subject, "err", err)
		return
	}
	if err := c.sessions.TerminalInput(ctx, sessionID, terminalID, in, "citadel"); err != nil {
		level := slog.LevelDebug
		if errors.Is(err, approval.ErrUnsigned) || errors.Is(err, approval.ErrBadSignature) || errors.Is(err, session.ErrStaleInput) {
			level = slog.LevelWarn
		}
		slog.Log(ctx, level, "daemon: terminal input refused", "session", sessionID, "terminal", terminalID, "err", err)
	}
}

// terminalSize resizes a terminal to what a browser's view of it shows.
func (c *commands) terminalSize(msg *nats.Msg) {
	sessionID, terminalID, ok := terminalSubject(msg.Subject)
	if !ok {
		return
	}
	var size v1.TerminalSize
	if json.Unmarshal(msg.Data, &size) != nil {
		return
	}
	if err := c.sessions.ResizeTerminal(sessionID, terminalID, size); err != nil {
		slog.Debug("daemon: terminal resize refused", "session", sessionID, "terminal", terminalID, "err", err)
	}
}

func (c *commands) replyResult(msg *nats.Msg, result any) {
	data, err := json.Marshal(result)
	if err != nil {
		c.reply(msg, v1.Reply{Error: err.Error()})
		return
	}
	c.reply(msg, v1.Reply{Result: data})
}

func (c *commands) reply(msg *nats.Msg, r v1.Reply) {
	data, _ := json.Marshal(r)
	if err := msg.Respond(data); err != nil {
		slog.Warn("daemon: reply to command", "subject", msg.Subject, "err", err)
	}
}

// localAPI answers the local CLI from the daemon's aggregates.
type localAPI struct {
	host      string
	world     string
	version   string
	started   time.Time
	agg       *host.Host
	sessions  *session.Manager
	checker   *agentChecker
	pin       *worldPin
	setupURL  func() string
	approvers *approvers
}

func (a *localAPI) CheckAgents(ctx context.Context) ([]v1.AgentAvailability, error) {
	return a.checker.check(ctx)
}

func (a *localAPI) Status(context.Context) socket.StatusInfo {
	repos := a.agg.Repos()
	infos := make([]socket.RepoInfo, 0, len(repos))
	for _, r := range repos {
		infos = append(infos, repoInfo(r))
	}
	st := socket.StatusInfo{Host: a.host, World: a.world, WorldOwner: a.pin.get(), Version: a.version, Started: a.started, Repos: infos, Sessions: a.Sessions(context.Background()), Agents: a.agg.Agents()}
	if n, ok := a.approvers.count(); ok {
		st.Approvers = &n
	}
	if a.setupURL != nil {
		st.SetupURL = a.setupURL()
	}
	return st
}

func (a *localAPI) Sessions(context.Context) []socket.SessionInfo {
	var out []socket.SessionInfo
	for _, s := range a.sessions.List() {
		out = append(out, a.sessionInfo(s))
	}
	return out
}

func (a *localAPI) sessionInfo(s session.Session) socket.SessionInfo {
	name := s.RepoID
	if r, ok := a.agg.Repo(s.RepoID); ok {
		name = r.Name
	}
	return socket.SessionInfo{
		ID: s.ID, Repo: name, Agent: s.Agent, Mode: s.Mode, Model: s.Model, Status: string(s.Status), Turns: s.Turns, Title: s.Title, Prompt: s.Prompt, Pending: len(s.Pending), Questions: len(s.Questions), Cancelling: s.Cancelling,
		Trusted: s.Trusted, RulesLapsed: s.RulesLapsed,
		Worktree: s.Worktree, Branch: s.Branch, Base: s.Base, InCheckout: s.InCheckout,
		Commit: s.Commit, MergedInto: s.MergedInto, PullRequest: s.PullRequest, WorktreeRemoved: s.WorktreeRemoved,
		StartedAt: s.StartedAt, EndedAt: s.EndedAt,
	}
}

func (a *localAPI) StopSession(ctx context.Context, sessionID, commandID string) (socket.SessionInfo, error) {
	s, err := a.sessions.Stop(ctx, v1.StopAgent{ID: commandID, SessionID: sessionID})
	return a.sessionInfo(s), err
}

func (a *localAPI) CancelTurn(ctx context.Context, sessionID, commandID string) (socket.SessionInfo, error) {
	s, err := a.sessions.Cancel(ctx, v1.CancelTurn{ID: commandID, SessionID: sessionID}, "local")
	return a.sessionInfo(s), err
}

func (a *localAPI) SetMode(ctx context.Context, sessionID, commandID, mode string) (socket.SessionInfo, error) {
	s, err := a.sessions.SetMode(ctx, v1.SetSessionMode{ID: commandID, SessionID: sessionID, Mode: mode}, "local")
	return a.sessionInfo(s), err
}

func (a *localAPI) SetModel(ctx context.Context, sessionID, commandID, model string) (socket.SessionInfo, error) {
	s, err := a.sessions.SetModel(ctx, v1.SetSessionModel{ID: commandID, SessionID: sessionID, Model: model}, "local")
	return a.sessionInfo(s), err
}

func (a *localAPI) RemoveSession(ctx context.Context, sessionID, commandID string, force bool) (socket.RemoveResult, error) {
	s, deleted, err := a.sessions.Remove(ctx, commandID, sessionID, force)
	return socket.RemoveResult{Session: a.sessionInfo(s), BranchDeleted: deleted}, err
}

func (a *localAPI) PruneSessions(ctx context.Context) []socket.PruneResult {
	out := []socket.PruneResult{}
	for _, p := range a.sessions.Prune(ctx) {
		out = append(out, socket.PruneResult{Session: a.sessionInfo(p.Session), Removed: p.Removed, BranchDeleted: p.BranchDeleted, Kept: p.Kept})
	}
	return out
}

// requestInput is a waiting request's whole input, for the host's own CLI,
// or nothing if it can't be read.
func (a *localAPI) requestInput(sessionID, requestID string) json.RawMessage {
	in, err := a.sessions.RequestInput(sessionID, requestID)
	if err != nil || !json.Valid([]byte(in.Input)) {
		return nil
	}
	return json.RawMessage(in.Input)
}

func (a *localAPI) Requests(context.Context) []socket.RequestInfo {
	out := []socket.RequestInfo{}
	for _, r := range a.sessions.Requests() {
		out = append(out, a.requestInfo(r))
	}
	return out
}

func (a *localAPI) requestInfo(r session.Request) socket.RequestInfo {
	name := r.Session.RepoID
	if repo, ok := a.agg.Repo(r.Session.RepoID); ok {
		name = repo.Name
	}
	title := r.Session.Title
	if title == "" {
		title = r.Session.Prompt
	}
	options := make([]socket.OptionInfo, 0, len(r.Permission.Options))
	for _, o := range r.Permission.Options {
		options = append(options, socket.OptionInfo{ID: o.ID, Name: o.Name, Kind: o.Kind})
	}
	return socket.RequestInfo{
		ID: r.Permission.RequestID, Session: r.Session.ID, Conversation: title, Repo: name, Agent: r.Session.Agent,
		Tool: r.Permission.Title, Kind: r.Permission.Kind, Locations: r.Permission.Locations, Input: a.requestInput(r.Session.ID, r.Permission.RequestID), Options: options, Since: r.Permission.At,
	}
}

// History lists the requests answered or expired on this host, oldest
// first, and checks each signature through the citadel against the
// world's approvers as they stand.
func (a *localAPI) History(_ context.Context, sessionID string) []socket.DecisionInfo {
	out := []socket.DecisionInfo{}
	world, reg := a.pin.get(), a.approvers.registry()
	for _, s := range a.sessions.List() {
		if sessionID != "" && s.ID != sessionID {
			continue
		}
		for _, d := range s.Decided {
			out = append(out, a.decisionInfo(world, reg, s, d))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out
}

// decisionInfo is one decided request as the history lists it.
func (a *localAPI) decisionInfo(world string, reg *approval.Registry, s session.Session, d session.Decided) socket.DecisionInfo {
	name := s.RepoID
	if repo, ok := a.agg.Repo(s.RepoID); ok {
		name = repo.Name
	}
	title := s.Title
	if title == "" {
		title = s.Prompt
	}
	info := socket.DecisionInfo{
		Request: d.Permission.RequestID, Session: s.ID, Conversation: title, Repo: name, Agent: s.Agent,
		Tool: d.Permission.Title, Kind: d.Permission.Kind, Answer: d.Answer, Option: d.OptionID, By: d.By,
		Approver: d.Approver, Reason: d.Reason, Asked: d.Permission.At, At: d.At,
	}
	if reg != nil && d.Approver != "" {
		if ap, ok := reg.Approver(d.Approver); ok {
			info.ApproverName = ap.Name
		}
	}
	info.Signature = a.signatureStatus(world, reg, s.ID, d)
	return info
}

// Decision is one decided request with the input it named and what its
// tool call produced, as this host kept them. It leaves checking them
// against their hashes to the reader.
func (a *localAPI) Decision(_ context.Context, requestID string) (socket.DecisionDetail, error) {
	world, reg := a.pin.get(), a.approvers.registry()
	for _, s := range a.sessions.List() {
		for _, d := range s.Decided {
			if d.Permission.RequestID != requestID {
				continue
			}
			out := socket.DecisionDetail{
				DecisionInfo: a.decisionInfo(world, reg, s, d),
				Sig:          d.Signature, ToolCall: d.Permission.ToolCallID,
				InputHash: d.Permission.InputHash, InputSize: d.Permission.InputSize,
			}
			if d.Signature != "" {
				out.World = world
			}
			if data, err := a.sessions.Blob(d.Permission.InputHash); err == nil {
				out.Input, out.InputKept = data, true
			}
			for _, tc := range s.ToolCalls {
				if tc.ID != d.Permission.ToolCallID || (tc.Status != "completed" && tc.Status != "failed") {
					continue
				}
				out.CallStatus, out.OutputHash, out.OutputSize = tc.Status, tc.OutputHash, tc.OutputSize
				if tc.OutputHash != "" {
					if data, err := a.sessions.Blob(tc.OutputHash); err == nil {
						out.Output, out.OutputKept = data, true
					}
				}
			}
			return out, nil
		}
	}
	return socket.DecisionDetail{}, fmt.Errorf("%w: no decision on %s", session.ErrUnknownRequest, requestID)
}

// signatureStatus says whether a decision through the citadel is signed,
// whether its signature is over the decision as the daemon recorded it,
// and whether its approver still approves.
func (a *localAPI) signatureStatus(world string, reg *approval.Registry, sessionID string, d session.Decided) string {
	switch {
	case d.By != "citadel":
		return ""
	case d.Approver == "":
		return "unsigned"
	case world == "" || reg == nil:
		return "not checked"
	}
	dec := approval.ToolCall{V: approval.DecisionVersion, Kind: approval.KindToolCall, World: world, Host: a.host, Session: sessionID, Request: d.Permission.RequestID, Decision: "deny", Input: d.Permission.InputHash}
	if d.Answer == "allowed" {
		dec.Decision, dec.Option = "allow", d.OptionID
	}
	if approval.VerifyDecision(dec, v1.DecisionSignature{Approver: d.Approver, Sig: d.Signature}) != nil {
		return "doesn't verify"
	}
	if !reg.Valid(d.Approver) {
		return "verified, but the approver no longer approves"
	}
	return "verified"
}

func (a *localAPI) Questions(context.Context) []socket.QuestionInfo {
	out := []socket.QuestionInfo{}
	for _, q := range a.sessions.Questions() {
		out = append(out, a.questionInfo(q))
	}
	return out
}

func (a *localAPI) questionInfo(q session.WaitingQuestion) socket.QuestionInfo {
	name := q.Session.RepoID
	if repo, ok := a.agg.Repo(q.Session.RepoID); ok {
		name = repo.Name
	}
	title := q.Session.Title
	if title == "" {
		title = q.Session.Prompt
	}
	return socket.QuestionInfo{ID: q.Question.QuestionID, Session: q.Session.ID, Conversation: title, Repo: name, Agent: q.Session.Agent, Message: q.Question.Message, Fields: q.Question.Fields, Since: q.Question.At}
}

func (a *localAPI) AnswerQuestion(ctx context.Context, questionID, commandID string, answers json.RawMessage, decline bool) (socket.QuestionInfo, error) {
	s, ok := a.sessions.FindQuestion(questionID)
	if !ok {
		return socket.QuestionInfo{}, session.ErrUnknownQuestion
	}
	var info socket.QuestionInfo
	for _, q := range s.Questions {
		if q.QuestionID == questionID {
			info = a.questionInfo(session.WaitingQuestion{Session: s, Question: q})
		}
	}
	_, err := a.sessions.Answer(ctx, v1.AnswerQuestion{ID: commandID, SessionID: s.ID, QuestionID: questionID, Answers: answers, Decline: decline}, "local")
	return info, err
}

func (a *localAPI) Approve(ctx context.Context, requestID, commandID string) (socket.RequestInfo, error) {
	s, ok := a.sessions.FindRequest(requestID)
	if !ok {
		return socket.RequestInfo{}, session.ErrUnknownRequest
	}
	info := a.pendingInfo(s, requestID)
	_, err := a.sessions.Approve(ctx, v1.ApproveToolCall{ID: commandID, SessionID: s.ID, RequestID: requestID}, "local")
	return info, err
}

func (a *localAPI) Deny(ctx context.Context, requestID, commandID string) (socket.RequestInfo, error) {
	s, ok := a.sessions.FindRequest(requestID)
	if !ok {
		return socket.RequestInfo{}, session.ErrUnknownRequest
	}
	info := a.pendingInfo(s, requestID)
	_, err := a.sessions.Deny(ctx, v1.DenyToolCall{ID: commandID, SessionID: s.ID, RequestID: requestID}, "local")
	return info, err
}

func (a *localAPI) pendingInfo(s session.Session, requestID string) socket.RequestInfo {
	for _, p := range s.Pending {
		if p.RequestID == requestID {
			return a.requestInfo(session.Request{Session: s, Permission: p})
		}
	}
	return socket.RequestInfo{ID: requestID, Session: s.ID}
}

func (a *localAPI) Rules(context.Context) []socket.RepoRulesInfo {
	out := []socket.RepoRulesInfo{}
	for _, r := range a.agg.Repos() {
		out = append(out, repoRules(r))
	}
	return out
}

func (a *localAPI) SetRepoRules(ctx context.Context, repoID, commandID string, list []v1.Rule) (socket.RepoRulesInfo, error) {
	r, err := a.agg.SetRules(ctx, v1.SetRepoRules{ID: commandID, RepoID: repoID, Rules: list}, "local")
	return repoRules(r), err
}

func repoRules(r host.Repo) socket.RepoRulesInfo {
	list := r.Rules
	if list == nil {
		list = []v1.Rule{}
	}
	return socket.RepoRulesInfo{RepoID: r.ID, Repo: r.Name, Rules: list, By: r.RulesBy, Approver: r.RulesApprover, Lapsed: r.RulesLapsed}
}

func (a *localAPI) RegisterRepo(ctx context.Context, path, commandID string) (socket.RepoInfo, error) {
	r, err := a.agg.RegisterRepo(ctx, v1.RegisterRepo{ID: commandID, Path: path})
	return repoInfo(r), err
}

func repoInfo(r host.Repo) socket.RepoInfo {
	return socket.RepoInfo{ID: r.ID, Name: r.Name, Path: r.Path, Commit: r.Commit, Files: r.TotalFiles, RegisteredAt: r.RegisteredAt}
}

// DefaultHost names this machine from its hostname: the first label, in
// lower case, with anything a subject can't carry replaced by a hyphen.
func DefaultHost() string {
	name, err := os.Hostname()
	if err != nil || name == "" {
		return "island"
	}
	name, _, _ = strings.Cut(name, ".")
	if h := cleanHost(name); h != "" {
		return h
	}
	return "island"
}

// cleanHost lowercases a name and replaces every character other than
// letters, digits, hyphens, and underscores with a hyphen, so it is safe in
// a NATS subject and as a JetStream domain.
func cleanHost(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '-'
		}
	}, name)
}

var semverRe = regexp.MustCompile(`^v?(\d+\.\d+\.\d+)([-+][0-9A-Za-z.-]+)?$`)

// semver turns a build version into the semantic version the micro service
// registry requires. A git describe such as a7ab8e1-dirty becomes 0.0.0, and
// the real string still travels in the service metadata.
func semver(v string) string {
	if m := semverRe.FindStringSubmatch(v); m != nil {
		return m[1] + m[2]
	}
	return "0.0.0"
}
