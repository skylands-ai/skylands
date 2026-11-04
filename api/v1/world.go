package v1

// WorldRecordSubject is where a citadel answers, by request-reply, with
// the world's record as a Signed message, or with an empty body while the
// world has not been set up.
const WorldRecordSubject = "v1.world.record"

// Signed is a message signed with an ed25519 nkey. Payload is the exact
// JSON that was signed, kept as a string so the bytes verified are the
// bytes signed. Signer is the signing key's public nkey, and Sig the
// signature in unpadded base64url.
type Signed struct {
	Payload string `json:"payload"`
	Signer  string `json:"signer"`
	Sig     string `json:"sig"`
}

// WorldRecord is the payload that makes a world: its name and the owner's
// public key. The owner signs it when the world is set up, and it can't
// change afterwards.
type WorldRecord struct {
	// Type is "world".
	Type string `json:"type"`
	Name string `json:"name"`
	// Owner is the owner key's public nkey, an operator key ("O...").
	Owner   string `json:"owner"`
	Created string `json:"created"`
}

// ApproverEntry is the payload that makes a device an approver: its
// public key and a name for it. In this form the owner signs it, which is
// how the browser that sets up a world is enrolled, and how a recovery
// file enrolls a browser.
type ApproverEntry struct {
	// Type is "approver".
	Type string `json:"type"`
	// World is the owner's public key, which ties the entry to one world.
	World string `json:"world"`
	// Key is the approver's public nkey, a user key ("U...").
	Key     string `json:"key"`
	Name    string `json:"name"`
	Created string `json:"created"`
}
