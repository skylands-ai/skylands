package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/spf13/cobra"

	"github.com/skylands-ai/skylands/internal/themepack"
)

// ThemesRepoURL is where community theme packs are reviewed and kept.
const ThemesRepoURL = "https://github.com/skylands-ai/themes"

// webClient fetches packs and the themes index. Tests replace it.
var webClient = &http.Client{Timeout: time.Minute}

func newThemeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "theme",
		GroupID: groupWorld,
		Short:   "Find, check, add, list, and remove the theme packs a world's citadels offer",
		Long: `A theme pack draws the 3D world in a new look from data alone: a theme.json,
a models.glb of glTF models, and a LICENSE, with no code. Packs are kept in
the world's NATS, so every citadel offers each one in its World theme list,
and each browser picks its own.

Search lists the packs in the official themes repository,
` + ThemesRepoURL + `, and add takes one by its id:

  skylands theme search
  skylands theme add paper-lanterns

Add, list, and remove connect to the world's NATS server, as a citadel
does. Search and check connect to no world.`,
	}
	cmd.AddCommand(newThemeSearchCmd(), newThemeCheckCmd(), newThemeAddCmd(), newThemeListCmd(), newThemeRemoveCmd())
	return cmd
}

// worldFlags are how a theme command reaches the world's NATS server.
type worldFlags struct {
	natsURL string
	creds   string
}

func (w *worldFlags) add(cmd *cobra.Command) {
	cmd.Flags().StringVar(&w.natsURL, "nats-url", "nats://127.0.0.1:4222", "URL of the world's NATS server, which devworld runs at the default")
	cmd.Flags().StringVar(&w.creds, "creds", "", "path to NATS credentials that may use the world's JetStream, such as the citadel's")
}

func (w *worldFlags) jetstream() (jetstream.JetStream, func(), error) {
	opts := []nats.Option{nats.Name("skylands-theme"), nats.Timeout(5 * time.Second)}
	if w.creds != "" {
		opts = append(opts, nats.UserCredentials(w.creds))
	}
	nc, err := nats.Connect(w.natsURL, opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("connect to the world's NATS server at %s: %w", w.natsURL, err)
	}
	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, nil, err
	}
	return js, nc.Close, nil
}

func newThemeSearchCmd() *cobra.Command {
	var catalog string
	cmd := &cobra.Command{
		Use:   "search [words...]",
		Short: "List the theme packs in the official themes repository",
		Long: `Search lists the theme packs in the official themes repository,
` + ThemesRepoURL + `, whose CI checks each one as a citadel will.
Given words, it lists only the packs whose id, name, description, or
author has every one of them. Add one to the world by its id with
"skylands theme add <id>".`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), time.Minute)
			defer cancel()
			c, err := themepack.FetchCatalog(ctx, webClient, catalog)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			found := c.Matching(args)
			if len(found) == 0 {
				if len(args) == 0 {
					fmt.Fprintln(out, "The themes repository lists no packs yet.")
				} else {
					fmt.Fprintf(out, "No pack in the themes repository matches %q. Run skylands theme search for them all.\n", strings.Join(args, " "))
				}
				return nil
			}
			tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tNAME\tVERSION\tDESCRIPTION")
			for _, l := range found {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", l.ID, l.Name, l.Version, l.Description)
			}
			if err := tw.Flush(); err != nil {
				return err
			}
			fmt.Fprintln(out, "\nAdd one to the world with: skylands theme add <id>")
			return nil
		},
	}
	cmd.Flags().StringVar(&catalog, "catalog", themepack.CatalogURL, "URL of the themes index to read, such as a mirror's")
	return cmd
}

func newThemeCheckCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "check <pack directory or .zip>",
		Short: "Check a theme pack the way a citadel will, without adding it",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, others, err := themepack.Read(args[0])
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			describePack(out, p)
			if len(others) > 0 {
				fmt.Fprintf(out, "Not part of the pack, so left out: %s\n", strings.Join(others, ", "))
			}
			return nil
		},
	}
}

func describePack(out io.Writer, p *themepack.Pack) {
	m := p.Manifest
	fmt.Fprintf(out, "%s %s (%s), %s\n", m.Name, m.Version, m.ID, m.License)
	fmt.Fprintf(out, "%d triangles in %d bytes of models\n", p.Triangles, len(p.Files[themepack.ModelsFile]))
	fmt.Fprintf(out, "sha256 %s\n", p.Hash)
}

