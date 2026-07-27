package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/abyssmemes/contextverse/internal/config"
	"github.com/abyssmemes/contextverse/internal/editor"
	"github.com/abyssmemes/contextverse/internal/logx"
	"github.com/abyssmemes/contextverse/internal/plugins"
	"github.com/abyssmemes/contextverse/internal/prompt"
	"github.com/abyssmemes/contextverse/internal/space"
	"github.com/abyssmemes/contextverse/internal/storage"
	"github.com/abyssmemes/contextverse/internal/syncclient"
	templatepkg "github.com/abyssmemes/contextverse/internal/template"
)

// runInitWizard is what `contextd init` does with no subcommand. It used to
// print the help text, which left a freshly installed user staring at three
// mode names with no way to tell which one they wanted.
func runInitWizard(cmd *cobra.Command) error {
	if !prompt.Interactive() {
		return fmt.Errorf("init needs a terminal; run contextd init solo|client|server (add --non-interactive for CI)")
	}
	out := cmd.OutOrStdout()
	fmt.Fprintln(out, wizardBanner())

	mode, err := prompt.Select(
		"How do you want to use ContextVerse?",
		"One binary, three modes. You can change this later with contextd init --reconfigure.",
		[]prompt.Choice{
			{
				ID:    "solo",
				Label: "Solo — just me, on this machine",
				Desc:  "A local context space at ~/.context. No server, no account, nothing leaves this machine. Add a git backend later if you want off-site backup.",
			},
			{
				ID:    "client",
				Label: "Client — join a team's server",
				Desc:  "Sync a space from a server someone already runs. You need its URL and an API token from whoever administers it.",
			},
			{
				ID:    "server",
				Label: "Server — host a space for a team",
				Desc:  "Run the server on this machine: spaces, users, path ACL, audit. Opens a setup page in your browser.",
			},
		}, 0)
	if err != nil {
		return wizardErr(err)
	}

	switch mode {
	case 0:
		return runSoloWizard(cmd)
	case 1:
		return runClientWizard(cmd)
	default:
		fmt.Fprintln(out, "\nStarting the server setup UI — run `contextd init server --noui` if you prefer the terminal.")
		srv := newInitServerCmd()
		srv.SetOut(cmd.OutOrStdout())
		srv.SetErr(cmd.ErrOrStderr())
		srv.SetIn(cmd.InOrStdin())
		srv.SetArgs(nil)
		return srv.ExecuteContext(cmd.Context())
	}
}

func wizardBanner() string {
	return "\nContextVerse setup\n"
}

// runReconfigure edits an existing installation. Everything here is a change to
// something already chosen, so each section reuses the picker the wizard asked
// with — the answer looks the same the second time as the first.
func runReconfigure(cmd *cobra.Command) error {
	if !prompt.Interactive() {
		return fmt.Errorf("--reconfigure needs a terminal; edit config.yaml directly, or use contextd backend/plugin/user for scripted changes")
	}
	root, err := resolveSpaceRoot()
	if err != nil {
		return err
	}
	if !config.Exists(root) {
		return fmt.Errorf("nothing to reconfigure: no space at %s — run contextd init", root)
	}
	out := cmd.OutOrStdout()

	for {
		cfg, err := config.Load(root)
		if err != nil {
			return err
		}
		choices := []prompt.Choice{
			{ID: "identity", Label: "Identity", Desc: identitySummary(cfg)},
			{ID: "plugins", Label: "AI tools", Desc: "Re-detect installed AI clients and re-wire which ones read this space."},
			{ID: "backend", Label: "Storage backend", Desc: "Currently " + orDefault(cfg.Backend.Driver, "local") + ". Switch between local and a git remote."},
			{ID: "editor", Label: "Default editor", Desc: editorSummary(cfg)},
		}
		if cfg.Mode == config.ModeClient {
			choices = append(choices, prompt.Choice{
				ID:    "server",
				Label: "Server connection",
				Desc:  "Currently " + cfg.Server.URL + " (space " + cfg.Server.Space + "). Change URL, token or space.",
			})
		}
		choices = append(choices, prompt.Choice{ID: "done", Label: "Done", Desc: "Leave everything else as it is."})

		i, err := prompt.Select("What do you want to change?",
			"Mode is "+string(cfg.Mode)+", space is "+root+".", choices, 0)
		if err != nil {
			if errors.Is(err, prompt.ErrCancelled) {
				return nil
			}
			return err
		}

		switch choices[i].ID {
		case "done":
			fmt.Fprintln(out, "\nDone. contextd status shows the result.")
			return nil
		case "identity":
			if err := reconfigureIdentity(cmd, root, cfg); err != nil {
				return wizardErr(err)
			}
		case "plugins":
			if err := wizardPlugins(cmd, root); err != nil {
				return wizardErr(err)
			}
		case "backend":
			driver, remote, err := wizardBackend()
			if err != nil {
				return wizardErr(err)
			}
			cfg.Backend = config.Backend{Driver: driver}
			if driver == "git" {
				cfg.Backend.GitRemote = remote
				cfg.Backend.GitAutoPush = true
			}
			if err := config.Save(cfg); err != nil {
				return err
			}
			logx.L().Info("reconfigured backend", "driver", driver)
			fmt.Fprintf(out, "  ✅ backend is now %s\n", driver)
		case "editor":
			if err := reconfigureEditor(cmd, cfg); err != nil {
				return wizardErr(err)
			}
		case "server":
			if err := reconfigureServer(cmd, root, cfg); err != nil {
				return wizardErr(err)
			}
		}
	}
}

