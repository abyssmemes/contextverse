package server

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/abyssmemes/contextverse/internal/auth"
	"github.com/abyssmemes/contextverse/internal/authz"
	"github.com/abyssmemes/contextverse/internal/config"
	"github.com/abyssmemes/contextverse/internal/logx"
	"github.com/abyssmemes/contextverse/internal/storage"
)

func (s *Server) requireUIAdmin(w http.ResponseWriter, r *http.Request) *auth.Principal {
	p := principalFrom(r.Context())
	if p == nil {
		http.Error(w, "admin required", http.StatusForbidden)
		return nil
	}
	pols := p.Policies
	if len(pols) == 0 && p.Role != "" {
		pols = []string{string(p.Role)}
	}
	if s.Authz != nil {
		if !s.Authz.AllowUser(p.User, pols, "sys/backends", authz.CapUpdate, s.authzVars()) &&
			!s.Authz.AllowUser(p.User, pols, "sys/auth/users", authz.CapUpdate, s.authzVars()) {
			http.Error(w, "admin required", http.StatusForbidden)
			return nil
		}
		return p
	}
	if !auth.CanAdmin(p.Role) {
		http.Error(w, "admin required", http.StatusForbidden)
		return nil
	}
	return p
}

func backendFromForm(r *http.Request, base config.Backend) config.Backend {
	b := base
	if v := strings.TrimSpace(r.FormValue("driver")); v != "" {
		b.Driver = strings.ToLower(v)
	}
	if v := strings.TrimSpace(r.FormValue("backend")); v != "" {
		b.Driver = strings.ToLower(v)
	}
	if v := strings.TrimSpace(r.FormValue("git_remote")); v != "" {
		b.GitRemote = v
	}
	if v := strings.TrimSpace(r.FormValue("git_user")); v != "" {
		b.GitUser = v
	}
	if v := strings.TrimSpace(r.FormValue("git_token")); v != "" {
		b.GitToken = v
	}
	if v := strings.TrimSpace(r.FormValue("git_ssh_key")); v != "" {
		b.GitSSHKey = v
	}
	if b.Driver == storage.DriverGit && b.GitRemote != "" {
		b.GitAutoPush = true
	}
	if v := strings.TrimSpace(r.FormValue("s3_endpoint")); v != "" {
		b.S3Endpoint = v
		b.S3PathStyle = true
	}
	if v := strings.TrimSpace(r.FormValue("s3_bucket")); v != "" {
		b.S3Bucket = v
	}
	if v := strings.TrimSpace(r.FormValue("s3_region")); v != "" {
		b.S3Region = v
	}
	if v := strings.TrimSpace(r.FormValue("s3_prefix")); v != "" {
		b.S3Prefix = v
	}
	if v := strings.TrimSpace(r.FormValue("s3_access_key")); v != "" {
		b.S3AccessKey = v
	}
	if v := strings.TrimSpace(r.FormValue("s3_secret_key")); v != "" {
		b.S3SecretKey = v
	}
	if v := strings.TrimSpace(r.FormValue("sql_dsn")); v != "" {
		b.SQLDSN = v
	}
	if b.Driver == "" {
		b.Driver = storage.DriverLocal
	}
	return b
}

func knownDriver(driver string) bool {
	driver = strings.ToLower(driver)
	for _, d := range storage.KnownDrivers() {
		if d == driver {
			return true
		}
	}
	return false
}

func (s *Server) applyBackend(b config.Backend) error {
	if !knownDriver(b.Driver) {
		return fmt.Errorf("unknown driver %q (want: %s)", b.Driver, strings.Join(storage.KnownDrivers(), "|"))
	}
	space := s.Cfg.Defaults.Space
	if space == "" {
		space = "team"
	}
	// Prefer an existing space so Open uses a real root; fall back to default name.
	names, _ := s.Spaces.List()
	rootName := space
	if len(names) > 0 {
		rootName = names[0]
	}
	if _, err := storage.Open(storage.OpenOptions{
		SpaceRoot: s.Spaces.SpaceRoot(rootName),
		SpaceName: rootName,
		Backend:   b,
		Driver:    b.Driver,
	}); err != nil {
		return err
	}
	s.Cfg.Backend = b
	s.Spaces.Backend = b
	if err := config.SaveServer(s.Cfg); err != nil {
		return err
	}
	logx.L().Info("backend switched", "driver", b.Driver, "data_dir", s.Cfg.DataDir)
	return nil
}

func (s *Server) probeBackendStatus(r *http.Request) string {
	driver := s.Cfg.Backend.Driver
	if driver == "" {
		driver = storage.DriverLocal
	}
	space := s.Cfg.Defaults.Space
	if space == "" {
		space = "team"
	}
	names, _ := s.Spaces.List()
	if len(names) > 0 {
		space = names[0]
	}
	b, err := storage.Open(storage.OpenOptions{
		SpaceRoot: s.Spaces.SpaceRoot(space),
		SpaceName: space,
		Backend:   s.Cfg.Backend,
		Driver:    driver,
	})
	if err != nil {
		return err.Error()
	}
	switch t := b.(type) {
	case *storage.Git:
		if err := t.TestConnectivity(r.Context()); err != nil {
			return "unhealthy: " + err.Error()
		}
	case *storage.S3:
		if err := t.TestConnectivity(r.Context()); err != nil {
			return "unhealthy: " + err.Error()
		}
	case *storage.SQL:
		if err := t.TestConnectivity(r.Context()); err != nil {
			return "unhealthy: " + err.Error()
		}
	}
	return "healthy"
}

func testBackendCAS(r *http.Request, b storage.Backend) error {
	ctx := r.Context()
	path := "_health/cas-probe"
	v1, err := b.Put(ctx, path, []byte("a"), "")
	if err != nil {
		if _, ver, gerr := b.Get(ctx, path); gerr == nil {
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
	switch t := b.(type) {
	case *storage.Git:
		return t.TestConnectivity(ctx)
	case *storage.S3:
		return t.TestConnectivity(ctx)
	case *storage.SQL:
		return t.TestConnectivity(ctx)
	}
	return nil
}

func (s *Server) migrateAllSpaces(r *http.Request, to config.Backend) (int, error) {
	from := s.Cfg.Backend
	if from.Driver == "" {
		from.Driver = storage.DriverLocal
	}
	if from.Driver == to.Driver {
		return 0, fmt.Errorf("already using %s", to.Driver)
	}
	names, err := s.Spaces.List()
	if err != nil {
		return 0, err
	}
	total := 0
	for _, name := range names {
		src, err := storage.Open(storage.OpenOptions{
			SpaceRoot: s.Spaces.SpaceRoot(name),
			SpaceName: name,
			Backend:   from,
			Driver:    from.Driver,
		})
		if err != nil {
			return total, fmt.Errorf("open source %s: %w", name, err)
		}
		dst, err := storage.Open(storage.OpenOptions{
			SpaceRoot: s.Spaces.SpaceRoot(name),
			SpaceName: name,
			Backend:   to,
			Driver:    to.Driver,
		})
		if err != nil {
			return total, fmt.Errorf("open dest %s: %w", name, err)
		}
		n, err := storage.Migrate(r.Context(), src, dst)
		if err != nil {
			return total, fmt.Errorf("migrate space %s: %w", name, err)
		}
		total += n
		logx.L().Info("space migrated", "space", name, "objects", n, "from", from.Driver, "to", to.Driver)
	}
	s.Cfg.Backend = to
	s.Spaces.Backend = to
	if err := config.SaveServer(s.Cfg); err != nil {
		return total, err
	}
	return total, nil
}
