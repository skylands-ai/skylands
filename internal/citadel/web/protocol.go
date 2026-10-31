package web

import "github.com/skylands-ai/skylands/internal/citadel/projections"

// Message is the envelope for everything the citadel sends to a browser over
// its WebSocket. Type names which other fields are set:
//
//   - "snapshot" carries Server and Islands, and opens every connection.
//   - "server" carries Server when the citadel's NATS connection changes.
//   - "island" carries Island when one host's state changes.
type Message struct {
	Type    string               `json:"type"`
	Server  *ServerInfo          `json:"server,omitempty"`
	Islands []projections.Island `json:"islands,omitempty"`
	Island  *projections.Island  `json:"island,omitempty"`
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
