package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestSetupLinkStandsApart(t *testing.T) {
	link := "http://127.0.0.1:8080/setup#abc123"

	plain := setupLinkText(link, false)
	if strings.Contains(plain, "\x1b[") {
		t.Errorf("plain text has escape codes: %q", plain)
	}
	if !strings.HasPrefix(plain, "\n") || !strings.HasSuffix(plain, "\n\n") {
		t.Errorf("plain text isn't set apart by blank lines: %q", plain)
	}
	if !strings.Contains(plain, "\n    "+link+"\n") {
		t.Errorf("the URL isn't alone on its line: %q", plain)
	}

	colored := setupLinkText(link, true)
	if !strings.Contains(colored, styleURL+link+styleReset+"\n") || !strings.Contains(colored, styleHeading) {
		t.Errorf("colored text = %q, want the heading and the URL styled", colored)
	}
	if !strings.Contains(colored, "\n    "+styleURL+link) {
		t.Errorf("the colored URL isn't alone on its line: %q", colored)
	}
}

func TestSetupLinkColorOnlyOnATerminal(t *testing.T) {
	var buf bytes.Buffer
	showSetupLink(&buf, "http://x/setup#t")
	if strings.Contains(buf.String(), "\x1b[") {
		t.Errorf("a buffer got color: %q", buf.String())
	}
	f, err := os.CreateTemp(t.TempDir(), "log")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if wantColor(f) {
		t.Error("a regular file wants color")
	}
	buf.Reset()
	showSetupLink(&buf, "")
	if buf.Len() != 0 {
		t.Errorf("an empty link printed %q", buf.String())
	}
}

func TestNoColorTurnsColorOff(t *testing.T) {
	tty, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0)
	if err != nil {
		t.Skip("no terminal to test against")
	}
	defer func() { _ = tty.Close() }()
	t.Setenv("NO_COLOR", "1")
	if wantColor(tty) {
		t.Error("NO_COLOR=1 still wants color on a terminal")
	}
	t.Setenv("NO_COLOR", "")
	if !wantColor(tty) {
		t.Error("a terminal without NO_COLOR doesn't want color")
	}
}
