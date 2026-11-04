package v1

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestDescribeInput(t *testing.T) {
	small := []byte(`{"command":"go test ./..."}`)
	hash, size, preview := DescribeInput(small)
	sum := sha256.Sum256(small)
	if hash != hex.EncodeToString(sum[:]) || size != len(small) || preview != string(small) {
		t.Errorf("small input = %s, %d, %q", hash, size, preview)
	}
	if hash, size, preview := DescribeInput(nil); hash != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" || size != 0 || preview != "" {
		t.Errorf("no input = %s, %d, %q", hash, size, preview)
	}
	// A long input is previewed by its start, cut back to a whole
	// character: each ✓ is three bytes, so InputPreviewMax falls inside one.
	big := []byte("x" + strings.Repeat("✓", InputPreviewMax))
	_, size, preview = DescribeInput(big)
	if size != len(big) || len(preview) > InputPreviewMax || len(preview) < InputPreviewMax-3 || !utf8.ValidString(preview) || !strings.HasPrefix(string(big), preview) {
		t.Errorf("big input previewed as %d bytes of %d, valid %v", len(preview), size, utf8.ValidString(preview))
	}
}
