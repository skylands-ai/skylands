package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nkeys"

	v1 "github.com/skylands-ai/skylands/api/v1"
	"github.com/skylands-ai/skylands/internal/approval"
	"github.com/skylands-ai/skylands/internal/citadel"
	"github.com/skylands-ai/skylands/internal/daemon"
	"github.com/skylands-ai/skylands/internal/daemon/agents"
	"github.com/skylands-ai/skylands/internal/daemon/socket"
	"github.com/skylands-ai/skylands/internal/fakeagent"
	"github.com/skylands-ai/skylands/internal/natsembed"
)

const testPassphrase = "correct horse battery"

// withPassphrase answers every passphrase prompt with pass, and keeps the
// CLI's approver keys in a directory of the test's own.
func withPassphrase(t *testing.T, pass string) (dir string) {
	t.Helper()
	dir = t.TempDir()
	oldAsk, oldDir := askSecret, approversDir
	askSecret = func(context.Context, string) (string, error) { return pass, nil }
	approversDir = func() string { return dir }
	t.Cleanup(func() { askSecret, approversDir = oldAsk, oldDir })
	return dir
}

func freeTCP(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().String()
}

func postTo(t *testing.T, client *http.Client, url string, body any) (int, string) {
	t.Helper()
	data, _ := json.Marshal(body)
	resp, err := client.Post(url, "application/json", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out bytes.Buffer
	_, _ = out.ReadFrom(resp.Body)
	return resp.StatusCode, out.String()
}

// TestApproverAddEnrollsThisCLI sets a world up through a citadel, has its
// approver make an invite link, and makes this CLI an approver with it,
// listed in the world's approvers as a CLI.
func TestApproverAddEnrollsThisCLI(t *testing.T) {
	dir := withPassphrase(t, testPassphrase)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	hub, err := natsembed.Start(ctx, natsembed.Config{Name: "hub", ClientAddr: "127.0.0.1:0", StoreDir: t.TempDir(), Domain: "world"})
	if err != nil {
		t.Fatal(err)
	}
	defer hub.Shutdown()
	addr := freeTCP(t)
	links := make(chan string, 4)
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- citadel.Run(runCtx, citadel.Config{NATSURL: hub.ClientURL(), HTTPAddr: addr, OnSetupLink: func(l string) { links <- l }})
	}()
	defer func() {
		stop()
		if err := <-done; err != nil {
			t.Errorf("citadel.Run returned %v", err)
		}
	}()
	base := "http://" + addr
	var token string
	select {
	case l := <-links:
		token = strings.TrimPrefix(l, base+"/setup#")
	case <-time.After(15 * time.Second):
		t.Fatal("the citadel never offered a setup link")
	}

	owner, _ := nkeys.CreateOperator()
	ownerPub, _ := owner.PublicKey()
	laptop, _ := nkeys.CreateUser()
	laptopPub, _ := laptop.PublicKey()
	world, _ := approval.Sign(owner, v1.WorldRecord{Type: "world", Name: "test world", Owner: ownerPub, Created: approval.Now()})
	first, _ := approval.Sign(owner, v1.ApproverEntry{Type: "approver", World: ownerPub, Key: laptopPub, Name: "laptop", Created: approval.Now()})
	if code, out := postTo(t, http.DefaultClient, base+"/api/world/setup", map[string]any{"token": token, "world": world, "approver": first}); code != http.StatusOK {
		t.Fatalf("setup = %d %s", code, out)
	}

	// The laptop signs in and makes an invite link, as Settings does.
	jar, _ := cookiejar.New(nil)
	asLaptop := &http.Client{Jar: jar}
	resp, err := asLaptop.Get(base + "/api/signin")
	if err != nil {
		t.Fatal(err)
	}
	var challenge v1.SignIn
	_ = json.NewDecoder(resp.Body).Decode(&challenge)
	_ = resp.Body.Close()
	challenge.Type = "signin"
	signin, _ := approval.Sign(laptop, challenge)
	if code, out := postTo(t, asLaptop, base+"/api/signin", map[string]any{"signin": signin}); code != http.StatusOK {
		t.Fatalf("sign in = %d %s", code, out)
	}
	claim, _ := nkeys.CreateUser()
	claimPub, _ := claim.PublicKey()
	claimSeed, _ := claim.Seed()
	voucher, _ := approval.Sign(laptop, v1.Voucher{Type: "voucher", World: ownerPub, Claim: claimPub, Created: approval.Now()})
	if code, out := postTo(t, asLaptop, base+"/api/world/vouchers", map[string]any{"voucher": voucher}); code != http.StatusOK {
		t.Fatalf("voucher = %d %s", code, out)
	}
	link := base + "/setup#approver=" + string(claimSeed)
	noDaemon := filepath.Join(t.TempDir(), "none.sock")

	if code, _, errOut := run(t, "approver", "add", base+"/setup#token", "--socket", noDaemon); code == 0 || !strings.Contains(errOut, "names no invite") {
		t.Errorf("a link with no invite: code %d, %s", code, errOut)
	}
	code, out, errOut := run(t, "approver", "add", link, "--name", "CLI on the test box", "--socket", noDaemon)
	if code != 0 || !strings.Contains(out, "laptop invited this CLI to approve what agents do in test world") || !strings.Contains(out, "now an approver in test world") {
		t.Fatalf("approver add: code %d\n%s%s", code, out, errOut)
	}

	// The key is kept sealed, readable only by this user, and opens with
	// the passphrase.
	path := filepath.Join(dir, ownerPub+".json")
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("the key file: %v, %v", info, err)
	}
	f, err := loadApprover(ownerPub)
	if err != nil || f.Name != "CLI on the test box" || f.Citadel != base {
		t.Fatalf("loadApprover = %+v, %v", f, err)
	}
	if raw, _ := os.ReadFile(path); bytes.Contains(raw, []byte("SU")) {
		t.Error("the key file holds a seed in the clear")
	}
	seed, err := f.Seed.Open(testPassphrase)
	if err != nil {
		t.Fatal(err)
	}
	if kp, err := nkeys.FromSeed(seed); err != nil {
		t.Fatal(err)
	} else if pub, _ := kp.PublicKey(); pub != f.Key {
		t.Errorf("the sealed key is %s, the file names %s", pub, f.Key)
	}

	// The world's approvers list the CLI as one.
	resp, err = http.Get(base + "/api/world/approvers")
	if err != nil {
		t.Fatal(err)
	}
	var listed struct {
		Approvers []struct {
			Key, Name, Device, By string
			Valid                 bool
		} `json:"approvers"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&listed)
	_ = resp.Body.Close()
	found := false
	for _, a := range listed.Approvers {
		if a.Key == f.Key {
			found = a.Name == "CLI on the test box" && a.Device == v1.ApproverDeviceCLI && a.By == laptopPub && a.Valid
		}
	}
	if !found {
		t.Errorf("approvers = %+v, want the CLI among them", listed.Approvers)
	}

	if code, out, _ := run(t, "approver", "list"); code != 0 || !strings.Contains(out, "test world") || !strings.Contains(out, f.Key) {
		t.Errorf("approver list: code %d\n%s", code, out)
	}
	if code, _, errOut := run(t, "approver", "add", link, "--socket", noDaemon); code == 0 || !strings.Contains(errOut, "already an approver") {
		t.Errorf("a second add for the same world: code %d, %s", code, errOut)
	}
}

// TestLocalDecisionsAreSignedByTheCLI approves a request and changes a
// repository's rules from the CLI, which signs each with its approver key,
// and the daemon takes them only because the signatures verify.
func TestLocalDecisionsAreSignedByTheCLI(t *testing.T) {
	withPassphrase(t, testPassphrase)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	hub, err := natsembed.Start(ctx, natsembed.Config{Name: "hub", ClientAddr: "127.0.0.1:0", LeafAddr: freeTCP(t), StoreDir: t.TempDir(), Domain: "world"})
	if err != nil {
		t.Fatal(err)
	}
	defer hub.Shutdown()
	nc, err := nats.Connect(hub.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := js.CreateOrUpdateStream(ctx, approval.StreamConfig()); err != nil {
		t.Fatal(err)
	}
	owner, _ := nkeys.CreateOperator()
	ownerPub, _ := owner.PublicKey()
	enroll := func(name, device string) nkeys.KeyPair {
		t.Helper()
		kp, _ := nkeys.CreateUser()
		pub, _ := kp.PublicKey()
		entry, _ := approval.Sign(owner, v1.ApproverEntry{Type: "approver", World: ownerPub, Key: pub, Name: name, Created: approval.Now(), Device: device})
		if _, err := approval.Append(ctx, js, v1.ApproverSubject("approver", pub), entry); err != nil {
			t.Fatal(err)
		}
		return kp
	}
	starter := enroll("laptop", "")
	cliKey := enroll("CLI on ws", v1.ApproverDeviceCLI)
	cliPub, _ := cliKey.PublicKey()
	seed, _ := cliKey.Seed()
	sealed, err := approval.Seal(seed, testPassphrase)
	if err != nil {
		t.Fatal(err)
	}
	tmp, err := writeApprover(approverFile{Skylands: "cli-approver", Version: 1, World: ownerPub, Key: cliPub, Name: "CLI on ws", Enrolled: approval.Now(), Seed: sealed})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, approverPath(ownerPub)); err != nil {
		t.Fatal(err)
	}

	sockDir, err := os.MkdirTemp("", "sk")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(sockDir) }()
	sock := filepath.Join(sockDir, "d.sock")
	fakeCmd, fakeEnv := fakeagent.TestCommand()
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- daemon.Run(runCtx, daemon.Config{
			Host: "ws", NATSURL: hub.LeafURL(), DataDir: t.TempDir(), SocketPath: sock, World: ownerPub, WorldDomain: "world",
			Adapters: agents.Registry{"fake": {Name: "fake", Provider: "local", Command: fakeCmd, Env: fakeEnv}},
		})
	}()
	defer func() {
		stop()
		if err := <-done; err != nil {
			t.Errorf("daemon.Run returned %v", err)
		}
	}()
	cli := socket.Dial(sock)
	repoDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(repoDir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "main.go"}, {"commit", "-q", "-m", "start"}} {
		cmd := exec.Command("git", append([]string{"-C", repoDir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	var repo socket.RepoInfo
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if repo, err = cli.RegisterRepo(ctx, repoDir); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("register repo: %v", err)
		}
	}

	// The world starts a session whose edit waits on a person.
	start := v1.StartSession{ID: "c1", SessionID: "s1", RepoID: repo.ID, Agent: "fake", Prompt: "fix it"}
	sig, _ := approval.SignDecision(starter, approval.Start{V: approval.DecisionVersion, Kind: approval.KindStart, World: ownerPub, Host: "ws", Session: "s1", Repo: repo.ID, Agent: "fake", Prompt: approval.TextHash("fix it")})
	start.Signature = &sig
	data, _ := json.Marshal(start)
	msg, err := nc.Request(v1.SessionCommandSubject("ws", "s1", v1.CommandStartSession), data, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	var reply v1.Reply
	_ = json.Unmarshal(msg.Data, &reply)
	if reply.Error != "" {
		t.Fatalf("StartSession: %s", reply.Error)
	}
	var req socket.RequestInfo
	for deadline := time.Now().Add(20 * time.Second); req.ID == ""; time.Sleep(50 * time.Millisecond) {
		if reqs, _ := cli.Requests(ctx); len(reqs) == 1 {
			req = reqs[0]
		}
		if time.Now().After(deadline) {
			t.Fatal("no request waited")
		}
	}

	// A wrong passphrase signs nothing.
	askSecret = func(context.Context, string) (string, error) { return "not it at all", nil }
	if code, _, errOut := run(t, "request", "approve", req.ID, "--socket", sock); code == 0 || !strings.Contains(errOut, "isn't the passphrase") {
		t.Errorf("approve with the wrong passphrase: code %d, %s", code, errOut)
	}
	askSecret = func(context.Context, string) (string, error) { return testPassphrase, nil }
	code, out, errOut := run(t, "request", "approve", req.ID, "--socket", sock)
	if code != 0 || !strings.Contains(out, "asks to run") || !strings.Contains(out, "Approved") {
		t.Fatalf("approve: code %d\n%s%s", code, out, errOut)
	}
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		h, _ := cli.History(ctx, "s1")
		if len(h) == 1 {
			if h[0].By != "local" || h[0].Approver != cliPub || h[0].Signature != "verified" {
				t.Errorf("the CLI's approval in the history = %+v", h[0])
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("history = %+v", h)
		}
	}

	// Rules from the host are signed over the list and its revision.
	if code, out, errOut := run(t, "rule", "add", repo.Name, "allow", "execute", "go test *", "--socket", sock); code != 0 || !strings.Contains(out, "Added rule 1") {
		t.Fatalf("rule add: code %d\n%s%s", code, out, errOut)
	}
	if code, out, errOut := run(t, "rule", "add", repo.Name, "deny", "execute", "rm -rf *", "--socket", sock); code != 0 || !strings.Contains(out, "Added rule 2") {
		t.Fatalf("a second rule add: code %d\n%s%s", code, out, errOut)
	}
	if code, out, errOut := run(t, "rule", "remove", repo.Name, "1", "--socket", sock); code != 0 || !strings.Contains(out, "1 rules remain") {
		t.Fatalf("rule remove: code %d\n%s%s", code, out, errOut)
	}
	list, err := cli.Rules(ctx)
	if err != nil || len(list) != 1 || len(list[0].Rules) != 1 || list[0].Rules[0].Decision != "deny" || list[0].By != "local" || list[0].Approver != cliPub {
		t.Errorf("rules after the CLI changed them = %+v, %v", list, err)
	}

	// Without its key, the CLI can't sign, and says how to become an
	// approver.
	if err := os.Remove(approverPath(ownerPub)); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := run(t, "rule", "add", repo.Name, "allow", "read", "--socket", sock); code == 0 || !strings.Contains(errOut, "skylands approver add") {
		t.Errorf("rule add without a key: code %d, %s", code, errOut)
	}
}
