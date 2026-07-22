package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

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
			if len(rows) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "(no files)")
				return nil
			}
			for _, r := range rows {
				fmt.Fprintf(cmd.OutOrStdout(), "%-48s  %s\n", r.path, storage.DisplayVersion(r.ver))
			}
			return nil
		},
	}
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
			fmt.Fprintf(cmd.OutOrStdout(), "path=%s current=%s\n", args[0], storage.DisplayVersion(storage.FormatFileVersion(meta.Current)))
			if len(versions) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "(no versions)")
				return nil
			}
			for _, v := range versions {
				flags := ""
				if v.Destroyed {
					flags += " destroyed"
				}
				if v.DeletedAt != nil {
					flags += " deleted"
				}
				if v.Version == meta.Current {
					flags += " current"
				}
				fmt.Fprintf(cmd.OutOrStdout(), "  v%-4d  %s  %d bytes%s\n",
					v.Version, v.CreatedAt.Format(time.RFC3339), v.Size, flags)
			}
			return nil
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
