package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/nats-io/nkeys"
	"github.com/spf13/cobra"

	v1 "github.com/skylands-ai/skylands/api/v1"
	"github.com/skylands-ai/skylands/internal/approval"
	"github.com/skylands-ai/skylands/internal/citadel/web"
	"github.com/skylands-ai/skylands/internal/daemon/socket"
)

// errNoTerminal means the CLI can't ask for a passphrase: it isn't running
// in a terminal a person types into.
var errNoTerminal = errors.New("signing needs the passphrase typed at a terminal, and this command isn't running in one")

// askSecret asks a person for a secret. Tests replace it.
var askSecret = readSecret

// approversDir is where this CLI keeps its approver keys, one file per
// world. Tests replace it.
var approversDir = func() string { return filepath.Join(defaultHome(), "approvers") }

// minPassphrase is the shortest passphrase an approver key takes.
const minPassphrase = 8

// approverFile is this CLI's approver key for one world. The public key
// is in the clear and the seed is sealed with a passphrase only the
// person knows: agents on this host run as the same user, so they can
// read the file, but they can't sign with it.
type approverFile struct {
	// Skylands is "cli-approver".
	Skylands  string          `json:"skylands"`
	Version   int             `json:"version"`
	World     string          `json:"world"`
	WorldName string          `json:"worldName,omitempty"`
	Key       string          `json:"key"`
	Name      string          `json:"name"`
	Citadel   string          `json:"citadel,omitempty"`
	Enrolled  string          `json:"enrolled"`
	Seed      approval.Sealed `json:"seed"`
}

func approverPath(world string) string { return filepath.Join(approversDir(), world+".json") }

// loadApprover reads this CLI's approver key for world.
func loadApprover(world string) (approverFile, error) {
	var f approverFile
	data, err := os.ReadFile(approverPath(world))
	if err != nil {
		return f, err
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return f, fmt.Errorf("read %s: %w", approverPath(world), err)
	}
	if f.Skylands != "cli-approver" || f.World != world || !nkeys.IsValidPublicUserKey(f.Key) {
		return f, fmt.Errorf("%s isn't this CLI's approver key for the world %s", approverPath(world), world)
	}
	return f, nil
}

// hostSigner signs what a person decides at this host, as the CLI's
// approver in the island's world.
type hostSigner struct {
	world, host string
	kp          nkeys.KeyPair
}

// signerFor finds the CLI's approver key for the world of the daemon cli
// talks to, and opens it with the passphrase the person types.
func signerFor(ctx context.Context, cli *socket.Client) (*hostSigner, error) {
	st, err := cli.Status(ctx)
	if err != nil {
		return nil, err
	}
	if st.WorldOwner == "" {
		return nil, errors.New("this island's world isn't set up yet, so nothing is decided on it; set the world up from the link the citadel printed")
	}
	f, err := loadApprover(st.WorldOwner)
	if errors.Is(err, os.ErrNotExist) {
		return nil, errors.New(`this CLI isn't an approver in this island's world, so it can't sign what you decide here; ask an approver for an invite link (Settings, then Approvers, then "Add an approver") and run: skylands approver add <link>`)
	}
	if err != nil {
		return nil, err
	}
	pass, err := askSecret(ctx, fmt.Sprintf("Passphrase for %s: ", f.Name))
	if err != nil {
		return nil, err
	}
	seed, err := f.Seed.Open(pass)
	if errors.Is(err, approval.ErrPassphrase) {
		return nil, errors.New("that isn't the passphrase of this CLI's approver key")
	}
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", approverPath(st.WorldOwner), err)
	}
	kp, err := nkeys.FromSeed(seed)
	clear(seed)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", approverPath(st.WorldOwner), err)
	}
	if pub, _ := kp.PublicKey(); pub != f.Key {
		return nil, fmt.Errorf("the key in %s doesn't match the public key it names", approverPath(st.WorldOwner))
	}
	return &hostSigner{world: st.WorldOwner, host: st.Host, kp: kp}, nil
}

func (s *hostSigner) sign(d approval.Decision) (*v1.DecisionSignature, error) {
	sig, err := approval.SignDecision(s.kp, d)
	if err != nil {
		return nil, err
	}
	return &sig, nil
}

func newApproverCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "approver",
		GroupID: groupLocal,
		Short:   "Make this host's CLI an approver, so it can sign what you decide here",
		Long: `Every decision a daemon acts on is signed by one of the world's approvers.
A browser signs what you decide in the world; the CLI on a host signs what
you decide with request approve, deny, and answer, and rule add and remove,
once it is an approver itself. It becomes one with an invite link, the
same way a browser does.

The CLI keeps its key encrypted with a passphrase you choose, and asks for
it each time it signs. Agents on this host run as you, so they can read
the key's file, but they can't sign with it.`,
	}
	cmd.AddCommand(newApproverAddCmd(), newApproverListCmd())
	return cmd
}

func newApproverAddCmd() *cobra.Command {
	var socketPath, name string
	cmd := &cobra.Command{
		Use:   "add <invite link>",
		Short: "Make this host's CLI an approver with an invite link",
		Long: `Approver add makes this host's CLI an approver in the world an invite link
is for. An approver makes the link in the world's Settings, under
Approvers, with "Add an approver", the same link that adds a browser. It
works once, within 15 minutes.

The command asks twice for a passphrase of at least 8 characters, which
encrypts the CLI's key. Every command that signs asks for it again. The
CLI is then listed among the world's approvers, and revoking it there
ends what it approved in advance, as it does for a browser.`,
		Example: `  skylands approver add 'https://skylands.example.com/setup#approver=SUAB...'
  skylands approver add --name "CLI on the build box" 'http://127.0.0.1:8080/setup#approver=SUAB...'`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return addApprover(cmd.Context(), cmd.OutOrStdout(), socket.Dial(socketPath), args[0], name)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", `what the world's approvers list calls this CLI (default "CLI on <island>")`)
	socketFlag(cmd, &socketPath)
	return cmd
}

// inviteLink is what an invite link names: the citadel it came from and
// the voucher's claim key, whose seed the link carries after #approver=.
type inviteLink struct {
	citadel string
	claim   nkeys.KeyPair
}

func parseInvite(raw string) (inviteLink, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return inviteLink{}, fmt.Errorf("%q isn't an invite link; copy the whole link an approver made", raw)
	}
	seed, ok := strings.CutPrefix(u.Fragment, "approver=")
	if !ok || seed == "" {
		return inviteLink{}, errors.New("that link names no invite; copy the whole link, including what follows #approver=")
	}
	kp, err := nkeys.FromSeed([]byte(seed))
	if err != nil {
		return inviteLink{}, fmt.Errorf("that link's invite is damaged: %w", err)
	}
	if pub, _ := kp.PublicKey(); !nkeys.IsValidPublicUserKey(pub) {
		return inviteLink{}, errors.New("that link's invite isn't an approver invite")
	}
	return inviteLink{citadel: u.Scheme + "://" + u.Host, claim: kp}, nil
}

