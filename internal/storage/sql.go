package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/abyssmemes/contextverse/internal/logx"
)

// SQLConfig configures a Postgres storage backend.
type SQLConfig struct {
	DSN string // postgres://user:pass@host:5432/db?sslmode=disable
}

// SQL is a Postgres-backed store with row-level CAS.
type SQL struct {
	db *sql.DB
}

// OpenSQL opens a Postgres backend and ensures schema.
func OpenSQL(ctx context.Context, cfg SQLConfig) (*SQL, error) {
	if cfg.DSN == "" {
		return nil, fmt.Errorf("%w: empty sql dsn", ErrInvalidArgument)
	}
	db, err := sql.Open("pgx", cfg.DSN)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(10)
	db.SetConnMaxLifetime(time.Hour)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sql ping: %w", err)
	}
	s := &SQL{db: db}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *SQL) migrate(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS cv_objects (
  path TEXT PRIMARY KEY,
  version TEXT NOT NULL,
  data BYTEA NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TABLE IF NOT EXISTS cv_heads (
  scope TEXT PRIMARY KEY,
  version TEXT NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
`)
	return err
}

func (s *SQL) Name() string { return "sql" }

// Close closes the DB pool.
func (s *SQL) Close() error { return s.db.Close() }

func (s *SQL) Get(ctx context.Context, path string) ([]byte, Version, error) {
	var ver string
	var data []byte
	err := s.db.QueryRowContext(ctx,
		`SELECT version, data FROM cv_objects WHERE path = $1`, sanitizePath(path),
	).Scan(&ver, &data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", ErrNotFound
	}
	if err != nil {
		return nil, "", err
	}
	return data, Version(ver), nil
}

func (s *SQL) List(ctx context.Context, prefix string) ([]Entry, error) {
	q := `SELECT path, version FROM cv_objects`
	var args []any
	if prefix != "" {
		q += ` WHERE path LIKE $1`
		args = append(args, prefix+"%")
	}
	q += ` ORDER BY path`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Entry
	for rows.Next() {
		var e Entry
		var ver string
		if err := rows.Scan(&e.Path, &ver); err != nil {
			return nil, err
		}
		e.Version = Version(ver)
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *SQL) Put(ctx context.Context, path string, data []byte, expected Version) (Version, error) {
	path = sanitizePath(path)
	next := contentVersion(data)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	var actual string
	err = tx.QueryRowContext(ctx,
		`SELECT version FROM cv_objects WHERE path = $1 FOR UPDATE`, path,
	).Scan(&actual)
	if errors.Is(err, sql.ErrNoRows) {
		actual = ""
	} else if err != nil {
		return "", err
	}
	if Version(actual) != expected {
		return "", &ConflictError{Path: path, Expected: expected, Actual: Version(actual)}
	}
	if actual == "" {
		_, err = tx.ExecContext(ctx,
			`INSERT INTO cv_objects (path, version, data, updated_at) VALUES ($1,$2,$3,NOW())`,
			path, string(next), data,
		)
	} else {
		res, err2 := tx.ExecContext(ctx,
			`UPDATE cv_objects SET version=$1, data=$2, updated_at=NOW() WHERE path=$3 AND version=$4`,
			string(next), data, path, string(expected),
		)
		if err2 != nil {
			return "", err2
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			return "", &ConflictError{Path: path, Expected: expected, Actual: "concurrent"}
		}
		err = nil
	}
	if err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	logx.L().Debug("sql put", "path", path, "version", string(next))
	return next, nil
}

func (s *SQL) Delete(ctx context.Context, path string, expected Version) error {
	path = sanitizePath(path)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var actual string
	err = tx.QueryRowContext(ctx,
		`SELECT version FROM cv_objects WHERE path = $1 FOR UPDATE`, path,
	).Scan(&actual)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if Version(actual) != expected {
		return &ConflictError{Path: path, Expected: expected, Actual: Version(actual)}
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM cv_objects WHERE path=$1 AND version=$2`, path, string(expected))
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQL) Head(ctx context.Context, scope string) (Version, error) {
	scope = headScope(scope)
	var ver string
	err := s.db.QueryRowContext(ctx, `SELECT version FROM cv_heads WHERE scope=$1`, scope).Scan(&ver)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return Version(ver), nil
}

func (s *SQL) SetHead(ctx context.Context, scope string, expected, next Version) error {
	scope = headScope(scope)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var actual string
	err = tx.QueryRowContext(ctx,
		`SELECT version FROM cv_heads WHERE scope=$1 FOR UPDATE`, scope,
	).Scan(&actual)
	if errors.Is(err, sql.ErrNoRows) {
		actual = ""
	} else if err != nil {
		return err
	}
	if Version(actual) != expected {
		return &ConflictError{Path: "head:" + scope, Expected: expected, Actual: Version(actual)}
	}
	if actual == "" {
		_, err = tx.ExecContext(ctx,
			`INSERT INTO cv_heads (scope, version, updated_at) VALUES ($1,$2,NOW())`,
			scope, string(next),
		)
	} else {
		res, err2 := tx.ExecContext(ctx,
			`UPDATE cv_heads SET version=$1, updated_at=NOW() WHERE scope=$2 AND version=$3`,
			string(next), scope, string(expected),
		)
		if err2 != nil {
			return err2
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			return &ConflictError{Path: "head:" + scope, Expected: expected, Actual: "concurrent"}
		}
		err = nil
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

// TestConnectivity pings the database.
func (s *SQL) TestConnectivity(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

func headScope(scope string) string {
	s := sanitizePath(scope)
	if s == "" || s == "." {
		return "_root"
	}
	return s
}
