package cli

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/abyssmemes/contextverse/internal/config"
	"github.com/abyssmemes/contextverse/internal/logx"
	"github.com/abyssmemes/contextverse/internal/spacesvc"
	"github.com/abyssmemes/contextverse/internal/storage"
)

// Server-side space management.
//
// These existed in the Web UI (`POST /ui/spaces`) and the HTTP API long before
// the CLI, which inverted the project's own rule that a capability lives in the
// CLI first and the UIs only present it. They run against the local server data
// dir through the same spacesvc the API uses, so all three surfaces produce the
// same result.

// SpaceEntry is one row of `space list`.
type SpaceEntry struct {
	Name     string `json:"name" yaml:"name"`
	Head     string `json:"head,omitempty" yaml:"head,omitempty"`
	Files    int    `json:"files" yaml:"files"`
	Bytes    int64  `json:"bytes" yaml:"bytes"`
	Template string `json:"template,omitempty" yaml:"template,omitempty"`
}

// SpaceDetail is the structured form of `space show`.
type SpaceDetail struct {
	Name        string   `json:"name" yaml:"name"`
	CreatedAt   string   `json:"created_at,omitempty" yaml:"created_at,omitempty"`
	Template    string   `json:"template,omitempty" yaml:"template,omitempty"`
	Head        string   `json:"head,omitempty" yaml:"head,omitempty"`
	Files       int      `json:"files" yaml:"files"`
	Bytes       int64    `json:"bytes" yaml:"bytes"`
	SyncDefault string   `json:"sync_default,omitempty" yaml:"sync_default,omitempty"`
	SyncRules   []string `json:"sync_rules,omitempty" yaml:"sync_rules,omitempty"`
	Root        string   `json:"root" yaml:"root"`
}

// openSpaceService resolves the server data dir and its configured backend.
func openSpaceService() (*spacesvc.Service, string, error) {
	dir, err := resolveServerDir()
	if err != nil {
		return nil, "", err
	}
	if !config.ServerExists(dir) {
		return nil, "", fmt.Errorf("no server at %s — these commands manage a server's spaces (run contextd init server, or pass --server-dir)", dir)
	}
	cfg, err := config.LoadServer(dir)
	if err != nil {
		return nil, "", err
	}
	return &spacesvc.Service{DataDir: dir, Backend: cfg.Backend}, dir, nil
}

func newSpaceListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List spaces on this server",
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, _, err := openSpaceService()
			if err != nil {
				return err
			}
			names, err := svc.List()
			if err != nil {
				return err
			}
			out := make([]SpaceEntry, 0, len(names))
			for _, name := range names {
				e := SpaceEntry{Name: name}
				if head, err := svc.Head(cmd.Context(), name); err == nil {
					e.Head = string(head)
				}
				if bytes, files, err := svc.SpaceUsage(cmd.Context(), name); err == nil {
					e.Bytes, e.Files = bytes, files
				}
				if meta, err := svc.LoadMeta(name); err == nil {
					e.Template = meta.Template
				}
				out = append(out, e)
			}
			return emit(cmd.OutOrStdout(), out, func(w io.Writer) error {
				if len(out) == 0 {
					fmt.Fprintln(w, "(no spaces)")
					return nil
				}
				for _, e := range out {
					fmt.Fprintf(w, "%-24s %5d files  %9d bytes  %s\n", e.Name, e.Files, e.Bytes, e.Head)
				}
				return nil
			})
		},
	}
}

func newSpaceShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <name>",
		Short: "Show one space: template, head, size and sync rules",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, _, err := openSpaceService()
			if err != nil {
				return err
			}
			name := args[0]
			meta, err := svc.LoadMeta(name)
			if err != nil {
				return err
			}
			root, err := svc.SpaceRootFor(name)
			if err != nil {
				return err
			}
			d := SpaceDetail{
				Name:        meta.Name,
				Template:    meta.Template,
				SyncDefault: meta.Sync.Default,
				Root:        root,
			}
			if !meta.CreatedAt.IsZero() {
				d.CreatedAt = meta.CreatedAt.Format(time.RFC3339)
			}
			for _, r := range meta.Sync.Rules {
				d.SyncRules = append(d.SyncRules, fmt.Sprintf("%s %s", r.Path, r.Mode))
			}
			if head, err := svc.Head(cmd.Context(), name); err == nil {
				d.Head = string(head)
			}
			if bytes, files, err := svc.SpaceUsage(cmd.Context(), name); err == nil {
				d.Bytes, d.Files = bytes, files
			}
			return emit(cmd.OutOrStdout(), d, func(w io.Writer) error {
				fmt.Fprintf(w, "name:       %s\n", d.Name)
				if d.CreatedAt != "" {
					fmt.Fprintf(w, "created:    %s\n", d.CreatedAt)
				}
				fmt.Fprintf(w, "template:   %s\n", orDefault(d.Template, "(none)"))
				fmt.Fprintf(w, "head:       %s\n", d.Head)
				fmt.Fprintf(w, "files:      %d\n", d.Files)
				fmt.Fprintf(w, "bytes:      %d\n", d.Bytes)
				fmt.Fprintf(w, "root:       %s\n", d.Root)
				fmt.Fprintf(w, "sync:       %s\n", orDefault(d.SyncDefault, "always"))
				for _, r := range d.SyncRules {
					fmt.Fprintf(w, "  rule:     %s\n", r)
				}
				return nil
			})
		},
	}
}

