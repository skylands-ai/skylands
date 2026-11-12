package web

import (
	v1 "github.com/skylands-ai/skylands/api/v1"
	"github.com/skylands-ai/skylands/internal/citadel/projections"
)

// Message is the envelope for everything the citadel sends to a browser over
// its WebSocket. Type names which other fields are set:
//
//   - "snapshot" carries Server, Islands, and Repos, and opens every
//     connection. While the citadel is still reading what happened, it
//     carries Loading too, and leaves out the hosts being read. Once
//     nothing is, the citadel sends every browser a new snapshot.
//   - "loading" carries Loading when what the citadel is still reading
//     changes, such as when it starts reading a host's stored events.
//   - "server" carries Server when the citadel's NATS connection changes.
//   - "island" carries Island when one host's state changes.
//   - "repo" carries Repo when a repository is registered or its terrain
//     changes.
//   - "session" carries Session whenever an agent session changes.
//   - "live" carries Live, with Host and ID, for the session the browser
//     is watching: text as it is appended to the transcript.
//   - "term" carries Output, with Host, ID, and Terminal, for the terminal
//     the browser is watching: what its shell wrote.
type Message struct {
	Type     string             `json:"type"`
	Host     string             `json:"host,omitempty"`
	ID       string             `json:"id,omitempty"`
	Terminal string             `json:"terminal,omitempty"`
	Live     *v1.Live           `json:"live,omitempty"`
	Output   *v1.TerminalOutput `json:"output,omitempty"`
	Server   *ServerInfo        `json:"server,omitempty"`
	World    *WorldInfo         `json:"world,omitempty"`
	// Approvers is every approver key ever enrolled, in a snapshot and in
	// an "approvers" message, which replaces the whole list.
	Approvers []ApproverInfo        `json:"approvers,omitempty"`
	Islands   []projections.Island  `json:"islands,omitempty"`
	Island    *projections.Island   `json:"island,omitempty"`
	Repos     []projections.Repo    `json:"repos,omitempty"`
	Repo      *projections.Repo     `json:"repo,omitempty"`
	Sessions  []projections.Session `json:"sessions,omitempty"`
	Session   *projections.Session  `json:"session,omitempty"`
	Loading   *Loading              `json:"loading,omitempty"`
}

// Loading is what a citadel still has to read before what it sends is the
// world as it is now, and not some point in its history. Nothing about a
// host it names is sent until the host has been read.
type Loading struct {
	// NATS is true until the citadel has reached the world's NATS server
	// and listed the hosts it followed before.
	NATS bool `json:"nats,omitempty"`
	// Hosts are the hosts whose stored events the citadel is reading, and,
	// as it starts, the hosts it followed before that it hasn't heard from.
	Hosts []string `json:"hosts,omitempty"`
}

// Inbound is what a browser sends: "watch" with Host and ID to read one
// session's transcript as it is written, or "unwatch" to stop, and
// "visible" or "hidden" when its page comes into sight or leaves it. A
// browser watches at most one session at a time.
//
// For a terminal, named by Host, ID, and Terminal, it sends "term-watch"
// to follow what the shell writes, or "term-unwatch" to stop, following
// at most one terminal at a time; "term-input" with Input, what a person
// typed, signed in the browser for the daemon to check; and "term-size"
// with Size when its view of the terminal changes size.
type Inbound struct {
	Type     string            `json:"type"`
	Host     string            `json:"host,omitempty"`
	ID       string            `json:"id,omitempty"`
	Terminal string            `json:"terminal,omitempty"`
	Input    *v1.TerminalInput `json:"input,omitempty"`
	Size     *v1.TerminalSize  `json:"size,omitempty"`
}

// ApproverInfo is one approver key, as the world's registry decides it.
type ApproverInfo struct {
	Key      string `json:"key"`
	Name     string `json:"name"`
	Enrolled string `json:"enrolled"`
	// By is "owner" for an approver the owner key enrolled, through setup
	// or the recovery file, or the key of the approver whose voucher it
	// used, named in Voucher.
	By      string `json:"by"`
	Voucher string `json:"voucher,omitempty"`
	// Valid is true while it approves: not revoked, and enrolled through
	// approvers that aren't, back to the owner key.
	Valid bool `json:"valid"`
	// Revoked is when it was revoked, and RevokedBy "owner" or the key of
	// the approver that revoked it.
	Revoked   string `json:"revoked,omitempty"`
	RevokedBy string `json:"revokedBy,omitempty"`
	// Through is the valid approvers enrolled through this one: what
	// revoking it takes along.
	Through []string `json:"through,omitempty"`
}

// VoucherInfo is what a browser sees of a voucher: who made it, until
// when it works, and whether it was used.
type VoucherInfo struct {
	Claim   string `json:"claim"`
	By      string `json:"by"`
	ByName  string `json:"byName"`
	Expires string `json:"expires"`
	Expired bool   `json:"expired"`
	UsedBy  string `json:"usedBy,omitempty"`
	// Valid is true while the approver that made it approves.
	Valid bool `json:"valid"`
}

// WorldInfo is the world as the citadel knows it.
type WorldInfo struct {
	// Version is the skylands version of the citadel serving the page,
	// which the web client shows beside the Skylands name.
	Version string `json:"version,omitempty"`
	// Checked is true once the citadel has looked for the world since it
	// reached NATS; before that, nobody can tell whether there is one.
	Checked bool `json:"checked"`
	// Ready is true once the world has been set up.
	Ready bool   `json:"ready"`
	Name  string `json:"name,omitempty"`
	// Owner is the owner key's public nkey, which a daemon on another host
	// is given to pin.
	Owner string `json:"owner,omitempty"`
	// LeafURL is the address daemons on other hosts dial, when known.
	LeafURL string `json:"leafUrl,omitempty"`
	// URL is the address people open this citadel at, when it was given
	// one with --url, which an invite link can name.
	URL string `json:"url,omitempty"`
	// PushKey is the world's VAPID public key, which a browser subscribes
	// to push notifications with, while this citadel sends them.
	PushKey string `json:"pushKey,omitempty"`
	// Problems are the NATS settings the world needs that are missing.
	Problems        []string `json:"problems,omitempty"`
	NATSSettingsURL string   `json:"natsSettingsUrl,omitempty"`
}

// ServerInfo describes the NATS server as it describes itself to the
// citadel's connection, which is what the world panel shows.
type ServerInfo struct {
	// Connected is false while the citadel is trying to reach NATS.
	Connected bool `json:"connected"`
	// URL is the address the citadel is connected through, or trying.
	URL     string `json:"url,omitempty"`
	Name    string `json:"name,omitempty"`
	ID      string `json:"id,omitempty"`
	Version string `json:"version,omitempty"`
	Cluster string `json:"cluster,omitempty"`
	// Servers is how many servers the citadel knows of in the cluster.
	Servers int `json:"servers,omitempty"`
	// Domain is the world's JetStream domain.
	Domain string `json:"domain,omitempty"`
}
