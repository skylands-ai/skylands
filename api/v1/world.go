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

// ApproverEntry is the payload that makes a browser an approver: its
// public key and a name for it. The owner key signs it when a browser
// sets the world up or uses the recovery file. A browser that uses an
// approver's voucher signs it with the voucher's claim key instead, and
// names that key in Voucher.
type ApproverEntry struct {
	// Type is "approver".
	Type string `json:"type"`
	// World is the owner's public key, which ties the entry to one world.
	World string `json:"world"`
	// Key is the approver's public nkey, a user key ("U...").
	Key     string `json:"key"`
	Name    string `json:"name"`
	Created string `json:"created"`
	// Voucher is the claim key of the voucher this entry used, for an entry
	// the claim key signs. An entry the owner key signs has none.
	Voucher string `json:"voucher,omitempty"`
}

// Voucher is the payload of a one-time grant an approver signs so another
// browser can become an approver. It names a claim key, whose seed travels
// only in the link the approver shows. The browser that opens the link
// signs its approver entry with that seed. A voucher works once, within
// 15 minutes of when it was stored, by the NATS server's clock.
type Voucher struct {
	// Type is "voucher".
	Type  string `json:"type"`
	World string `json:"world"`
	// Claim is the claim key's public nkey, a user key ("U...").
	Claim   string `json:"claim"`
	Created string `json:"created"`
}

// Revocation is the payload that revokes an approver, signed by the owner
// key or by an approver. A revocation is never undone: the browser it
// names has to become an approver again with a new key.
type Revocation struct {
	// Type is "revocation".
	Type  string `json:"type"`
	World string `json:"world"`
	// Key is the revoked approver's public nkey.
	Key     string `json:"key"`
	Created string `json:"created"`
}
