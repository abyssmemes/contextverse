package storage

import (
	"context"
	"strings"
)

// Prefixed namespaces blob paths and head scopes under a shared Backend
// (S3/SQL) so multiple spaces do not collide on the same keys.
type Prefixed struct {
	Inner  Backend
	Prefix string // e.g. "spaces/team" — no trailing slash required
}

func (p *Prefixed) Name() string { return p.Inner.Name() }

func (p *Prefixed) ns(path string) string {
	path = strings.TrimPrefix(path, "/")
	if p.Prefix == "" {
		return path
	}
	if path == "" {
		return strings.Trim(p.Prefix, "/")
	}
	return strings.Trim(p.Prefix, "/") + "/" + path
}

func (p *Prefixed) strip(path string) string {
	pre := strings.Trim(p.Prefix, "/")
	if pre == "" {
		return path
	}
	pre += "/"
	return strings.TrimPrefix(path, pre)
}

func (p *Prefixed) Get(ctx context.Context, path string) ([]byte, Version, error) {
	return p.Inner.Get(ctx, p.ns(path))
}

func (p *Prefixed) List(ctx context.Context, prefix string) ([]Entry, error) {
	entries, err := p.Inner.List(ctx, p.ns(prefix))
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(entries))
	for _, e := range entries {
		out = append(out, Entry{Path: p.strip(e.Path), Version: e.Version})
	}
	return out, nil
}

func (p *Prefixed) Put(ctx context.Context, path string, data []byte, expected Version) (Version, error) {
	return p.Inner.Put(ctx, p.ns(path), data, expected)
}

func (p *Prefixed) Delete(ctx context.Context, path string, expected Version) error {
	return p.Inner.Delete(ctx, p.ns(path), expected)
}

func (p *Prefixed) Head(ctx context.Context, scope string) (Version, error) {
	return p.Inner.Head(ctx, p.ns(scope))
}

func (p *Prefixed) SetHead(ctx context.Context, scope string, expected, next Version) error {
	return p.Inner.SetHead(ctx, p.ns(scope), expected, next)
}
