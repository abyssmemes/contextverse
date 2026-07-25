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

// ns namespaces a caller path. The path is validated first: without that,
// "../other-space/notes.md" would resolve out of this space's prefix and hit a
// neighbour's objects in the shared backend.
func (p *Prefixed) ns(path string) (string, error) {
	clean, err := CleanPath(path)
	if err != nil {
		return "", err
	}
	pre := strings.Trim(p.Prefix, "/")
	if pre == "" {
		return clean, nil
	}
	if clean == "" {
		return pre, nil
	}
	return pre + "/" + clean, nil
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
	key, err := p.ns(path)
	if err != nil {
		return nil, "", err
	}
	return p.Inner.Get(ctx, key)
}

func (p *Prefixed) List(ctx context.Context, prefix string) ([]Entry, error) {
	key, err := p.ns(prefix)
	if err != nil {
		return nil, err
	}
	entries, err := p.Inner.List(ctx, key)
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
	key, err := p.ns(path)
	if err != nil {
		return "", err
	}
	return p.Inner.Put(ctx, key, data, expected)
}

func (p *Prefixed) Delete(ctx context.Context, path string, expected Version) error {
	key, err := p.ns(path)
	if err != nil {
		return err
	}
	return p.Inner.Delete(ctx, key, expected)
}

func (p *Prefixed) Head(ctx context.Context, scope string) (Version, error) {
	key, err := p.ns(scope)
	if err != nil {
		return "", err
	}
	return p.Inner.Head(ctx, key)
}

func (p *Prefixed) SetHead(ctx context.Context, scope string, expected, next Version) error {
	key, err := p.ns(scope)
	if err != nil {
		return err
	}
	return p.Inner.SetHead(ctx, key, expected, next)
}
