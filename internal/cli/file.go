package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/abyssmemes/contextverse/internal/editor"
	"github.com/abyssmemes/contextverse/internal/logx"
	"github.com/abyssmemes/contextverse/internal/prompt"
	"github.com/abyssmemes/contextverse/internal/storage"
)

func newFileCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "file",
		Short: "Per-file version history (Vault KV v2-style)",
	}
	cmd.AddCommand(newFileListCmd())
	cmd.AddCommand(newFileHistoryCmd())
	cmd.AddCommand(newFileGetCmd())
	cmd.AddCommand(newFilePutCmd())
	cmd.AddCommand(newFileEditCmd())
	cmd.AddCommand(newFileDiffCmd())
	cmd.AddCommand(newFileRevertCmd())
	cmd.AddCommand(newFileUndeleteCmd())
	cmd.AddCommand(newFileDestroyCmd())
	return cmd
}

func openFileLog() (*storage.FileLog, error) {
	root, cfg, err := loadSpaceConfig()
	if err != nil {
		return nil, err
	}
	b, err := openConfiguredBackend(root, cfg)
	if err != nil {
		return nil, err
	}
	return &storage.FileLog{Backend: b}, nil
}

func newFileListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List tracked files with file versions (same as Web UI / TUI)",
		RunE: func(cmd *cobra.Command, args []string) error {
			fl, err := openFileLog()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			entries, err := fl.Backend.List(ctx, "")
			if err != nil {
				return err
			}
			type row struct {
				path string
				ver  storage.Version
			}
			var rows []row
			for _, e := range entries {
				if strings.HasPrefix(e.Path, storage.SnapshotPrefix) || storage.IsFileLogInternal(e.Path) {
					continue
				}
				if strings.HasPrefix(e.Path, "_health/") || strings.HasPrefix(e.Path, "_heads/") {
					continue
				}
				ver := e.Version
				if lv, lerr := fl.LiveVersion(ctx, e.Path); lerr == nil {
					ver = lv
				}
				rows = append(rows, row{e.Path, ver})
			}
			sort.Slice(rows, func(i, j int) bool { return rows[i].path < rows[j].path })

			out := make([]FileEntry, 0, len(rows))
			for _, r := range rows {
				out = append(out, FileEntry{Path: r.path, Version: storage.DisplayVersion(r.ver)})
			}
			return emit(cmd.OutOrStdout(), out, func(w io.Writer) error {
				if len(out) == 0 {
					fmt.Fprintln(w, "(no files)")
					return nil
				}
				for _, e := range out {
					fmt.Fprintf(w, "%-48s  %s\n", e.Path, e.Version)
				}
				return nil
			})
		},
	}
}

// FileEntry is one tracked file in `file list`.
type FileEntry struct {
	Path    string `json:"path" yaml:"path"`
	Version string `json:"version" yaml:"version"`
}

// FileVersionEntry is one row of `file history`.
type FileVersionEntry struct {
	Version   int    `json:"version" yaml:"version"`
	CreatedAt string `json:"created_at" yaml:"created_at"`
	Size      int    `json:"size" yaml:"size"`
	Current   bool   `json:"current" yaml:"current"`
	Deleted   bool   `json:"deleted,omitempty" yaml:"deleted,omitempty"`
	Destroyed bool   `json:"destroyed,omitempty" yaml:"destroyed,omitempty"`
}

// FileHistory is the structured form of `file history`.
type FileHistory struct {
	Path     string             `json:"path" yaml:"path"`
	Current  string             `json:"current" yaml:"current"`
	Versions []FileVersionEntry `json:"versions" yaml:"versions"`
}

func newFileHistoryCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "history <path>",
		Short: "List versions for a path (newest first)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			fl, err := openFileLog()
			if err != nil {
				return err
			}
			meta, versions, err := fl.ListVersions(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			hist := FileHistory{
				Path:     args[0],
				Current:  storage.DisplayVersion(storage.FormatFileVersion(meta.Current)),
				Versions: make([]FileVersionEntry, 0, len(versions)),
			}
			for _, v := range versions {
				hist.Versions = append(hist.Versions, FileVersionEntry{
					Version:   v.Version,
					CreatedAt: v.CreatedAt.Format(time.RFC3339),
					Size:      v.Size,
					Current:   v.Version == meta.Current,
					Deleted:   v.DeletedAt != nil,
					Destroyed: v.Destroyed,
				})
			}
			return emit(cmd.OutOrStdout(), hist, func(w io.Writer) error {
				fmt.Fprintf(w, "path=%s current=%s\n", hist.Path, hist.Current)
				if len(hist.Versions) == 0 {
					fmt.Fprintln(w, "(no versions)")
					return nil
				}
				for _, v := range hist.Versions {
					flags := ""
					if v.Destroyed {
						flags += " destroyed"
					}
					if v.Deleted {
						flags += " deleted"
					}
					if v.Current {
						flags += " current"
					}
					fmt.Fprintf(w, "  v%-4d  %s  %d bytes%s\n", v.Version, v.CreatedAt, v.Size, flags)
				}
				return nil
			})
		},
	}
}

