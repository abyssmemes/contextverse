package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/abyssmemes/contextverse/internal/config"
	"github.com/abyssmemes/contextverse/internal/logx"
	"github.com/abyssmemes/contextverse/internal/syncclient"
)

func newInitClientCmd() *cobra.Command {
	var (
		url            string
		token          string
		spaceName      string
		name           string
		language       string
		nonInteractive bool
		force          bool
	)
	cmd := &cobra.Command{
		Use:   "client",
		Short: "Initialize a client checkout that syncs from a server",
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := resolveSpaceRoot()
			if err != nil {
				return err
			}
			if !nonInteractive {
				in := bufio.NewReader(cmd.InOrStdin())
				url = prompt(in, "Server URL", orDefault(url, "http://127.0.0.1:8743"))
				token = prompt(in, "API token", token)
				spaceName = prompt(in, "Space name", orDefault(spaceName, "team"))
				name = prompt(in, "Your name", name)
				language = prompt(in, "Preferred language", orDefault(language, "English"))
			}
			if url == "" || token == "" || spaceName == "" {
				return fmt.Errorf("--url, --token, and --space are required")
			}
			if language == "" {
				language = "English"
			}
			if config.Exists(root) && !force {
				return fmt.Errorf("already initialized at %s (use --force)", root)
			}
			if err := os.MkdirAll(root, 0o755); err != nil {
				return err
			}
			cfg := &config.Config{
				Mode:      config.ModeClient,
				SpaceRoot: root,
				Identity:  config.Identity{Name: name, Language: language},
				Server: config.ClientServer{
					URL:       url,
					Space:     spaceName,
					TokenFile: root + "/.token",
				},
			}
			// normalize token file path
			cfg.Server.TokenFile = ""
			if err := syncclient.WriteToken(root, token); err != nil {
				return err
			}
			if err := config.Save(cfg); err != nil {
				return err
			}

			client, err := syncclient.NewFromConfig(cfg)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
			defer cancel()
			if _, _, err := client.WhoAmI(ctx); err != nil {
				return fmt.Errorf("auth failed: %w", err)
			}
			meta, err := client.GetSpace(ctx)
			if err != nil {
				return err
			}
			syncCfg := syncclient.ParseSync(meta)
			st, err := syncclient.LoadState(root)
			if err != nil {
				return err
			}
			res, err := client.Pull(ctx, root, "", syncCfg, st, false)
			if err != nil {
				return fmt.Errorf("initial pull: %w", err)
			}
			if err := syncclient.SaveState(root, st); err != nil {
				return err
			}
			cfg.Sync.LastHead = res.Head
			cfg.Sync.LastSyncAt = time.Now().UTC()
			if err := config.Save(cfg); err != nil {
				return err
			}
			logx.L().Info("client init complete", "space_root", root, "head", res.Head)
			fmt.Fprintf(cmd.OutOrStdout(), "\n✅ Client initialized at %s\n", root)
			fmt.Fprintf(cmd.OutOrStdout(), "Server: %s  space=%s  files=%d\n", url, spaceName, res.Updated)
			fmt.Fprintf(cmd.OutOrStdout(), "Next: contextd pull | contextd push | contextd activate\n")
			return nil
		},
	}
	cmd.Flags().StringVar(&url, "url", "http://127.0.0.1:8743", "server base URL")
	cmd.Flags().StringVar(&token, "token", "", "API bearer token")
	cmd.Flags().StringVar(&spaceName, "space", "team", "space name")
	cmd.Flags().StringVar(&name, "name", "", "your name")
	cmd.Flags().StringVar(&language, "language", "English", "preferred language")
	cmd.Flags().BoolVar(&nonInteractive, "non-interactive", false, "do not prompt")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite existing config")
	return cmd
}

func newPullCmd() *cobra.Command {
	var check bool
	cmd := &cobra.Command{
		Use:   "pull",
		Short: "Pull changes from the server",
		RunE: func(cmd *cobra.Command, args []string) error {
			root, cfg, err := loadSpaceConfig()
			if err != nil {
				return err
			}
			if cfg.Mode != config.ModeClient {
				return fmt.Errorf("pull requires client mode (got %s)", cfg.Mode)
			}
			client, err := syncclient.NewFromConfig(cfg)
			if err != nil {
				return err
			}
			meta, err := client.GetSpace(cmd.Context())
			if err != nil {
				return err
			}
			syncCfg := syncclient.ParseSync(meta)
			st, err := syncclient.LoadState(root)
			if err != nil {
				return err
			}
			res, err := client.Pull(cmd.Context(), root, cfg.Sync.LastHead, syncCfg, st, check)
			if err != nil {
				return err
			}
			if check {
				fmt.Fprintf(cmd.OutOrStdout(), "would update %d files (skip %d); head=%s\n", res.Updated, res.Skipped, res.Head)
				return nil
			}
			if err := syncclient.SaveState(root, st); err != nil {
				return err
			}
			cfg.Sync.LastHead = res.Head
			cfg.Sync.LastSyncAt = time.Now().UTC()
			if err := config.Save(cfg); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "pulled %d files (skipped %d); head=%s\n", res.Updated, res.Skipped, res.Head)
			return nil
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "dry-run")
	return cmd
}

func newPushCmd() *cobra.Command {
	var check bool
	cmd := &cobra.Command{
		Use:   "push",
		Short: "Push local changes to the server",
		RunE: func(cmd *cobra.Command, args []string) error {
			root, cfg, err := loadSpaceConfig()
			if err != nil {
				return err
			}
			if cfg.Mode != config.ModeClient {
				return fmt.Errorf("push requires client mode (got %s)", cfg.Mode)
			}
			client, err := syncclient.NewFromConfig(cfg)
			if err != nil {
				return err
			}
			meta, err := client.GetSpace(cmd.Context())
			if err != nil {
				return err
			}
			syncCfg := syncclient.ParseSync(meta)
			head, err := client.Head(cmd.Context())
			if err != nil {
				return err
			}
			// Prefer last known head for CAS; refresh if empty.
			expected := cfg.Sync.LastHead
			if expected == "" {
				expected = head
			}
			res, err := client.Push(cmd.Context(), root, expected, syncCfg, check)
			if err != nil {
				return err
			}
			if check {
				fmt.Fprintf(cmd.OutOrStdout(), "would push %d ops; expected_head=%s\n", res.Applied, expected)
				return nil
			}
			cfg.Sync.LastHead = res.Head
			cfg.Sync.LastSyncAt = time.Now().UTC()
			if err := config.Save(cfg); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "pushed %d ops; head=%s\n", res.Applied, res.Head)
			return nil
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "dry-run")
	return cmd
}

// softPull attempts a pull with timeout; returns warning string if skipped.
func softPull(ctx context.Context, cfg *config.Config, timeout time.Duration) string {
	if cfg.Mode != config.ModeClient {
		return ""
	}
	pctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	client, err := syncclient.NewFromConfig(cfg)
	if err != nil {
		return err.Error()
	}
	meta, err := client.GetSpace(pctx)
	if err != nil {
		return err.Error()
	}
	syncCfg := syncclient.ParseSync(meta)
	st, err := syncclient.LoadState(cfg.SpaceRoot)
	if err != nil {
		return err.Error()
	}
	res, err := client.Pull(pctx, cfg.SpaceRoot, cfg.Sync.LastHead, syncCfg, st, false)
	if err != nil {
		return err.Error()
	}
	_ = syncclient.SaveState(cfg.SpaceRoot, st)
	cfg.Sync.LastHead = res.Head
	cfg.Sync.LastSyncAt = time.Now().UTC()
	_ = config.Save(cfg)
	return ""
}