func newThemeAddCmd() *cobra.Command {
	var (
		world   worldFlags
		pin     string
		replace bool
		catalog string
	)
	cmd := &cobra.Command{
		Use:   "add <id, pack directory, .zip, or https:// URL of a .zip>",
		Short: "Check a theme pack and add it to the world",
		Long: `Add checks a theme pack, as check does, and stores it in the world, where
every citadel offers it. Name a pack the official themes repository lists,
as "skylands theme search" shows, by its id; add downloads it and refuses
it unless its files have the sha256 the repository lists. A directory or
zip here by the same name is read instead.

A pack is pinned by its sha256, which add prints and list shows. Pass
--sha256 to refuse a pack whose contents aren't the ones you expect.

A pack already in the world under the same id is never replaced unless
--replace says so, so an update is always a choice.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := readPack(cmd.Context(), args[0], catalog, cmd.OutOrStdout())
			if err != nil {
				return err
			}
			if pin != "" && !strings.EqualFold(pin, p.Hash) {
				return fmt.Errorf("the pack's sha256 is %s, not %s", p.Hash, pin)
			}
			js, done, err := world.jetstream()
			if err != nil {
				return err
			}
			defer done()
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()
			store, err := themepack.OpenStore(ctx, js)
			if err != nil {
				return fmt.Errorf("open the world's theme bucket: %w", err)
			}
			m := p.Manifest
			out := cmd.OutOrStdout()
			had, ok, err := themepack.Lookup(ctx, store, m.ID)
			if err != nil {
				return err
			}
			switch {
			case ok && had.Hash == p.Hash:
				fmt.Fprintf(out, "%s %s (%s) is already in the world, sha256 %s\n", m.Name, m.Version, m.ID, p.Hash)
				return nil
			case ok && !replace:
				return fmt.Errorf("the world already has %s %s as %s, sha256 %s; pass --replace to replace it with %s", had.Name, had.Version, m.ID, had.Hash, m.Version)
			}
			if err := themepack.Put(ctx, store, p); err != nil {
				return fmt.Errorf("store the pack: %w", err)
			}
			verb := "Added"
			if ok {
				verb = "Replaced " + had.Version + " with"
			}
			fmt.Fprintf(out, "%s %s %s (%s), sha256 %s\n", verb, m.Name, m.Version, m.ID, p.Hash)
			fmt.Fprintln(out, "Each browser picks it under World theme, on the citadel's page.")
			return nil
		},
	}
	world.add(cmd)
	cmd.Flags().StringVar(&pin, "sha256", "", "the sha256 the pack must have")
	cmd.Flags().BoolVar(&replace, "replace", false, "replace the pack already in the world under the same id")
	cmd.Flags().StringVar(&catalog, "catalog", themepack.CatalogURL, "URL of the themes index to find a pack's id in, such as a mirror's")
	return cmd
}

// readPack reads a pack from a directory, a zip archive, an https URL of
// one, or, by its id, the themes index at catalog.
func readPack(ctx context.Context, from, catalog string, out io.Writer) (*themepack.Pack, error) {
	u, err := url.Parse(from)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		if _, statErr := os.Stat(from); statErr != nil && themepack.IsID(from) {
			return fromCatalog(ctx, from, catalog, out)
		}
		p, _, err := themepack.Read(from)
		return p, err
	}
	if u.Scheme != "https" {
		return nil, fmt.Errorf("download packs over https, not %s", u.Scheme)
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, from, nil)
	if err != nil {
		return nil, err
	}
	resp, err := webClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download the pack: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download the pack: %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, themepack.MaxArchive+1))
	if err != nil {
		return nil, fmt.Errorf("download the pack: %w", err)
	}
	if len(data) > themepack.MaxArchive {
		return nil, fmt.Errorf("the download is over %d bytes, more than a pack may be", themepack.MaxArchive)
	}
	files, _, err := themepack.ReadZip(data)
	if err != nil {
		return nil, err
	}
	return themepack.Check(files)
}

// fromCatalog downloads the pack the themes index at catalog lists as id.
func fromCatalog(ctx context.Context, id, catalog string, out io.Writer) (*themepack.Pack, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	c, err := themepack.FetchCatalog(ctx, webClient, catalog)
	if err != nil {
		return nil, err
	}
	l, ok := c.Find(id)
	if !ok {
		return nil, fmt.Errorf("%q is neither a pack directory or zip here nor a pack in the themes repository; skylands theme search lists those", id)
	}
	fmt.Fprintf(out, "Downloading %s %s from the themes repository\n", l.Name, l.Version)
	return c.Fetch(ctx, webClient, l)
}

func newThemeListCmd() *cobra.Command {
	var world worldFlags
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the theme packs in the world",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			js, done, err := world.jetstream()
			if err != nil {
				return err
			}
			defer done()
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()
			out := cmd.OutOrStdout()
			store, err := themepack.ExistingStore(ctx, js)
			if err != nil {
				return err
			}
			var packs []themepack.Installed
			if store != nil {
				if packs, err = themepack.List(ctx, store); err != nil {
					return err
				}
			}
			if len(packs) == 0 {
				fmt.Fprintln(out, "No theme packs in the world. The built-in themes are Default and Sci-fi.")
				fmt.Fprintln(out, "Find packs to add with: skylands theme search")
				return nil
			}
			tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tNAME\tVERSION\tSHA256")
			for _, p := range packs {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", p.ID, p.Name, p.Version, p.Hash)
			}
			return tw.Flush()
		},
	}
	world.add(cmd)
	return cmd
}

func newThemeRemoveCmd() *cobra.Command {
	var world worldFlags
	cmd := &cobra.Command{
		Use:   "remove <id>",
		Short: "Remove a theme pack from the world",
		Long: `Remove takes a theme pack out of the world. Every citadel stops offering
it, and browsers drawing the world in it go back to Default.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			js, done, err := world.jetstream()
			if err != nil {
				return err
			}
			defer done()
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()
			id := args[0]
			store, err := themepack.ExistingStore(ctx, js)
			if err != nil {
				return err
			}
			var had themepack.Installed
			ok := false
			if store != nil {
				if had, ok, err = themepack.Lookup(ctx, store, id); err != nil {
					return err
				}
			}
			if !ok {
				return fmt.Errorf("the world has no theme pack %q", id)
			}
			if err := store.Delete(ctx, id); err != nil && !errors.Is(err, jetstream.ErrObjectNotFound) {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Removed %s %s (%s)\n", had.Name, had.Version, id)
			return nil
		},
	}
	world.add(cmd)
	return cmd
}