func newFileGetCmd() *cobra.Command {
	var version int
	cmd := &cobra.Command{
		Use:   "get <path>",
		Short: "Print file content (optionally a historical version)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			fl, err := openFileLog()
			if err != nil {
				return err
			}
			var data []byte
			if version > 0 {
				data, _, err = fl.GetVersion(cmd.Context(), args[0], version)
			} else {
				data, _, err = fl.Get(cmd.Context(), args[0])
			}
			if err != nil {
				return err
			}
			_, err = cmd.OutOrStdout().Write(data)
			return err
		},
	}
	cmd.Flags().IntVarP(&version, "version", "v", 0, "historical version number")
	return cmd
}

// currentBody reads the live body and version of path, treating a missing file
// as an empty body at version zero so a new file can be created by the same
// code path that updates an existing one.
func currentBody(cmd *cobra.Command, fl *storage.FileLog, path string) ([]byte, storage.Version, error) {
	data, ver, err := fl.Get(cmd.Context(), path)
	if errors.Is(err, storage.ErrNotFound) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	return data, ver, nil
}

// commitBody writes a new version through the FileLog and mirrors it into the
// working tree. Writing straight to disk would skip the version log entirely —
// `file history` would never see the change — so every writer goes through here.
func commitBody(cmd *cobra.Command, fl *storage.FileLog, path string, data []byte, expected storage.Version) (storage.Version, error) {
	next, err := fl.Put(cmd.Context(), path, data, expected)
	if err != nil {
		return "", err
	}
	if root, _, lerr := loadSpaceConfig(); lerr == nil {
		if werr := writeCLITreeFile(root, path, data); werr != nil {
			logx.L().Warn("write working tree copy", "path", path, "err", werr)
		}
	}
	return next, nil
}

func newFilePutCmd() *cobra.Command {
	var from string
	cmd := &cobra.Command{
		Use:   "put <path>",
		Short: "Write a file as a new version (from a file or stdin)",
		Long: `Write content to a path in the space, creating a new version.

The write is compare-and-swap against the current version: if someone else
changed the file since it was read, the write is rejected rather than silently
overwriting their version.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if from == "" {
				return fmt.Errorf("--from is required (a file path, or - for stdin)")
			}
			var (
				data []byte
				err  error
			)
			if from == "-" {
				data, err = io.ReadAll(cmd.InOrStdin())
			} else {
				data, err = os.ReadFile(from)
			}
			if err != nil {
				return err
			}
			fl, err := openFileLog()
			if err != nil {
				return err
			}
			_, cur, err := currentBody(cmd, fl, args[0])
			if err != nil {
				return err
			}
			next, err := commitBody(cmd, fl, args[0], data, cur)
			if err != nil {
				return err
			}
			logx.L().Info("file put", "path", args[0], "bytes", len(data), "version", string(next))
			fmt.Fprintf(cmd.OutOrStdout(), "wrote %s → %s\n", args[0], storage.DisplayVersion(next))
			return nil
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "read content from this file, or - for stdin")
	return cmd
}

func newFileEditCmd() *cobra.Command {
	var editorID string
	cmd := &cobra.Command{
		Use:   "edit <path>",
		Short: "Open a file in your editor and save it as a new version",
		Long: `Check the file out into your editor, then write it back as a new version.

The editor is $VISUAL, then $EDITOR; if neither is set and several are installed
you are asked to pick one. --editor overrides. Saving with no changes creates no
version.

The version the file had when it was opened is used for compare-and-swap, so a
concurrent change is reported as a conflict instead of overwriting it.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := args[0]
			ed, err := resolveEditor(editorID)
			if err != nil {
				return err
			}
			fl, err := openFileLog()
			if err != nil {
				return err
			}
			data, opened, err := currentBody(cmd, fl, path)
			if err != nil {
				return err
			}
			logx.L().Info("file edit opening", "path", path, "editor", ed.ID, "version", string(opened))

			edited, changed, err := editor.Session(ed, path, data)
			if err != nil {
				return err
			}
			if !changed {
				logx.L().Info("file edit unchanged", "path", path)
				fmt.Fprintf(cmd.OutOrStdout(), "no changes to %s\n", path)
				return nil
			}
			next, err := commitBody(cmd, fl, path, edited, opened)
			if errors.Is(err, storage.ErrConflict) {
				// Keep the sentinel wrapped: main.go classifies it as exit 3, and a
				// plain fmt.Errorf here would silently downgrade it to a usage error.
				return fmt.Errorf("%s changed while you were editing (was %s) — re-run edit and reapply your change: %w",
					path, storage.DisplayVersion(opened), err)
			}
			if err != nil {
				return err
			}
			logx.L().Info("file edit saved", "path", path, "bytes", len(edited), "version", string(next))
			fmt.Fprintf(cmd.OutOrStdout(), "saved %s → %s\n", path, storage.DisplayVersion(next))
			return nil
		},
	}
	cmd.Flags().StringVar(&editorID, "editor", "", "editor binary to use (default: $VISUAL, $EDITOR, then first installed)")
	return cmd
}

