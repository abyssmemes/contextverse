package syncclient

import (
	"context"
	"fmt"
	"time"

	"github.com/abyssmemes/contextverse/internal/config"
	"github.com/abyssmemes/contextverse/internal/logx"
)

// PollOnce checks server head and pulls if it changed. Used by contextd daemon
// and integration tests — same semantics as `contextd pull` when head moved.
func PollOnce(ctx context.Context, root string, cfg *config.Config) (pulled bool, err error) {
	client, err := NewFromConfig(cfg)
	if err != nil {
		return false, err
	}
	head, err := client.Head(ctx)
	if err != nil {
		return false, fmt.Errorf("head: %w", err)
	}
	if head == cfg.Sync.LastHead {
		logx.L().Debug("daemon unchanged", "head", head)
		return false, nil
	}
	meta, err := client.GetSpace(ctx)
	if err != nil {
		return false, fmt.Errorf("get space: %w", err)
	}
	syncCfg := ParseSync(meta)
	st, err := LoadState(root)
	if err != nil {
		return false, fmt.Errorf("state: %w", err)
	}
	res, err := client.Pull(ctx, root, cfg.Sync.LastHead, syncCfg, st, false)
	if err != nil {
		return false, fmt.Errorf("pull: %w", err)
	}
	_ = SaveState(root, st)
	cfg.Sync.LastHead = res.Head
	cfg.Sync.LastSyncAt = time.Now().UTC()
	if err := config.Save(cfg); err != nil {
		return true, err
	}
	logx.L().Info("daemon pulled", "updated", res.Updated, "head", res.Head)
	return true, nil
}