func identitySummary(cfg *config.Config) string {
	who := cfg.Identity.Name
	if who == "" {
		who = "(unset)"
	}
	if cfg.Identity.Role != "" {
		who += ", " + cfg.Identity.Role
	}
	return "Currently " + who + ". Changes config.yaml; identity/me.md stays yours."
}

func editorSummary(cfg *config.Config) string {
	if cfg.Editor == "" {
		return "Not set — the TUI asks each time, or follows $EDITOR."
	}
	return "Currently " + cfg.Editor + "."
}

// reconfigureIdentity updates the recorded identity but will not rewrite
// identity/me.md: that file is seeded once and then belongs to the user, and
// regenerating it from a template would silently delete whatever they wrote.
func reconfigureIdentity(cmd *cobra.Command, root string, cfg *config.Config) error {
	name, err := prompt.Text("Your name", "Recorded in config.yaml.", cfg.Identity.Name)
	if err != nil {
		return err
	}
	role, err := prompt.Text("Your role", "Recorded in config.yaml.", cfg.Identity.Role)
	if err != nil {
		return err
	}
	language, err := wizardLanguage()
	if err != nil {
		return err
	}
	cfg.Identity = config.Identity{Name: name, Role: role, Language: language}
	if err := config.Save(cfg); err != nil {
		return err
	}
	logx.L().Info("reconfigured identity", "name", name)
	fmt.Fprintln(cmd.OutOrStdout(), "  ✅ identity updated in config.yaml")

	open, err := prompt.Confirm("Open identity/me.md as well?",
		"That file is what the AI actually reads, and it is yours — contextd will not rewrite it from a template.", false)
	if err != nil || !open {
		return nil
	}
	ed, err := resolveEditor("")
	if err != nil {
		return err
	}
	fl, err := openFileLog()
	if err != nil {
		return err
	}
	data, opened, err := currentBody(cmd, fl, "identity/me.md")
	if err != nil {
		return err
	}
	edited, changed, err := editor.Session(ed, "identity/me.md", data)
	if err != nil {
		return err
	}
	if !changed {
		fmt.Fprintln(cmd.OutOrStdout(), "  no changes to identity/me.md")
		return nil
	}
	next, err := commitBody(cmd, fl, "identity/me.md", edited, opened)
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "  ✅ identity/me.md → %s\n", storage.DisplayVersion(next))
	return nil
}

