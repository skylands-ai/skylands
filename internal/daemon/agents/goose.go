package agents

// gooseModes are the modes a goose session may be in, set through its
// mode setting: approve, where every session starts, and chat, in which
// goose runs no tools at all. goose's default mode, auto, approves every
// tool call itself, so none would reach the daemon's rules or a person.
// In approve mode goose asks before every tool call, except one its own
// settings always allow.
// Its smart_approve mode would let reads through, but which calls are
// reads is decided by goose, partly by asking a model, rather than by the
// daemon's rules. goose reports no kind for its tool calls, so the rules
// cannot tell its reads apart either, and every call waits on a person
// unless a rule or trust lets it pass.
var gooseModes = []string{"approve", "chat"}

// gooseInstall is how to get goose, which comes as a binary for each
// platform rather than from npm, so the daemon runs the one on PATH. goose
// keeps its model provider in its own config, set up with goose configure.
const gooseInstall = "install goose (https://block.github.io/goose/docs/getting-started/installation), then run goose configure to choose its model provider"
