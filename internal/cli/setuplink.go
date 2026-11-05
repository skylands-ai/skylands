package cli

import (
	"io"
	"os"
)

// showSetupLink prints the link that sets the world up to w, apart from the
// log lines around it, with the URL alone on its line so a double-click or
// the terminal's link detection takes all of it. It's in color when w is a
// terminal and NO_COLOR isn't set. An empty link, once the world is set
// up, prints nothing.
func showSetupLink(w io.Writer, link string) {
	if link == "" {
		return
	}
	_, _ = io.WriteString(w, setupLinkText(link, wantColor(w)))
}

// ANSI styles for the setup link: dark text on bright yellow for the
// heading, which reads on light and dark terminals alike, and bold bright
// green for the URL.
const (
	styleHeading = "\x1b[1;30;103m"
	styleURL     = "\x1b[1;92m"
	styleReset   = "\x1b[0m"
)

// setupLinkText is what showSetupLink prints, as one write, so no log line
// lands inside it.
func setupLinkText(link string, color bool) string {
	if !color {
		return "\n==> Set up this world: open this link in a browser\n\n    " + link + "\n\n"
	}
	return "\n" + styleHeading + " Set up this world " + styleReset + " Open this link in a browser:\n\n    " +
		styleURL + link + styleReset + "\n\n"
}

// wantColor is true when w is a terminal and NO_COLOR, as
// https://no-color.org describes it, isn't set.
func wantColor(w io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