func reconfigureEditor(cmd *cobra.Command, cfg *config.Config) error {
	found := editor.Detect()
	if len(found) == 0 {
		return fmt.Errorf("no editor found in PATH")
	}
	choices := make([]prompt.Choice, 0, len(found)+1)
	for _, e := range found {
		desc := "Runs in this terminal."
		if !e.Terminal {
			desc = "Opens a separate window; contextd waits for the file to close."
		}
		choices = append(choices, prompt.Choice{ID: e.ID, Label: e.Name, Desc: desc})
	}
	choices = append(choices, prompt.Choice{
		ID: "", Label: "No preference", Desc: "Follow $VISUAL/$EDITOR, and ask when neither is set.",
	})
	i, err := prompt.Select("Default editor", "Used by the TUI and by contextd file edit.", choices, 0)
	if err != nil {
		return err
	}
	cfg.Editor = choices[i].ID
	if err := config.Save(cfg); err != nil {
		return err
	}
	logx.L().Info("reconfigured editor", "editor", cfg.Editor)
	fmt.Fprintf(cmd.OutOrStdout(), "  ✅ editor preference saved\n")
	return nil
}

// reconfigureServer changes where a client syncs from, verifying the new
// credentials before committing them — the same order the join wizard uses.
func reconfigureServer(cmd *cobra.Command, root string, cfg *config.Config) error {
	url, err := prompt.Text("Server URL", "Where contextd pulls from and pushes to.", cfg.Server.URL)
	if err != nil {
		return err
	}
	token, err := prompt.Text("API token", "Leave empty to keep the token already stored.", "")
	if err != nil {
		return err
	}

	previous, _ := syncclient.ReadToken(cfg)
	if strings.TrimSpace(token) != "" {
		if err := syncclient.WriteToken(root, token); err != nil {
			return err
		}
	}

	probe := &config.Config{Mode: config.ModeClient, SpaceRoot: root, Server: config.ClientServer{URL: url}}
	client, err := syncclient.NewFromConfig(probe)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
	defer cancel()
	user, role, err := client.WhoAmI(ctx)
	if err != nil {
		// Put the working token back rather than leaving the client unable to sync
		// because a reconfigure attempt was wrong.
		if strings.TrimSpace(token) != "" && previous != "" {
			if rerr := syncclient.WriteToken(root, previous); rerr != nil {
				logx.L().Error("restore previous token", "err", rerr)
			}
		}
		return fmt.Errorf("could not authenticate against %s (previous settings kept): %w", url, err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "  ✅ authenticated as %s (%s)\n", user, role)

	spaceName, err := wizardSpaceChoice(ctx, client)
	if err != nil {
		return err
	}
	cfg.Server.URL = url
	cfg.Server.Space = spaceName
	cfg.Sync.LastHead = "" // the new space has its own history
	if err := config.Save(cfg); err != nil {
		return err
	}
	logx.L().Info("reconfigured server", "url", url, "space", spaceName)
	fmt.Fprintf(cmd.OutOrStdout(), "  ✅ now syncing %s from %s — run contextd pull\n", spaceName, url)
	return nil
}

// wizardErr turns a cancelled prompt into a calm exit rather than a stack of
// error noise — quitting a setup wizard is a normal thing to do.
func wizardErr(err error) error {
	if errors.Is(err, prompt.ErrCancelled) {
		return fmt.Errorf("setup cancelled — nothing was written")
	}
	return err
}

func runSoloWizard(cmd *cobra.Command) error {
	out := cmd.OutOrStdout()
	root, err := resolveSpaceRoot()
	if err != nil {
		return err
	}
	if config.Exists(root) {
		ok, err := prompt.Confirm(
			"A space already exists at "+root,
			"Continuing rewrites config.yaml and re-seeds template files. identity/me.md is kept.",
			false)
		if err != nil {
			return wizardErr(err)
		}
		if !ok {
			return fmt.Errorf("setup cancelled — existing space at %s left alone", root)
		}
	}

	// --- identity -----------------------------------------------------------
	name, err := prompt.Text("Your name", "Goes into identity/me.md — the file every AI reads to know who it is talking to.", "")
	if err != nil {
		return wizardErr(err)
	}
	role, err := prompt.Text("Your role", "One line. \"DevOps engineer\", \"Backend developer\" — it shapes how the AI pitches answers.", "")
	if err != nil {
		return wizardErr(err)
	}
	language, err := wizardLanguage()
	if err != nil {
		return wizardErr(err)
	}
	tools, err := prompt.Text("Tools you work with", "Comma-separated, e.g. Go, Terraform, Kubernetes. Skip with enter.", "")
	if err != nil {
		return wizardErr(err)
	}

	// --- template -----------------------------------------------------------
	templateName, err := wizardTemplate(out)
	if err != nil {
		return wizardErr(err)
	}

	// --- backend ------------------------------------------------------------
	backend, gitRemote, err := wizardBackend()
	if err != nil {
		return wizardErr(err)
	}

	// --- build --------------------------------------------------------------
	fmt.Fprintln(out, "\nCreating your context space…")
	if err := createSoloSpace(cmd, root, soloSetup{
		Name: name, Role: role, Language: language, Tools: tools,
		Template: templateName, Backend: backend, GitRemote: gitRemote,
	}); err != nil {
		return err
	}

	// --- AI clients ---------------------------------------------------------
	if err := wizardPlugins(cmd, root); err != nil {
		// Wiring an editor integration is not worth losing a finished space over.
		logx.L().Warn("wizard plugin step", "err", err)
		fmt.Fprintf(cmd.ErrOrStderr(), "AI client wiring skipped: %v\n", err)
	}

	printSpaceMap(out, root, "solo")
	return nil
}

// soloSetup is everything the wizard and the flag-driven `init solo` both need,
// so the two paths build a space the same way instead of drifting apart.
type soloSetup struct {
	Name            string
	Role            string
	Language        string
	Tools           string
	Template        string
	TemplatePath    string
	RefreshTemplate bool
	Force           bool
	Backend         string
	GitRemote       string
	Quiet           bool // flag path prints its own summary
}

func createSoloSpace(cmd *cobra.Command, root string, s soloSetup) error {
	if s.Language == "" {
		s.Language = "English"
	}
	if s.Template == "" {
		s.Template = "solo-default"
	}
	if err := space.Create(space.CreateOptions{
		SpaceRoot:       root,
		TemplateName:    s.Template,
		TemplatePath:    s.TemplatePath,
		RefreshTemplate: s.RefreshTemplate,
		Identity: space.IdentityFields{
			Name: s.Name, Role: s.Role, Language: s.Language, Tools: s.Tools,
		},
		Force: s.Force,
	}); err != nil {
		return err
	}
	if err := space.UpdateIndex(root); err != nil {
		return err
	}
	cfg := &config.Config{
		Mode:      config.ModeSolo,
		SpaceRoot: root,
		Identity: config.Identity{
			Name: s.Name, Role: s.Role, Language: s.Language,
		},
		Template: s.Template,
		Backend:  config.Backend{Driver: "local"},
	}
	if s.TemplatePath != "" {
		cfg.Template = s.TemplatePath
	}
	if s.Backend == "git" && s.GitRemote != "" {
		cfg.Backend = config.Backend{Driver: "git", GitRemote: s.GitRemote, GitAutoPush: true}
	}
	if err := config.Save(cfg); err != nil {
		return err
	}

	// Record the seeded tree as version 1 of each file.
	//
	// The template is written straight to disk, so without this the version log
	// knows nothing about the files the user actually starts with: `file list`
	// reported "(no files)" on a fresh space with eleven Markdown files in it,
	// `file history` was empty for every one of them, and the Files tab in the
	// TUI and the local console showed nothing. History only began at the first
	// write through contextd, which is not when the content began.
	seeded, err := seedFileLog(cmd, root)
	if err != nil {
		// A space that exists but has no baseline history is still usable, so
		// this warns rather than unwinding a successful creation.
		logx.L().Warn("seed version history", "space_root", root, "err", err)
	}

	logx.L().Info("solo space created", "space_root", root, "template", cfg.Template, "backend", cfg.Backend.Driver, "seeded_files", seeded)
	if s.Quiet {
		return nil
	}
	fmt.Fprintf(cmd.OutOrStdout(), "  ✅ space seeded from %s\n", cfg.Template)
	if cfg.Backend.Driver == "git" {
		fmt.Fprintf(cmd.OutOrStdout(), "  ✅ git backend → %s\n", s.GitRemote)
	}
	return nil
}

func runClientWizard(cmd *cobra.Command) error {
	out := cmd.OutOrStdout()
	root, err := resolveSpaceRoot()
	if err != nil {
		return err
	}
	if config.Exists(root) {
		ok, err := prompt.Confirm("A space already exists at "+root,
			"Continuing replaces its config and pulls the server's copy over it.", false)
		if err != nil {
			return wizardErr(err)
		}
		if !ok {
			return fmt.Errorf("setup cancelled — existing space at %s left alone", root)
		}
	}

	url, err := prompt.Text("Server URL",
		"Where your team's contextd server listens, e.g. https://context.my-team.com. Ask whoever runs it.",
		"http://127.0.0.1:8743")
	if err != nil {
		return wizardErr(err)
	}
	token, err := prompt.Text("API token",
		"Issued by an admin with contextd user reset-token <you>. It is stored in the space directory, readable only by you.", "")
	if err != nil {
		return wizardErr(err)
	}
	if strings.TrimSpace(token) == "" {
		return fmt.Errorf("a token is required to join a server")
	}

	// Verify before writing anything: a typo here used to be discovered only
	// after the config had already been saved.
	fmt.Fprintln(out, "\nChecking the server…")
	probe := &config.Config{Mode: config.ModeClient, SpaceRoot: root, Server: config.ClientServer{URL: url}}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	if err := syncclient.WriteToken(root, token); err != nil {
		return err
	}
	client, err := syncclient.NewFromConfig(probe)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
	defer cancel()
	user, role, err := client.WhoAmI(ctx)
	if err != nil {
		return fmt.Errorf("could not authenticate against %s: %w", url, err)
	}
	fmt.Fprintf(out, "  ✅ authenticated as %s (%s)\n", user, role)

	spaceName, err := wizardSpaceChoice(ctx, client)
	if err != nil {
		return wizardErr(err)
	}

	name, err := prompt.Text("Your name", "Goes into identity/me.md. Identity is yours and is not pushed over the team's copy.", user)
	if err != nil {
		return wizardErr(err)
	}
	language, err := wizardLanguage()
	if err != nil {
		return wizardErr(err)
	}

	cfg := &config.Config{
		Mode:      config.ModeClient,
		SpaceRoot: root,
		Identity:  config.Identity{Name: name, Language: language},
		Server:    config.ClientServer{URL: url, Space: spaceName},
	}
	if err := config.Save(cfg); err != nil {
		return err
	}

	fmt.Fprintln(out, "\nPulling the space…")
	res, err := clientInitialPull(ctx, cfg, root)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "  ✅ %d file(s) at head %s\n", res, cfg.Sync.LastHead)

	if err := wizardPlugins(cmd, root); err != nil {
		logx.L().Warn("wizard plugin step", "err", err)
		fmt.Fprintf(cmd.ErrOrStderr(), "AI client wiring skipped: %v\n", err)
	}

	if err := wizardBackgroundSync(cmd, cfg); err != nil {
		logx.L().Warn("wizard background sync step", "err", err)
		fmt.Fprintf(cmd.ErrOrStderr(), "background sync setup skipped: %v\n", err)
	}

	printSpaceMap(out, root, "client")
	fmt.Fprintf(out, "  contextd pull / contextd push        sync with %s\n", url)
	return nil
}

