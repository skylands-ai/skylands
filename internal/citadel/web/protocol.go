package web

import (
	v1 "github.com/skylands-ai/skylands/api/v1"
	"github.com/skylands-ai/skylands/internal/citadel/projections"
)

// Message is the envelope for everything the citadel sends to a browser over
// its WebSocket. Type names which other fields are set:
//
//   - "snapshot" carries Server, Islands, and Repos, and opens every
//     connection.
//   - "server" carries Server when the citadel's NATS connection changes.
//   - "island" carries Island when one host's state changes.
//   - "repo" carries Repo when a repository is registered or its terrain
//     changes.
//   - "session" carries Session whenever an agent session changes.
//   - "live" carries Live, with Host and ID, for the session the browser
//     is watching: text as it is appended to the transcript.
type Message struct {
	Type   string      `json:"type"`
	Host   string      `json:"host,omitempty"`
	ID     string      `json:"id,omitempty"`
	Live   *v1.Live    `json:"live,omitempty"`
	Server *ServerInfo `json:"server,omitempty"`
	World  *WorldInfo  `json:"world,omitempty"`
	// Approvers is every approver key ever enrolled, in a snapshot and in
	// an "approvers" message, which replaces the whole list.
	Approvers []ApproverInfo        `json:"approvers,omitempty"`
	Islands   []projections.Island  `json:"islands,omitempty"`
	Island    *projections.Island   `json:"island,omitempty"`
	Repos     []projections.Repo    `json:"repos,omitempty"`
	Repo      *projections.Repo     `json:"repo,omitempty"`
	Sessions  []projections.Session `json:"sessions,omitempty"`
	Session   *projections.Session  `json:"session,omitempty"`
}

// Inbound is what a browser sends: "watch" with Host and ID to read one
// session's transcript as it is written, or "unwatch" to stop. A browser
// watches at most one session at a time.
type Inbound struct {
	Type string `json:"type"`
	Host string `json:"host,omitempty"`
	ID   string `json:"id,omitempty"`
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