func newSpaceCreateCmd() *cobra.Command {
	var (
		templateName string
		force        bool
	)
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a space on this server, seeded from a template",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, dir, err := openSpaceService()
			if err != nil {
				return err
			}
			name := args[0]
			meta, err := svc.Create(cmd.Context(), name, templateName, force)
			if err != nil {
				return err
			}
			logx.L().Info("space created", "space", name, "template", meta.Template, "data_dir", dir)
			return emit(cmd.OutOrStdout(), SpaceEntry{Name: meta.Name, Template: meta.Template}, func(w io.Writer) error {
				fmt.Fprintf(w, "created space %s from template %s\n", meta.Name, orDefault(meta.Template, "(none)"))
				fmt.Fprintf(w, "grant access with: contextd policy … / contextd acl allow <user> spaces/%s/\n", meta.Name)
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&templateName, "template", "solo-default", "template to seed the space from")
	cmd.Flags().BoolVar(&force, "force", false, "re-seed if the space already exists")
	return cmd
}

func newSpaceDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a space and everything in it",
		Long: `Delete a space, its files and its version history.

This is not a soft delete and there is no undo — take a snapshot first if the
content might be wanted again (contextd history snapshot).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, _, err := openSpaceService()
			if err != nil {
				return err
			}
			name := args[0]

			// Deleting a team's context is irreversible and easy to mistype, so it
			// takes an explicit confirmation rather than just succeeding quietly.
			if !yes {
				bytes, files, _ := svc.SpaceUsage(cmd.Context(), name)
				return fmt.Errorf("refusing to delete %q (%d files, %d bytes) without --yes; history is destroyed and cannot be recovered",
					name, files, bytes)
			}
			if err := svc.Delete(name); err != nil {
				return err
			}
			logx.L().Info("space deleted", "space", name)
			fmt.Fprintf(cmd.OutOrStdout(), "deleted space %s\n", name)
			return nil
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm irreversible deletion")
	return cmd
}

// newSpaceAdoptCmd brings an existing working tree into the version log.
//
// Spaces created before setup recorded a baseline have their documents on disk
// and nothing in the log. Everything that reads through the FileLog — `file
// list`, `file history`, the TUI Files tab, the local console — therefore shows
// an empty space while the files are plainly there. That contradiction is the
// most confusing state contextd can be in, and this is the way out of it.
func newSpaceAdoptCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "adopt",
		Short: "Record existing files as version 1 so they become visible",
		Long: `Bring a working tree that predates version tracking into the log.

Files already tracked are left alone, so this fills gaps rather than creating
duplicate versions. Run it once; contextd also does it for you on activate.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := resolveSpaceRoot()
			if err != nil {
				return err
			}
			n, err := seedFileLog(cmd, root)
			if err != nil {
				return err
			}
			if n == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "nothing to adopt — every file is already tracked")
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "adopted %d file(s) as v1 — they are now visible to file list, the TUI and the console\n", n)
			return nil
		},
	}
}

// adoptUntrackedFiles is the automatic path. It says nothing when there is
// nothing to do, so it can sit inside commands people already run.
func adoptUntrackedFiles(cmd *cobra.Command, root string) {
	fl, err := openFileLog()
	if err != nil {
		return
	}
	// Only act on the pathological case: a log that knows nothing at all while
	// the tree has content. A partially tracked space is somebody's deliberate
	// state and is not ours to rewrite.
	entries, err := fl.Backend.List(cmd.Context(), "")
	if err != nil {
		return
	}
	for _, e := range entries {
		if !storage.IsFileLogInternal(e.Path) && !strings.HasPrefix(e.Path, storage.SnapshotPrefix) {
			return
		}
	}
	n, err := seedFileLog(cmd, root)
	if err != nil {
		logx.L().Warn("adopt existing files", "err", err)
		return
	}
	if n > 0 {
		logx.L().Info("adopted untracked files", "count", n, "space_root", root)
		fmt.Fprintf(cmd.ErrOrStderr(), "recorded %d existing file(s) as v1 so they are visible to file list and the TUI\n", n)
	}
}

// SyncRuleChange is what `space sync set` reports.
type SyncRuleChange struct {
	Space string `json:"space" yaml:"space"`
	Path  string `json:"path" yaml:"path"`
	From  string `json:"from,omitempty" yaml:"from,omitempty"`
	To    string `json:"to" yaml:"to"`
}

func newSpaceSyncCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Change which paths travel between server and clients",
		Long: `Set a path's sync mode on a server space.

  always     synced in both directions
  init-only  seeded once when a client joins, then local — never pushed back
  never      stays where it is

The defaults suit a team: identity/ is init-only, so a person's own me.md is
seeded from the template and then belongs to their machine.

That is the wrong default for one person syncing their own two machines
through their own server, where there is no team copy to protect:

    contextd space sync set my-space identity/ always

Editing this used to mean hand-editing meta.yaml on the server, which meant
nobody knew it was possible.`,
	}
	cmd.AddCommand(newSpaceSyncSetCmd())
	return cmd
}

func newSpaceSyncSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <space> <path> <always|init-only|never>",
		Short: "Set the sync mode for a path in a server space",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, path, mode := args[0], args[1], args[2]
			switch mode {
			case "always", "init-only", "never":
			default:
				return fmt.Errorf("unknown mode %q (always, init-only, never)", mode)
			}
			svc, _, err := openSpaceService()
			if err != nil {
				return err
			}
			meta, err := svc.LoadMeta(name)
			if err != nil {
				return err
			}

			change := SyncRuleChange{Space: name, Path: path, To: mode}
			found := false
			for i, r := range meta.Sync.Rules {
				if r.Path == path {
					change.From = r.Mode
					meta.Sync.Rules[i].Mode = mode
					found = true
					break
				}
			}
			if !found {
				meta.Sync.Rules = append(meta.Sync.Rules, spacesvc.SyncRule{Path: path, Mode: mode})
			}
			if err := svc.SaveMeta(meta); err != nil {
				return err
			}

			return emit(cmd.OutOrStdout(), change, func(w io.Writer) error {
				if change.From == "" {
					fmt.Fprintf(w, "%s: %s is now %s\n", name, path, mode)
				} else {
					fmt.Fprintf(w, "%s: %s %s → %s\n", name, path, change.From, mode)
				}
				if mode == "always" && strings.HasPrefix(path, "identity") {
					fmt.Fprintln(w, "\nidentity/ will now sync in both directions. That is what you want")
					fmt.Fprintln(w, "for your own devices, and not what you want on a shared team space,")
					fmt.Fprintln(w, "where it would push one person's me.md to everyone.")
				}
				return nil
			})
		},
	}
}