// wizardBackgroundSync offers the sync daemon at the one moment the question
// makes sense. It existed for a while with no way to discover it: nothing in
// setup mentioned it, and nothing installed it, so a client that had been
// running fine went stale the moment its terminal closed.
func wizardBackgroundSync(cmd *cobra.Command, cfg *config.Config) error {
	choices := []prompt.Choice{
		{
			ID:    "install",
			Label: "Yes — keep it current in the background",
			Desc:  "Installs a per-user service that starts at login and pulls when the server moves. Never pushes: publishing stays an explicit contextd push.",
			Note:  "recommended",
		},
		{
			ID:    "session",
			Label: "Just for this session",
			Desc:  "Runs until you close the terminal or reboot. Start it again later with contextd daemon start.",
		},
		{
			ID:    "manual",
			Label: "No — I'll pull myself",
			Desc:  "contextd activate soft-pulls when you enter a project, and contextd pull is always there. Set it up later with contextd daemon install.",
		},
	}
	if !serviceSupported() {
		// Offering to install a service this platform has no template for would
		// be a promise the next screen breaks.
		choices = choices[1:]
	}

	i, err := prompt.Select("Keep this machine in sync automatically?",
		"The daemon polls the server and pulls when someone else publishes.", choices, 0)
	if err != nil {
		if errors.Is(err, prompt.ErrCancelled) {
			return nil
		}
		return err
	}

	out := cmd.OutOrStdout()
	switch choices[i].ID {
	case "install":
		bin, err := os.Executable()
		if err != nil {
			return err
		}
		abs, err := filepath.Abs(cfg.SpaceRoot)
		if err != nil {
			return err
		}
		path, err := installService(bin, abs, daemonInterval(cfg))
		if err != nil {
			return err
		}
		if err := startService(); err != nil {
			fmt.Fprintf(out, "  ✅ autostart installed → %s\n", path)
			fmt.Fprintf(cmd.ErrOrStderr(), "  (not started yet: %v — start with %s)\n", err, startHint())
			return nil
		}
		fmt.Fprintf(out, "  ✅ background sync running, and will start at login\n")
	case "session":
		fmt.Fprintln(out, "  Start it with: contextd daemon start")
	default:
		fmt.Fprintln(out, "  No background sync. contextd pull when you want the latest.")
	}
	return nil
}

