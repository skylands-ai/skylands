// Package issuelink reads links to issues, such as
// https://github.com/org/storefront/issues/12, and git remotes, so work
// asked for by an issue's link goes to the repository the issue is in:
// the one with a remote on the link's host at the link's owner and name.
// Links are read in GitHub's form, on github.com or a GitHub Enterprise
// host, which an island's gh reads.
package issuelink

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"

	v1 "github.com/skylands-ai/skylands/api/v1"
)

// Link is one issue.
type Link struct {
	// Host is the link's host, lowercase.
	Host  string `json:"host"`
	Owner string `json:"owner"`
	Repo  string `json:"repo"`
	// Number is the issue's number.
	Number int `json:"number"`
}

// link matches an issue link anywhere in text. Owners and repositories
// are letters, digits, hyphens, underscores, and dots, as GitHub's are.
var link = regexp.MustCompile(`https://([A-Za-z0-9](?:[A-Za-z0-9.-]*[A-Za-z0-9])?)/([A-Za-z0-9][A-Za-z0-9_.-]*)/([A-Za-z0-9_.-]+)/issues/([1-9][0-9]{0,9})(?:[/?#][^\s]*)?`)

// Find returns the first issue link in text.
func Find(text string) (Link, bool) {
	for _, m := range link.FindAllStringSubmatchIndex(text, -1) {
		// A link glued to a word before it, such as xhttps://, isn't one.
		if m[0] > 0 && !boundary(text[m[0]-1]) {
			continue
		}
		if l, ok := linkAt(text, m); ok {
			return l, true
		}
	}
	return Link{}, false
}

// Parse reads raw, and nothing else, as an issue link.
func Parse(raw string) (Link, bool) {
	m := link.FindStringSubmatchIndex(raw)
	if m == nil || m[0] != 0 || m[1] != len(raw) {
		return Link{}, false
	}
	return linkAt(raw, m)
}

// linkAt is the link a match of link found in text. The number must end
// where the link does, or at /, ?, or #, so issues/12abc and an eleventh
// digit aren't read as part of a link.
func linkAt(text string, m []int) (Link, bool) {
	if m[9] < len(text) && wordByte(text[m[9]]) {
		return Link{}, false
	}
	n, err := strconv.Atoi(text[m[8]:m[9]])
	repo := strings.TrimSuffix(text[m[6]:m[7]], ".git")
	if err != nil || repo == "" || repo == "." || repo == ".." {
		return Link{}, false
	}
	return Link{Host: strings.ToLower(text[m[2]:m[3]]), Owner: text[m[4]:m[5]], Repo: repo, Number: n}, true
}

// boundary reports whether b can come just before a link: anything but
// what a word, a host, or a path is made of.
func boundary(b byte) bool {
	return !wordByte(b) && b != '-' && b != '.' && b != '/'
}

// wordByte reports whether b is a letter, a digit, or an underscore.
func wordByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '_'
}

// URL is the issue's link, as Find reads it, without anything after its
// number.
func (l Link) URL() string {
	return "https://" + l.Host + "/" + l.Owner + "/" + l.Repo + "/issues/" + strconv.Itoa(l.Number)
}

// Ref names the issue as GitHub does across repositories: owner/repo#12.
func (l Link) Ref() string {
	return l.Owner + "/" + l.Repo + "#" + strconv.Itoa(l.Number)
}

// CloneURL is the https URL of the issue's repository.
func (l Link) CloneURL() string {
	return "https://" + l.Host + "/" + l.Owner + "/" + l.Repo + ".git"
}

// In reports whether the issue is in the repository a remote points to.
// GitHub's owners and names don't depend on case.
func (l Link) In(r v1.RepoRemote) bool {
	return r.Host != "" && r.Host == l.Host && r.Path == strings.ToLower(l.Owner+"/"+l.Repo)
}

// InAny reports whether the issue is in the repository any of remotes
// points to.
func (l Link) InAny(remotes []v1.RepoRemote) bool {
	for _, r := range remotes {
		if l.In(r) {
			return true
		}
	}
	return false
}

// Remote reads one git remote: its URL without any credentials, and the
// host and path of the repository it points to, lowercase and without a
// trailing .git. A remote with no host, such as a local path, has none.
// An https URL keeps no user name, which can be a token; an ssh URL keeps
// its user, such as git, and never a password.
func Remote(name, raw string) v1.RepoRemote {
	r := v1.RepoRemote{Name: name}
	raw = strings.TrimSpace(raw)
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil {
			return r
		}
		switch {
		case u.Scheme == "ssh" && u.User != nil:
			u.User = url.User(u.User.Username())
		default:
			u.User = nil
		}
		r.URL = u.String()
		if u.Scheme == "file" {
			return r
		}
		r.Host, r.Path = strings.ToLower(u.Hostname()), repoPath(u.Path)
		return r
	}
	// git's short form of ssh, [user@]host:path, has no slash before its
	// first colon. Anything else is a path on disk.
	before, path, ok := strings.Cut(raw, ":")
	if !ok || strings.Contains(before, "/") || before == "" {
		r.URL = raw
		return r
	}
	host := before
	if user, h, ok := strings.Cut(before, "@"); ok {
		host = h
		before = user + "@" + h
	}
	r.URL = before + ":" + path
	r.Host, r.Path = strings.ToLower(host), repoPath(path)
	return r
}

// repoPath is the path of a repository on its host as Remote keeps it.
func repoPath(p string) string {
	p = strings.Trim(p, "/")
	p = strings.TrimSuffix(p, ".git")
	return strings.ToLower(strings.Trim(p, "/"))
}
