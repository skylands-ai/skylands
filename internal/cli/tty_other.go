//go:build !linux && !darwin

package cli

import "context"

func readSecret(context.Context, string) (string, error) { return "", errNoTerminal }
