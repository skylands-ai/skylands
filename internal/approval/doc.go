// Package approval verifies who may decide for a world. A world belongs to
// an owner key, an ed25519 nkey created in the owner's browser when the
// world is set up and kept only in the owner's recovery file. Approvers are
// devices, each with its own nkey, enrolled by an entry the owner key
// signs. Everything here is a v1.Signed message, verified against the
// exact bytes that were signed.
package approval
