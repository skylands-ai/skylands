// Package approval defines the signed approval format and verifies it. An
// approver's device signs, with a passkey, a digest of the request id and the
// exact tool input, and a daemon acts on a decision only when that signature
// verifies against an enrolled approver key.
package approval
