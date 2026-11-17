package placement

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/skylands-ai/skylands/internal/daemon/host"
	"github.com/skylands-ai/skylands/internal/dispatch"
)

// Limits on what is read from a repository to index it.
const (
	// maxPaths is the most tracked files indexed; a repository with more
	// is indexed by the first ones git lists.
	maxPaths = 200_000
	// maxManifests is the most module manifests read.
	maxManifests = 50
	// maxManifestBytes and maxReadmeBytes are the most read of one file.
	maxManifestBytes = 256 * 1024
	maxReadmeBytes   = 64 * 1024
	// maxReadme is the most of the README's first section kept.
	maxReadme = 3000
	// commits is how many recent commit subjects are read.
	commits = 100
)

// head is the commit the checkout is on, or "" in a repository with no
// commits yet.
func head(ctx context.Context, dir string) string {
	out, err := git(ctx, dir, "rev-parse", "--verify", "-q", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// gather reads what the index holds of a repository from its checkout:
// its tracked files, the modules they declare, the first section of its
// README, and its recent commit subjects.
func gather(ctx context.Context, r host.Repo) (dispatch.Source, string, error) {
	src := dispatch.Source{ID: r.ID, Name: r.Name}
	commit := head(ctx, r.Path)
	list, err := git(ctx, r.Path, "ls-files", "-z")
	if err != nil {
		return src, "", err
	}
	for f := range strings.SplitSeq(list, "\x00") {
		if f == "" {
			continue
		}
		if len(src.Paths) == maxPaths {
			break
		}
		src.Paths = append(src.Paths, f)
	}
	src.Modules = modules(r.Path, r.Name, src.Paths)
	src.Readme = readme(r.Path, src.Paths)
	if commit != "" {
		if log, err := git(ctx, r.Path, "log", "-n", strconv.Itoa(commits), "--no-merges", "--format=%s"); err == nil {
			for l := range strings.SplitSeq(log, "\n") {
				if l = strings.TrimSpace(l); l != "" {
					src.Commits = append(src.Commits, l)
				}
			}
		}
	}
	return src, commit, nil
}

// manifestDirs are directories whose manifests aren't the repository's
// own modules: vendored code and test fixtures.
var manifestDirs = map[string]bool{"vendor": true, "node_modules": true, "third_party": true, "testdata": true, "fixtures": true, "examples": true}

// modules reads the names of the modules and packages the repository
// declares in go.mod, package.json, and Cargo.toml, at its top level and
// up to two directories down, as a monorepo's are. The repository's own
// name isn't repeated.
func modules(dir, name string, paths []string) []dispatch.Module {
	seen := map[string]bool{strings.ToLower(name): true}
	var out []dispatch.Module
	read := 0
	for _, p := range paths {
		segs := strings.Split(p, "/")
		if len(segs) > 3 || read == maxManifests {
			continue
		}
		skip := false
		for _, s := range segs[:len(segs)-1] {
			if manifestDirs[s] || strings.HasPrefix(s, ".") {
				skip = true
			}
		}
		base := segs[len(segs)-1]
		if skip || base != "go.mod" && base != "package.json" && base != "Cargo.toml" {
			continue
		}
		read++
		data, err := readAtMost(filepath.Join(dir, filepath.FromSlash(p)), maxManifestBytes)
		if err != nil {
			continue
		}
		var m string
		switch base {
		case "go.mod":
			m = goModule(data)
		case "package.json":
			m = npmPackage(data)
		case "Cargo.toml":
			m = crate(data)
		}
		if m != "" && !seen[strings.ToLower(m)] {
			seen[strings.ToLower(m)] = true
			out = append(out, dispatch.Module{Name: m, Manifest: p})
		}
	}
	return out
}

var majorVersion = regexp.MustCompile(`^v[0-9]+$`)

// goModule is the last element of a go.mod's module path, which is the
// name people call it by, less any major version suffix.
func goModule(data []byte) string {
	s := bufio.NewScanner(bytes.NewReader(data))
	for s.Scan() {
		f := strings.Fields(s.Text())
		if len(f) >= 2 && f[0] == "module" {
			mod := strings.Trim(f[1], `"`+"`")
			elems := strings.Split(mod, "/")
			last := elems[len(elems)-1]
			if majorVersion.MatchString(last) && len(elems) > 1 {
				last = elems[len(elems)-2]
			}
			return last
		}
	}
	return ""
}

// npmPackage is a package.json's name, without its scope.
func npmPackage(data []byte) string {
	var pkg struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(data, &pkg) != nil {
		return ""
	}
	if i := strings.LastIndexByte(pkg.Name, '/'); i >= 0 {
		return pkg.Name[i+1:]
	}
	return pkg.Name
}

// crate is the name in a Cargo.toml's [package] table.
func crate(data []byte) string {
	section := ""
	s := bufio.NewScanner(bytes.NewReader(data))
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if strings.HasPrefix(line, "[") {
			section = strings.Trim(line, "[] ")
			continue
		}
		if section != "package" {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok && strings.TrimSpace(k) == "name" {
			return strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return ""
}

// readme is the first section of the repository's README: its title and
// what comes before the next heading.
func readme(dir string, paths []string) string {
	best := ""
	for _, p := range paths {
		if strings.Contains(p, "/") || !strings.HasPrefix(strings.ToLower(p), "readme") {
			continue
		}
		// A Markdown README is the one people read.
		if best == "" || strings.EqualFold(path.Ext(p), ".md") && !strings.EqualFold(path.Ext(best), ".md") {
			best = p
		}
	}
	if best == "" {
		return ""
	}
	data, err := readAtMost(filepath.Join(dir, best), maxReadmeBytes)
	if err != nil {
		return ""
	}
	return firstSection(string(data))
}

// firstSection is the text up to a document's second heading, in
// Markdown's # or underlined form, at most maxReadme bytes.
func firstSection(doc string) string {
	lines := strings.Split(doc, "\n")
	var keep []string
	headings, fenced := 0, false
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			fenced = !fenced
		}
		underlined := i+1 < len(lines) && t != "" && isUnderline(lines[i+1])
		if !fenced && (strings.HasPrefix(t, "#") || underlined) {
			headings++
			if headings == 2 {
				break
			}
		}
		if !fenced && isUnderline(l) && i > 0 && strings.TrimSpace(lines[i-1]) != "" {
			continue
		}
		keep = append(keep, l)
	}
	out := strings.TrimSpace(strings.Join(keep, "\n"))
	if len(out) > maxReadme {
		out = out[:maxReadme]
	}
	return out
}

func isUnderline(l string) bool {
	t := strings.TrimSpace(l)
	return len(t) >= 3 && (strings.Trim(t, "=") == "" || strings.Trim(t, "-") == "")
}

// readAtMost reads the start of a file, refusing anything but a regular
// file, so a link out of the checkout isn't followed.
func readAtMost(name string, n int64) ([]byte, error) {
	fi, err := os.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, errors.New("not a regular file")
	}
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return io.ReadAll(io.LimitReader(f, n))
}

func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", errors.New("git " + args[0] + ": " + msg)
	}
	return string(out), nil
}