// wizardSpaceChoice turns the server's space list into a picker. Falls back to
// typing a name only when the listing is unavailable.
func wizardSpaceChoice(ctx context.Context, client *syncclient.Client) (string, error) {
	spaces, err := client.ListSpaces(ctx)
	if err != nil || len(spaces) == 0 {
		if err != nil {
			logx.L().Warn("list spaces", "err", err)
		}
		return prompt.Text("Space name", "The server did not return a listing for this token — type the name you were given.", "team")
	}
	if len(spaces) == 1 {
		return spaces[0].Name, nil
	}
	choices := make([]prompt.Choice, 0, len(spaces))
	for _, s := range spaces {
		desc := "Space on this server."
		if s.Head != "" {
			desc = "Current head: " + s.Head
		}
		choices = append(choices, prompt.Choice{ID: s.Name, Label: s.Name, Desc: desc})
	}
	i, err := prompt.Select("Which space?", "These are the spaces your token can see.", choices, 0)
	if err != nil {
		return "", err
	}
	return choices[i].ID, nil
}

// clientInitialPull performs the first sync and records the head.
func clientInitialPull(ctx context.Context, cfg *config.Config, root string) (int, error) {
	client, err := syncclient.NewFromConfig(cfg)
	if err != nil {
		return 0, err
	}
	meta, err := client.GetSpace(ctx)
	if err != nil {
		return 0, err
	}
	syncCfg := syncclient.ParseSync(meta)
	st, err := syncclient.LoadState(root)
	if err != nil {
		return 0, err
	}
	res, err := client.Pull(ctx, root, "", syncCfg, st, false)
	if err != nil {
		return 0, fmt.Errorf("initial pull: %w", err)
	}
	if err := syncclient.SaveState(root, st); err != nil {
		return 0, err
	}
	cfg.Sync.LastHead = res.Head
	cfg.Sync.LastSyncAt = time.Now().UTC()
	if err := config.Save(cfg); err != nil {
		return 0, err
	}
	return res.Updated, nil
}

