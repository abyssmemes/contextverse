package cli

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/abyssmemes/contextverse/internal/config"
	"github.com/abyssmemes/contextverse/internal/logx"
	"github.com/abyssmemes/contextverse/internal/storage"
)

func newBackendCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backend",
		Short: "Manage the storage backend (local|git|s3|sql)",
	}
	cmd.AddCommand(newBackendListCmd())
	cmd.AddCommand(newBackendShowCmd())
	cmd.AddCommand(newBackendSetCmd())
	cmd.AddCommand(newBackendTestCmd())
	cmd.AddCommand(newBackendMigrateCmd())
	return cmd
}

func newBackendListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List available storage backend drivers",
		RunE: func(cmd *cobra.Command, args []string) error {
			for _, d := range storage.KnownDrivers() {
				fmt.Fprintln(cmd.OutOrStdout(), d)
			}
			return nil
		},
	}
}

func newBackendShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Show the configured storage backend",
		RunE: func(cmd *cobra.Command, args []string) error {
			root, cfg, err := loadSpaceConfig()
			if err != nil {
				return err
			}
			driver := orDefault(cfg.Backend.Driver, storage.DriverLocal)
			fmt.Fprintf(cmd.OutOrStdout(), "space_root: %s\n", root)
			fmt.Fprintf(cmd.OutOrStdout(), "driver:     %s\n", driver)
			if cfg.Backend.GitRemote != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "git_remote: %s\n", cfg.Backend.GitRemote)
			}
			b, err := openConfiguredBackend(root, cfg)
			if err != nil {
				fmt.Fprintf(cmd.OutOrStdout(), "status:     error (%v)\n", err)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "status:     open (%s)\n", b.Name())
			head, err := b.Head(cmd.Context(), storage.SpaceScope)
			if errors.Is(err, storage.ErrNotFound) {
				fmt.Fprintf(cmd.OutOrStdout(), "space_head: (none)\n")
			} else if err != nil {
				fmt.Fprintf(cmd.OutOrStdout(), "space_head: error (%v)\n", err)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "space_head: %s\n", head)
			}
			return nil
		},
	}
}

func newBackendSetCmd() *cobra.Command {
	var (
		remote   string
		gitUser  string
		gitToken string
		gitKey   string
		s3Ep     string
		s3Bucket string
		s3Region string
		s3Prefix string
		s3AK     string
		s3SK     string
		sqlDSN   string
	)
	cmd := &cobra.Command{
		Use:   "set [local|git|s3|sql]",
		Short: "Configure the storage backend driver",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			driver := strings.ToLower(args[0])
			ok := false
			for _, d := range storage.KnownDrivers() {
				if d == driver {
					ok = true
					break
				}
			}
			if !ok {
				return fmt.Errorf("unknown driver %q (want: %s)", driver, strings.Join(storage.KnownDrivers(), "|"))
			}
			root, cfg, err := loadSpaceConfig()
			if err != nil {
				return err
			}
			cfg.Backend.Driver = driver
			if remote != "" {
				cfg.Backend.GitRemote = remote
			}
			if gitUser != "" {
				cfg.Backend.GitUser = gitUser
			}
			if gitToken != "" {
				cfg.Backend.GitToken = gitToken
			}
			if gitKey != "" {
				cfg.Backend.GitSSHKey = gitKey
			}
			if driver == storage.DriverGit && cfg.Backend.GitRemote != "" {
				cfg.Backend.GitAutoPush = true
			}
			if s3Ep != "" {
				cfg.Backend.S3Endpoint = s3Ep
				cfg.Backend.S3PathStyle = true
			}
			if s3Bucket != "" {
				cfg.Backend.S3Bucket = s3Bucket
			}
			if s3Region != "" {
				cfg.Backend.S3Region = s3Region
			}
			if s3Prefix != "" {
				cfg.Backend.S3Prefix = s3Prefix
			}
			if s3AK != "" {
				cfg.Backend.S3AccessKey = s3AK
			}
			if s3SK != "" {
				cfg.Backend.S3SecretKey = s3SK
			}
			if sqlDSN != "" {
				cfg.Backend.SQLDSN = sqlDSN
			}
			if err := config.Save(cfg); err != nil {
				return err
			}
			if _, err := openConfiguredBackend(root, cfg); err != nil {
				return err
			}
			logx.L().Info("backend set", "driver", driver, "space_root", root)
			fmt.Fprintf(cmd.OutOrStdout(), "Backend set to %s\n", driver)
			return nil
		},
	}
	cmd.Flags().StringVar(&remote, "remote", "", "git remote URL (https or git@…)")
	cmd.Flags().StringVar(&gitUser, "git-user", "", "HTTPS git username (default: git)")
	cmd.Flags().StringVar(&gitToken, "git-token", "", "HTTPS PAT (or set CONTEXTVERSE_GIT_TOKEN / GITHUB_TOKEN)")
	cmd.Flags().StringVar(&gitKey, "git-ssh-key", "", "path to SSH private key for private repos")
	cmd.Flags().StringVar(&s3Ep, "s3-endpoint", "", "S3/MinIO endpoint (e.g. http://127.0.0.1:9000)")
	cmd.Flags().StringVar(&s3Bucket, "s3-bucket", "", "S3 bucket name")
	cmd.Flags().StringVar(&s3Region, "s3-region", "us-east-1", "S3 region")
	cmd.Flags().StringVar(&s3Prefix, "s3-prefix", "", "key prefix inside bucket")
	cmd.Flags().StringVar(&s3AK, "s3-access-key", "", "S3 access key")
	cmd.Flags().StringVar(&s3SK, "s3-secret-key", "", "S3 secret key")
	cmd.Flags().StringVar(&sqlDSN, "sql-dsn", "", "Postgres DSN")
	return cmd
}

func newBackendTestCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "test",
		Short: "Verify backend open + CAS primitive",
		RunE: func(cmd *cobra.Command, args []string) error {
			root, cfg, err := loadSpaceConfig()
			if err != nil {
				return err
			}
			b, err := openConfiguredBackend(root, cfg)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			path := "_health/cas-probe"
			v1, err := b.Put(ctx, path, []byte("a"), "")
			if err != nil {
				if data, ver, gerr := b.Get(ctx, path); gerr == nil {
					_ = data
					_ = b.Delete(ctx, path, ver)
					v1, err = b.Put(ctx, path, []byte("a"), "")
				}
				if err != nil {
					return fmt.Errorf("cas put: %w", err)
				}
			}
			if _, err := b.Put(ctx, path, []byte("b"), ""); !errors.Is(err, storage.ErrConflict) {
				return fmt.Errorf("expected CAS conflict, got %v", err)
			}
			v2, err := b.Put(ctx, path, []byte("b"), v1)
			if err != nil {
				return fmt.Errorf("cas update: %w", err)
			}
			if err := b.Delete(ctx, path, v2); err != nil {
				return fmt.Errorf("cas delete: %w", err)
			}
			if g, ok := b.(*storage.Git); ok {
				if err := g.TestConnectivity(ctx); err != nil {
					return fmt.Errorf("git connectivity: %w", err)
				}
			}
			if s3, ok := b.(*storage.S3); ok {
				if err := s3.TestConnectivity(ctx); err != nil {
					return fmt.Errorf("s3 connectivity: %w", err)
				}
			}
			if sq, ok := b.(*storage.SQL); ok {
				if err := sq.TestConnectivity(ctx); err != nil {
					return fmt.Errorf("sql connectivity: %w", err)
				}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "ok: driver=%s cas=pass\n", b.Name())
			return nil
		},
	}
}

func newBackendMigrateCmd() *cobra.Command {
	var remote string
	cmd := &cobra.Command{
		Use:   "migrate [local|git]",
		Short: "Copy all stored objects to another backend and switch config",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			to := strings.ToLower(args[0])
			ok := false
			for _, d := range storage.KnownDrivers() {
				if d == to {
					ok = true
					break
				}
			}
			if !ok {
				return fmt.Errorf("unknown driver %q", to)
			}
			root, cfg, err := loadSpaceConfig()
			if err != nil {
				return err
			}
			fromDriver := orDefault(cfg.Backend.Driver, storage.DriverLocal)
			if fromDriver == to {
				return fmt.Errorf("already using %s", to)
			}
			src, err := openConfiguredBackend(root, cfg)
			if err != nil {
				return err
			}
			dstOpts := storage.OpenOptions{Driver: to, SpaceRoot: root, Backend: cfg.Backend}
			dstOpts.Backend.Driver = to
			if remote != "" {
				dstOpts.Backend.GitRemote = remote
			}
			dst, err := storage.Open(dstOpts)
			if err != nil {
				return err
			}
			n, err := storage.Migrate(cmd.Context(), src, dst)
			if err != nil {
				return err
			}
			cfg.Backend.Driver = to
			if remote != "" {
				cfg.Backend.GitRemote = remote
			}
			if err := config.Save(cfg); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Migrated %d objects: %s → %s\n", n, fromDriver, to)
			return nil
		},
	}
	cmd.Flags().StringVar(&remote, "remote", "", "git remote URL when migrating to git")
	return cmd
}

func newHistoryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "history",
		Short: "Space snapshots (versioning on top of the storage backend)",
	}
	cmd.AddCommand(newHistorySnapshotCmd())
	cmd.AddCommand(newHistoryListCmd())
	cmd.AddCommand(newHistoryRestoreCmd())
	cmd.AddCommand(newHistoryShowCmd())
	return cmd
}

func newHistorySnapshotCmd() *cobra.Command {
	var message string
	cmd := &cobra.Command{
		Use:   "snapshot",
		Short: "Snapshot the current space into the configured backend",
		RunE: func(cmd *cobra.Command, args []string) error {
			root, cfg, err := loadSpaceConfig()
			if err != nil {
				return err
			}
			b, err := openConfiguredBackend(root, cfg)
			if err != nil {
				return err
			}
			hist := &storage.History{Backend: b}
			meta, err := hist.SnapshotSpace(cmd.Context(), root, message)
			if err != nil {
				return err
			}
			if g, ok := b.(*storage.Git); ok {
				if err := g.Push(cmd.Context()); err != nil {
					logx.L().Warn("git push after snapshot failed", "err", err)
					fmt.Fprintf(cmd.OutOrStdout(), "snapshot %s (%d files) — push failed: %v\n", meta.ID, len(meta.Files), err)
					return nil
				}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "snapshot %s (%d files)\n", meta.ID, len(meta.Files))
			return nil
		},
	}
	cmd.Flags().StringVarP(&message, "message", "m", "", "snapshot message")
	return cmd
}

func newHistoryListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List snapshots (newest first)",
		RunE: func(cmd *cobra.Command, args []string) error {
			root, cfg, err := loadSpaceConfig()
			if err != nil {
				return err
			}
			b, err := openConfiguredBackend(root, cfg)
			if err != nil {
				return err
			}
			list, err := (&storage.History{Backend: b}).ListSnapshots(cmd.Context())
			if err != nil {
				return err
			}
			if len(list) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "(no snapshots)")
				return nil
			}
			for _, s := range list {
				msg := s.Message
				if msg != "" {
					msg = " — " + msg
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s  %s  %d files%s\n",
					s.ID, s.CreatedAt.Format("2006-01-02 15:04:05"), len(s.Files), msg)
			}
			return nil
		},
	}
}

func newHistoryShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <id>",
		Short: "Show a snapshot manifest",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, cfg, err := loadSpaceConfig()
			if err != nil {
				return err
			}
			b, err := openConfiguredBackend(root, cfg)
			if err != nil {
				return err
			}
			meta, err := (&storage.History{Backend: b}).GetSnapshot(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "id:      %s\n", meta.ID)
			fmt.Fprintf(cmd.OutOrStdout(), "created: %s\n", meta.CreatedAt.Format(time.RFC3339))
			fmt.Fprintf(cmd.OutOrStdout(), "message: %s\n", meta.Message)
			fmt.Fprintf(cmd.OutOrStdout(), "files:   %d\n", len(meta.Files))
			for path, ver := range meta.Files {
				fmt.Fprintf(cmd.OutOrStdout(), "  %s  %s\n", ver, path)
			}
			return nil
		},
	}
}

func newHistoryRestoreCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restore <id>",
		Short: "Restore space files from a snapshot",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, cfg, err := loadSpaceConfig()
			if err != nil {
				return err
			}
			b, err := openConfiguredBackend(root, cfg)
			if err != nil {
				return err
			}
			if err := (&storage.History{Backend: b}).RestoreSpace(cmd.Context(), root, args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Restored snapshot %s into %s\n", args[0], root)
			return nil
		},
	}
}

func loadSpaceConfig() (string, *config.Config, error) {
	root, err := resolveSpaceRoot()
	if err != nil {
		return "", nil, err
	}
	if !config.Exists(root) {
		return "", nil, fmt.Errorf("no config at %s — run contextd init solo first", root)
	}
	cfg, err := config.Load(root)
	if err != nil {
		return "", nil, err
	}
	return root, cfg, nil
}

func openConfiguredBackend(root string, cfg *config.Config) (storage.Backend, error) {
	return storage.Open(storage.OpenOptions{
		Driver:    orDefault(cfg.Backend.Driver, storage.DriverLocal),
		SpaceRoot: root,
		Backend:   cfg.Backend,
	})
}
