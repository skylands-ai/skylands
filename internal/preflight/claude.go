package preflight

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
)

// claudeDir is where Claude Code keeps its user settings and plugins.
func claudeDir() string {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude")
}

// claudeManaged is where Claude Code reads managed settings on this
// platform, or empty where it has none.
func claudeManaged() string {
	switch runtime.GOOS {
	case "darwin":
		return "/Library/Application Support/ClaudeCode/managed-settings.json"
	case "linux":
		return "/etc/claude-code/managed-settings.json"
	}
	return ""
}

// withClaudeSettings adds the checks of Claude Code's own settings after
// Claude Code's other checks, when it is installed.
func withClaudeSettings(ctx context.Context, e Env, checks []Check) []Check {
	last := -1
	for i, c := range checks {
		if c.Agent != "claude" {
			continue
		}
		if c.Name == "agent claude" && (c.Missing || c.Status == Fail || c.Status == Skip) {
			return checks
		}
		last = i
	}
	if last < 0 || e.ReadFile == nil {
		return checks
	}
	files, repos := claudeFiles(ctx, e)
	return slices.Insert(checks, last+1, claudeSettingsCheck(e, files, repos), claudeHooksCheck(e, files, repos))
}

// claudeFiles are the Claude Code settings files that apply to sessions on
// this host: the user's, and the project and local settings of each
// repository the daemon has registered. repos counts the repositories, or
// is -1 when no daemon answered to say which there are.
func claudeFiles(ctx context.Context, e Env) (files []string, repos int) {
	if e.ClaudeDir != "" {
		files = append(files, filepath.Join(e.ClaudeDir, "settings.json"))
	}
	repos = -1
	if e.Status != nil {
		if st, err := e.Status(ctx, e.SocketPath); err == nil {
			repos = len(st.Repos)
			for _, r := range st.Repos {
				files = append(files, filepath.Join(r.Path, ".claude", "settings.json"), filepath.Join(r.Path, ".claude", "settings.local.json"))
			}
		}
	}
	return files, repos
}

// readJSON reads a JSON file into v. found is false when there is no file.
func readJSON(e Env, name string, v any) (found bool, err error) {
	data, err := e.ReadFile(name)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err == nil {
		err = json.Unmarshal(data, v)
	}
	return true, err
}

// where says which settings were read, for a check that found nothing.
func where(repos int) string {
	switch {
	case repos < 0:
		return "your settings; no daemon answered to say which repositories to read"
	case repos == 0:
		return "your settings, and no repositories are registered"
	case repos == 1:
		return "your settings or the registered repository's"
	default:
		return fmt.Sprintf("your settings or those of %d registered repositories", repos)
	}
}

// claudeSettingsCheck looks for allow rules in Claude Code's own settings
// files. Claude Code runs a call that one of them allows without asking,
// even in the mode the daemon sets, so the call never reaches the host's
// rules or a person.
func claudeSettingsCheck(e Env, files []string, repos int) Check {
	c := Check{Name: "settings claude", Agent: "claude"}
	var allowing, unreadable []string
	for _, f := range files {
		var settings struct {
			Permissions struct {
				Allow []string `json:"allow"`
			} `json:"permissions"`
		}
		found, err := readJSON(e, f, &settings)
		switch {
		case !found:
		case err != nil:
			unreadable = append(unreadable, f)
		case len(settings.Permissions.Allow) > 0:
			allowing = append(allowing, fmt.Sprintf("%s (%d)", f, len(settings.Permissions.Allow)))
		}
	}
	switch {
	case len(allowing) > 0:
		c.Status, c.Found = Warn, "allow rules in "+strings.Join(allowing, ", ")
		c.Fix = "Claude Code runs a call these rules allow without asking, so neither the host's rules nor a person sees it; remove the rules for calls Skylands should decide"
		if len(unreadable) > 0 {
			c.Found += "; could not read " + strings.Join(unreadable, ", ")
		}
	case len(unreadable) > 0:
		c.Status, c.Found = Warn, "could not read "+strings.Join(unreadable, ", ")
		c.Fix = "check them for allow rules: Claude Code runs a call one allows without asking, so neither the host's rules nor a person sees it"
	default:
		c.Found = "no allow rules in " + where(repos)
	}
	return c
}

// hooks are the hooks a settings file or a plugin defines, by event: each
// event has groups, each with a matcher and the commands to run.
type hooks map[string][]struct {
	Hooks []json.RawMessage `json:"hooks"`
}

// approvingEvents are the hook events whose hooks can approve a tool call
// themselves: PreToolUse before Claude Code decides whether to ask, and
// PermissionRequest when it would ask.
var approvingEvents = []string{"PreToolUse", "PermissionRequest"}

// summary lists a file's hook events and how many hooks each has, the
// ones that can approve a call first, and says whether there are any of
// those.
func (h hooks) summary() (text string, approves bool) {
	counts := map[string]int{}
	for event, groups := range h {
		for _, g := range groups {
			counts[event] += len(g.Hooks)
		}
	}
	events := make([]string, 0, len(counts))
	for event, n := range counts {
		if n > 0 {
			events = append(events, event)
		}
	}
	sort.Slice(events, func(i, j int) bool {
		ai, aj := slices.Contains(approvingEvents, events[i]), slices.Contains(approvingEvents, events[j])
		if ai != aj {
			return ai
		}
		return events[i] < events[j]
	})
	parts := make([]string, 0, len(events))
	for _, event := range events {
		parts = append(parts, fmt.Sprintf("%s %d", event, counts[event]))
		approves = approves || slices.Contains(approvingEvents, event)
	}
	return strings.Join(parts, ", "), approves
}