func wizardLanguage() (string, error) {
	choices := []prompt.Choice{
		{ID: "English", Label: "English", Desc: "The AI answers you in English."},
		{ID: "Russian", Label: "Русский", Desc: "The AI answers you in Russian."},
		{ID: "Hebrew", Label: "עברית", Desc: "The AI answers you in Hebrew."},
		{ID: "other", Label: "Something else…", Desc: "Type the language name yourself."},
	}
	i, err := prompt.Select("Preferred language", "Recorded in identity/me.md as the language you want answers in.", choices, 0)
	if err != nil {
		return "", err
	}
	if choices[i].ID != "other" {
		return choices[i].ID, nil
	}
	return prompt.Text("Language", "Written into identity/me.md as-is.", "English")
}

// wizardTemplate offers the template catalog. Templates decide the starting
// shape of the space, so this was the worst thing to leave unasked: init silently
// used solo-default and never mentioned that a catalog existed.
func wizardTemplate(out io.Writer) (string, error) {
	fmt.Fprintln(out, "\nFetching the template catalog…")
	entries, err := templatepkg.List("", "", nil)
	if err != nil {
		logx.L().Warn("template catalog unavailable", "err", err)
		fmt.Fprintf(out, "Catalog unavailable (%v) — using the built-in solo-default.\n", err)
		return "solo-default", nil
	}

	choices := make([]prompt.Choice, 0, len(entries)+1)
	for _, e := range entries {
		desc := e.Description
		if desc == "" {
			desc = "Template from the public catalog (github.com/" + templatepkg.DefaultRepo + ")."
		}
		note := ""
		if e.Name == "solo-default" {
			note = "recommended"
		}
		choices = append(choices, prompt.Choice{ID: e.Name, Label: e.Name, Desc: desc, Note: note})
	}
	if len(choices) == 0 {
		return "solo-default", nil
	}
	initial := 0
	for i, c := range choices {
		if c.ID == "solo-default" {
			initial = i
			break
		}
	}
	i, err := prompt.Select("Starting template",
		"Decides which files your space begins with. You can re-seed later with contextd space seed --template <name>.",
		choices, initial)
	if err != nil {
		return "", err
	}
	return choices[i].ID, nil
}