// resolveEditor picks the editor for an edit session: an explicit id wins, then
// $VISUAL/$EDITOR, and only then does it ask. Asking someone who already told
// the environment which editor they want would be noise, so the picker appears
// exactly when the answer is genuinely ambiguous.
func resolveEditor(id string) (editor.Editor, error) {
	if id != "" {
		return editor.Lookup(id)
	}
	if ed, ok := editor.FromEnvironment(); ok {
		return ed, nil
	}
	found := editor.Detect()
	switch {
	case len(found) == 0:
		return editor.Editor{}, fmt.Errorf("no editor found: set $EDITOR, or install one of vim, nvim, nano, helix, micro, code")
	case len(found) == 1 || !prompt.Interactive():
		return found[0], nil
	}
	choices := make([]prompt.Choice, 0, len(found))
	for _, e := range found {
		desc := "Runs in this terminal."
		if !e.Terminal {
			desc = "Opens a separate window; contextd waits for you to close the file."
		}
		choices = append(choices, prompt.Choice{ID: e.ID, Label: e.Name, Desc: desc})
	}
	i, err := prompt.Select("Open with", "$EDITOR is not set, so pick one. Set $EDITOR to skip this next time.", choices, 0)
	if err != nil {
		if errors.Is(err, prompt.ErrCancelled) {
			return editor.Editor{}, fmt.Errorf("cancelled")
		}
		return editor.Editor{}, err
	}
	return found[i], nil
}

func newFileRevertCmd() *cobra.Command {
	var version int
	cmd := &cobra.Command{
		Use:   "revert <path>",
		Short: "Write a historical version's body as a new current version",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if version < 1 {
				return fmt.Errorf("--version is required")
			}
			fl, err := openFileLog()
			if err != nil {
				return err
			}
			data, _, err := fl.GetVersion(cmd.Context(), args[0], version)
			if err != nil {
				return err
			}
			_, cur, err := fl.Get(cmd.Context(), args[0])
			if err != nil && !errors.Is(err, storage.ErrNotFound) {
				return err
			}
			next, err := fl.Put(cmd.Context(), args[0], data, cur)
			if err != nil {
				return err
			}
			if root, _, lerr := loadSpaceConfig(); lerr == nil {
				_ = writeCLITreeFile(root, args[0], data)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "reverted %s from v%d → %s\n", args[0], version, storage.DisplayVersion(next))
			return nil
		},
	}
	cmd.Flags().IntVarP(&version, "version", "v", 0, "version to restore as new current")
	_ = cmd.MarkFlagRequired("version")
	return cmd
}

func newFileUndeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "undelete <path>",
		Short: "Restore a soft-deleted file to live",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			fl, err := openFileLog()
			if err != nil {
				return err
			}
			ver, err := fl.Undelete(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			data, _, err := fl.Get(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if root, _, lerr := loadSpaceConfig(); lerr == nil {
				_ = writeCLITreeFile(root, args[0], data)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "undeleted %s → %s\n", args[0], storage.DisplayVersion(ver))
			return nil
		},
	}
}

func newFileDestroyCmd() *cobra.Command {
	var version int
	cmd := &cobra.Command{
		Use:   "destroy <path>",
		Short: "Permanently destroy one historical version",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if version < 1 {
				return fmt.Errorf("--version is required")
			}
			fl, err := openFileLog()
			if err != nil {
				return err
			}
			if err := fl.Destroy(cmd.Context(), args[0], version); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "destroyed %s v%d\n", args[0], version)
			return nil
		},
	}
	cmd.Flags().IntVarP(&version, "version", "v", 0, "version to destroy")
	_ = cmd.MarkFlagRequired("version")
	return cmd
}

func writeCLITreeFile(spaceRoot, path string, data []byte) error {
	abs := filepath.Join(spaceRoot, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	tmp := abs + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, abs)
}
