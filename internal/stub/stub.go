// Package stub holds the placeholder error returned by scaffolding whose
// behavior is designed but not yet implemented. Delete this package once
// nothing imports it.
package stub

import "errors"

// ErrUnimplemented is returned by every entry point that exists in the
// design but has no implementation yet.
var ErrUnimplemented = errors.New("unimplemented")