func wizardBackend() (driver, gitRemote string, err error) {
	i, err := prompt.Select("Where should the space be stored?",
		"Storage is pluggable. Solo works fine on the local filesystem; git adds off-site backup and history without running a server.",
		[]prompt.Choice{
			{ID: "local", Label: "Local filesystem", Desc: "Everything stays in the space directory on this machine. Versions are still kept — this is not \"no history\".", Note: "recommended"},
			{ID: "git", Label: "Git remote", Desc: "Same space, mirrored to a git remote you own (e.g. a private GitHub repo). Off-site backup, no server needed."},
		}, 0)
	if err != nil {
		return "", "", err
	}
	if i == 0 {
		return "local", "", nil
	}
	remote, err := prompt.Text("Git remote URL",
		"An empty repository you control, e.g. git@github.com:me/my-context.git. Credentials come from your normal git setup.", "")
	if err != nil {
		return "", "", err
	}
	if strings.TrimSpace(remote) == "" {
		return "local", "", nil
	}
	return "git", remote, nil
}

// wizardPlugins ticks the AI clients contextd can see and lets the user correct
// it. Everything is a checkbox: nothing detected no longer means "type ids into
// a free-text field", it means the same list with nothing pre-ticked.
func wizardPlugins(cmd *cobra.Command, spaceRoot string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	vars, err := plugins.DefaultVars(spaceRoot, cwd, "")
	if err != nil {
		return err
	}
	cat, err := plugins.LoadDefaultCatalog(plugins.CatalogOpts{})
	if err != nil {
		return err
	}
	if len(cat) == 0 {
		return nil
	}

	detected := map[string]string{}
	for _, d := range plugins.Detect(cat, vars) {
		detected[d.Integration.ID] = d.How
	}

	choices := make([]prompt.Choice, 0, len(cat))
	pre := make([]bool, 0, len(cat))
	for _, in := range cat {
		note := ""
		if how, ok := detected[in.ID]; ok {
			note = "detected via " + how
		}
		choices = append(choices, prompt.Choice{
			ID:    in.ID,
			Label: in.Display,
			Desc:  pluginDesc(in),
			Note:  note,
		})
		pre = append(pre, detected[in.ID] != "")
	}

	lede := "Checked clients get wired to read your space at session start. Nothing is installed for the others."
	if len(detected) == 0 {
		lede = "Nothing was auto-detected, so nothing is ticked — check whichever you actually use."
	}
	picked, err := prompt.MultiSelect("Wire your AI tools", lede, choices, pre)
	if err != nil {
		return err
	}
	if len(picked) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "  No AI clients wired. Run contextd plugin install later, or contextd activate in a project.")
		return nil
	}
	for _, i := range picked {
		res, err := plugins.ApplyByID(cat, choices[i].ID, vars)
		if err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "  ✗ %s: %v\n", choices[i].ID, err)
			continue
		}
		fmt.Fprintf(cmd.OutOrStdout(), "  ✅ %s (%s) → %s\n", res.ID, res.Action, res.Target)
	}
	return nil
}

