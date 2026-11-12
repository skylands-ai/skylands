package v1

// A terminal is a shell a person opens in a session's worktree, so they
// can build and test where the agent works without asking it to. Opening
// and closing one are session events; what goes in and out of it is plain
// NATS on the terminal's subjects, which no stream captures.

// Terminal event types.
const (
	EventTerminalOpened = "TerminalOpened"
	EventTerminalClosed = "TerminalClosed"
)

// TerminalOpened records a shell opened in a session's worktree, and the
// approver who signed the opening, with their signature, once the world
// exists.
type TerminalOpened struct {
	SessionID  string `json:"sessionId"`
	TerminalID string `json:"terminalId"`
	// Shell is the program the terminal runs, such as /bin/zsh.
	Shell     string `json:"shell"`
	Approver  string `json:"approver,omitempty"`
	Signature string `json:"signature,omitempty"`
}

// Why a terminal closed, in TerminalClosed.
const (
	// TerminalClosedByPerson is a person closing it.
	TerminalClosedByPerson = "closed"
	// TerminalExited is its shell exiting, as on `exit`.
	TerminalExited = "exited"
	// TerminalSessionEnded is its session being ended.
	TerminalSessionEnded = "session-ended"
	// TerminalWorktreeRemoved is its session's worktree being removed.
	TerminalWorktreeRemoved = "worktree-removed"
	// TerminalRestart is the daemon restarting, which ends every shell.
	TerminalRestart = "restart"
)

// TerminalClosed records that a terminal's shell is gone. By is "local" or
// "citadel" for a person closing it. ExitCode is the shell's own, when it
// exited.
type TerminalClosed struct {
	SessionID  string `json:"sessionId"`
	TerminalID string `json:"terminalId"`
	Reason     string `json:"reason"`
	By         string `json:"by,omitempty"`
	ExitCode   *int   `json:"exitCode,omitempty"`
}

// Terminal commands, sent to a session by request-reply.
const (
	CommandOpenTerminal  = "OpenTerminal"
	CommandCloseTerminal = "CloseTerminal"
)

// OpenTerminal opens a shell in an open session's worktree. The sender
// picks TerminalID, which the daemon refuses once it has seen it, so a
// signed opening opens one terminal only. Cols and Rows are its first
// size, and zero means 80 by 24.
type OpenTerminal struct {
	ID         string `json:"id"`
	SessionID  string `json:"sessionId"`
	TerminalID string `json:"terminalId"`
	Cols       int    `json:"cols,omitempty"`
	Rows       int    `json:"rows,omitempty"`
	// Signature is an approver's signature over the opening, which a
	// command through the citadel needs once the world exists.
	Signature *DecisionSignature `json:"signature,omitempty"`
}

// OpenTerminalResult is the result of an OpenTerminal command.
type OpenTerminalResult struct {
	SessionID  string `json:"sessionId"`
	TerminalID string `json:"terminalId"`
	Shell      string `json:"shell"`
}

// CloseTerminal ends a terminal's shell. Closing makes nothing act, so it
// isn't signed.
type CloseTerminal struct {
	ID         string `json:"id"`
	SessionID  string `json:"sessionId"`
	TerminalID string `json:"terminalId"`
}

// FetchTerminal returns what a terminal last showed, as a TerminalOutput:
// the newest of its output, as much as the daemon keeps. The request names
// it by TerminalID.
const FetchTerminal = "terminal"

// TerminalInput is what a person typed or pasted into a terminal,
// published on its input subject. Each browser view of a terminal picks an
// Attach id and counts its inputs from one in Seq, and once the world
// exists an approver signs each one over both and the SHA-256 of Data, so
// the daemon takes each input once, in order, and only from an approver.
type TerminalInput struct {
	Attach    string             `json:"attach"`
	Seq       int                `json:"seq"`
	Data      []byte             `json:"data"`
	Signature *DecisionSignature `json:"signature,omitempty"`
}

// TerminalSize is a terminal's size in character cells, published on its
// size subject when a browser's view of it changes size. It isn't signed:
// it only tells the shell to draw again.
type TerminalSize struct {
	Cols int `json:"cols"`
	Rows int `json:"rows"`
}

// TerminalOutput is what a terminal's shell wrote, published on its output
// subject as it is written, and what the terminal fetch returns. Off is
// where Data begins among every byte the shell has written, so a reader
// can tell what it already has from what is new.
type TerminalOutput struct {
	Off  int64  `json:"off"`
	Data []byte `json:"data"`
}

// TerminalInputSubject carries a terminal's input: plain NATS, no stream.
func TerminalInputSubject(host, session, terminal string) string {
	return terminalSubject(host, session, terminal) + ".in"
}

// TerminalSizeSubject carries a terminal's size as its viewers change it.
func TerminalSizeSubject(host, session, terminal string) string {
	return terminalSubject(host, session, terminal) + ".size"
}

// TerminalOutputSubject carries a terminal's output, for whoever is
// watching it right now.
func TerminalOutputSubject(host, session, terminal string) string {
	return terminalSubject(host, session, terminal) + ".out"
}

// TerminalInputFilter matches every terminal's input on host, and
// TerminalSizeFilter every terminal's size.
func TerminalInputFilter(host string) string { return "v1.island." + host + ".session.*.term.*.in" }
func TerminalSizeFilter(host string) string  { return "v1.island." + host + ".session.*.term.*.size" }

func terminalSubject(host, session, terminal string) string {
	return "v1.island." + host + ".session." + session + ".term." + terminal
}