// claudeHooksCheck looks for hooks: the commands Claude Code runs on
// events, from its settings files, its managed settings, and the plugins
// they enable. A PreToolUse or PermissionRequest hook can approve a tool
// call itself, so the call never reaches the host's rules or a person,
// and every hook runs its command without any approval.
func claudeHooksCheck(e Env, files []string, repos int) Check {
	c := Check{Name: "hooks claude", Agent: "claude"}
	if e.ClaudeManaged != "" {
		files = append(files, e.ClaudeManaged)
	}
	var found, unreadable []string
	approves := false
	enabled := map[string]bool{}
	for _, f := range files {
		var settings struct {
			Hooks          hooks           `json:"hooks"`
			EnabledPlugins map[string]bool `json:"enabledPlugins"`
		}
		ok, err := readJSON(e, f, &settings)
		if !ok {
			continue
		}
		if err != nil {
			unreadable = append(unreadable, f)
			continue
		}
		if text, a := settings.Hooks.summary(); text != "" {
			found = append(found, f+" ("+text+")")
			approves = approves || a
		}
		// A plugin enabled in any of the files may run in some session.
		for plugin, on := range settings.EnabledPlugins {
			enabled[plugin] = enabled[plugin] || on
		}
	}
	var plugins []string
	for plugin, on := range enabled {
		if on {
			plugins = append(plugins, plugin)
		}
	}
	sort.Strings(plugins)
	if len(plugins) > 0 {
		installed, err := installedPlugins(e)
		for _, plugin := range plugins {
			h, perr := pluginHooks(e, installed[plugin])
			switch {
			case err != nil || perr != nil || len(installed[plugin]) == 0:
				unreadable = append(unreadable, "the hooks of the enabled plugin "+plugin)
			default:
				if text, a := h.summary(); text != "" {
					found = append(found, "the plugin "+plugin+" ("+text+")")
					approves = approves || a
				}
			}
		}
	}
	switch {
	case len(found) > 0:
		c.Status, c.Found = Warn, "hooks in "+strings.Join(found, ", ")
		c.Fix = "these hooks run their commands without any approval, as the daemon's user, in each session's directory; remove or turn off the ones Skylands sessions shouldn't run"
		if approves {
			c.Fix = "a PreToolUse or PermissionRequest hook can approve a tool call itself, so neither the host's rules nor a person sees it, and every hook runs its command without any approval; remove or turn off the ones Skylands sessions shouldn't run"
		}
		if len(unreadable) > 0 {
			c.Found += "; could not read " + strings.Join(unreadable, ", ")
		}
	case len(unreadable) > 0:
		c.Status, c.Found = Warn, "could not read "+strings.Join(unreadable, ", ")
		c.Fix = "check them for hooks: a PreToolUse or PermissionRequest hook can approve a tool call itself, so neither the host's rules nor a person sees it"
	default:
		c.Found = "no hooks in enabled plugins or " + where(repos)
	}
	return c
}

// installedPlugins are where Claude Code installed each plugin, by its
// name and marketplace, as in enabledPlugins: Claude Code's own record
// under its directory.
func installedPlugins(e Env) (map[string][]string, error) {
	var record struct {
		Plugins map[string][]struct {
			InstallPath string `json:"installPath"`
		} `json:"plugins"`
	}
	if e.ClaudeDir == "" {
		return nil, errors.New("no Claude Code directory")
	}
	found, err := readJSON(e, filepath.Join(e.ClaudeDir, "plugins", "installed_plugins.json"), &record)
	if err != nil || !found {
		return nil, errors.Join(err, errors.New("no record of installed plugins"))
	}
	out := map[string][]string{}
	for plugin, installs := range record.Plugins {
		for _, in := range installs {
			if in.InstallPath != "" {
				out[plugin] = append(out[plugin], in.InstallPath)
			}
		}
	}
	return out, nil
}

// pluginHooks are the hooks a plugin defines across its installs: in the
// file its manifest names, or in hooks/hooks.json.
func pluginHooks(e Env, installs []string) (hooks, error) {
	out := hooks{}
	for _, dir := range installs {
		file := filepath.Join(dir, "hooks", "hooks.json")
		var manifest struct {
			Hooks json.RawMessage `json:"hooks"`
		}
		if _, err := readJSON(e, filepath.Join(dir, ".claude-plugin", "plugin.json"), &manifest); err != nil {
			return nil, err
		}
		var named string
		if json.Unmarshal(manifest.Hooks, &named) == nil && named != "" {
			file = filepath.Join(dir, named)
		}
		var defined struct {
			Hooks hooks `json:"hooks"`
		}
		if _, err := readJSON(e, file, &defined); err != nil {
			return nil, err
		}
		for event, groups := range defined.Hooks {
			out[event] = append(out[event], groups...)
		}
	}
	return out, nil
}