func addApprover(ctx context.Context, out io.Writer, cli *socket.Client, link, name string) error {
	inv, err := parseInvite(link)
	if err != nil {
		return err
	}
	claim, _ := inv.claim.PublicKey()
	hc := &http.Client{Timeout: 15 * time.Second}
	var world web.WorldInfo
	if err := citadelCall(ctx, hc, http.MethodGet, inv.citadel+"/api/world", nil, &world); err != nil {
		return err
	}
	if !world.Ready || world.Owner == "" {
		return fmt.Errorf("the world at %s isn't set up yet", inv.citadel)
	}
	if f, err := loadApprover(world.Owner); err == nil {
		return fmt.Errorf("this CLI is already an approver in %s, as %s; to replace it, revoke it from the approvers list and remove %s", worldName(world), f.Name, approverPath(world.Owner))
	}
	var voucher web.VoucherInfo
	if err := citadelCall(ctx, hc, http.MethodGet, inv.citadel+"/api/world/vouchers/"+url.PathEscape(claim), nil, &voucher); err != nil {
		return err
	}
	switch {
	case voucher.UsedBy != "":
		return errors.New("that link was already used; ask an approver for a new one")
	case voucher.Expired:
		return errors.New("that link has expired; ask an approver for a new one")
	case !voucher.Valid:
		return errors.New("the approver who made that link no longer approves; ask another approver for a new one")
	}
	// The island's daemon decides in one world, and a key for another is
	// no use to it.
	island := ""
	if st, err := cli.Status(ctx); err == nil {
		if st.WorldOwner != "" && st.WorldOwner != world.Owner {
			return fmt.Errorf("the daemon on this host belongs to another world than the link's (%s, not %s)", st.WorldOwner, world.Owner)
		}
		island = st.Host
	}
	if island == "" {
		island, _ = os.Hostname()
	}
	if name == "" {
		name = "CLI on " + island
	}
	if strings.TrimSpace(name) == "" || len(name) > approval.MaxNameLength {
		return fmt.Errorf("give a name of at most %d bytes", approval.MaxNameLength)
	}
	fmt.Fprintf(out, "%s invited this CLI to approve what agents do in %s. It will be listed as %q.\n", voucher.ByName, worldName(world), name)
	fmt.Fprintln(out, "Choose a passphrase for its key. You'll type it each time the CLI signs.")
	pass, err := askSecret(ctx, "Passphrase: ")
	if err != nil {
		return err
	}
	if len(pass) < minPassphrase {
		return fmt.Errorf("the passphrase needs at least %d characters", minPassphrase)
	}
	again, err := askSecret(ctx, "Passphrase again: ")
	if err != nil {
		return err
	}
	if again != pass {
		return errors.New("the two passphrases differ")
	}

	kp, err := nkeys.CreateUser()
	if err != nil {
		return err
	}
	pub, _ := kp.PublicKey()
	seed, _ := kp.Seed()
	sealed, err := approval.Seal(seed, pass)
	clear(seed)
	if err != nil {
		return err
	}
	now := approval.Now()
	entry, err := approval.Sign(inv.claim, v1.ApproverEntry{Type: "approver", World: world.Owner, Key: pub, Name: name, Created: now, Voucher: claim, Device: v1.ApproverDeviceCLI})
	if err != nil {
		return err
	}
	file := approverFile{Skylands: "cli-approver", Version: 1, World: world.Owner, WorldName: world.Name, Key: pub, Name: name, Citadel: inv.citadel, Enrolled: now, Seed: sealed}
	// The key is written before the citadel stores the entry, so an
	// approver is never enrolled without a key to sign with. It takes its
	// own name only once the citadel has.
	tmp, err := writeApprover(file)
	if err != nil {
		return err
	}
	if err := citadelCall(ctx, hc, http.MethodPost, inv.citadel+"/api/world/approvers", map[string]v1.Signed{"approver": entry}, nil); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, approverPath(world.Owner)); err != nil {
		return fmt.Errorf("the CLI is an approver, but its key couldn't be kept: %w; revoke %q from the approvers list", err, name)
	}
	fmt.Fprintf(out, "This CLI is now an approver in %s, listed as %q. Its key is in %s.\n", worldName(world), name, approverPath(world.Owner))
	return nil
}

// writeApprover writes f beside where it belongs, readable only by this
// user, and returns where.
func writeApprover(f approverFile) (string, error) {
	dir := approversDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, f.World+".*.tmp")
	if err != nil {
		return "", err
	}
	_, werr := tmp.Write(append(data, '\n'))
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr, os.Chmod(tmp.Name(), 0o600)); err != nil {
		_ = os.Remove(tmp.Name())
		return "", err
	}
	return tmp.Name(), nil
}

func worldName(w web.WorldInfo) string {
	if w.Name != "" {
		return w.Name
	}
	return "the world"
}

// citadelCall makes one call to a citadel's API, decoding its answer into
// out when out isn't nil, and its error message when it refuses.
func citadelCall(ctx context.Context, hc *http.Client, method, u string, in, out any) error {
	var body io.Reader
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("reach the citadel: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		if json.NewDecoder(resp.Body).Decode(&e) == nil && e.Error != "" {
			return fmt.Errorf("the citadel says: %s", e.Error)
		}
		return fmt.Errorf("the citadel replied %s", resp.Status)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func newApproverListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the worlds this host's CLI is an approver in",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			paths, _ := filepath.Glob(filepath.Join(approversDir(), "*.json"))
			var files []approverFile
			for _, p := range paths {
				world := strings.TrimSuffix(filepath.Base(p), ".json")
				if f, err := loadApprover(world); err == nil {
					files = append(files, f)
				}
			}
			if len(files) == 0 {
				fmt.Fprintln(out, "This CLI isn't an approver in any world. Make it one with: skylands approver add <invite link>")
				return nil
			}
			sort.Slice(files, func(i, j int) bool { return files[i].Enrolled < files[j].Enrolled })
			tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "WORLD\tNAME\tKEY\tCITADEL")
			for _, f := range files {
				w := f.WorldName
				if w == "" {
					w = f.World
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", w, f.Name, f.Key, f.Citadel)
			}
			return tw.Flush()
		},
	}
}