// pluginChooser is the picker `plugin install` and `activate` use when nothing
// was auto-detected — the same checkbox list as the wizard, instead of asking
// the user to type "1,3" or "all" into a text field.
func pluginChooser(catalog []*plugins.Integration) ([]*plugins.Integration, error) {
	if !prompt.Interactive() {
		return nil, nil
	}
	choices := make([]prompt.Choice, 0, len(catalog))
	for _, in := range catalog {
		choices = append(choices, prompt.Choice{ID: in.ID, Label: in.Display, Desc: pluginDesc(in)})
	}
	picked, err := prompt.MultiSelect("Which AI tools do you use?",
		"Nothing was auto-detected. Check the ones you actually use — contextd wires those to read your space.",
		choices, nil)
	if err != nil {
		if errors.Is(err, prompt.ErrCancelled) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]*plugins.Integration, 0, len(picked))
	for _, i := range picked {
		out = append(out, catalog[i])
	}
	return out, nil
}

func pluginDesc(in *plugins.Integration) string {
	switch in.Mechanism {
	case plugins.MechanismCommandHook:
		return "Live hook: re-reads your space at the start of every session, so it is never stale."
	case plugins.MechanismRulesSlot, plugins.MechanismInstructionsSlot:
		target := in.Target
		if in.Merge == plugins.MergeMarkedBlock {
			return "Writes a marked block into " + target + ", leaving the rest of the file alone. Snapshot, refreshed on activate."
		}
		return "Writes the entry set into " + target + ". Snapshot, refreshed on activate."
	default:
		return in.Notes
	}
}

// printSpaceMap closes the wizard by explaining what now exists and why. The old
// init printed two lines and left the user to guess what eleven new files were
// for.
func printSpaceMap(out io.Writer, root, mode string) {
	fmt.Fprintf(out, "\n✅ Your %s context space is ready at %s\n\n", mode, root)
	fmt.Fprintln(out, "What is in it:")

	rows := []struct{ path, why string }{
		{"context-entry.md", "the front door — tells an AI where to go next"},
		{"space-index.md", "the map of everything in the space"},
		{"identity/me.md", "who you are; the AI reads this first"},
		{"team/principles.md", "how you work — rules the AI should follow"},
		{"team/space-map.md", "navigation for larger spaces"},
		{"decisions.md", "choices you have made and don't want re-litigated"},
		{"projects/", "one folder per project, with its own context"},
		{"config.yaml", "mode, identity and storage backend"},
	}
	for _, r := range rows {
		if _, err := os.Stat(root + "/" + strings.TrimSuffix(r.path, "/")); err != nil {
			continue
		}
		fmt.Fprintf(out, "  %-22s %s\n", r.path, r.why)
	}

	fmt.Fprintln(out, "\nThey are ordinary Markdown files — open them, edit them, keep them true.")
	fmt.Fprintln(out, "\nNext:")
	fmt.Fprintf(out, "  contextd file edit identity/me.md   fill in your details\n")
	fmt.Fprintf(out, "  cd <your-project> && contextd activate   point your AI tools at this space\n")
	fmt.Fprintf(out, "  contextd tui                        browse and edit it full-screen\n")
	fmt.Fprintf(out, "  contextd status                     check what is wired\n")
}

// seedFileLog records every file of a freshly seeded working tree as version 1,
// so version history covers the space from the moment it exists rather than
// from the first time someone happens to edit through contextd.
//
// Files already known to the log are skipped, which makes this safe to run on a
// re-seed: it fills gaps instead of stacking a duplicate version on content
// that has not changed.
func seedFileLog(cmd *cobra.Command, root string) (int, error) {
	fl, err := openFileLog()
	if err != nil {
		return 0, err
	}
	ctx := cmd.Context()

	var seeded int
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Never walk into contextd's own storage or a VCS directory.
			switch d.Name() {
			case ".contextverse", ".sync", ".git":
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "config.yaml" || strings.HasPrefix(rel, ".") {
			return nil
		}
		if _, err := fl.LiveVersion(ctx, rel); err == nil {
			return nil // already tracked
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if _, err := fl.Put(ctx, rel, body, ""); err != nil {
			return fmt.Errorf("%s: %w", rel, err)
		}
		seeded++
		return nil
	})
	return seeded, err
}
