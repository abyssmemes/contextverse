package storage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/abyssmemes/contextverse/internal/config"
)

// Driver names.
const (
	DriverLocal = "local"
	DriverGit   = "git"
	DriverS3    = "s3"
	DriverSQL   = "sql"
)

// OpenOptions selects and configures a backend for a space.
type OpenOptions struct {
	Driver    string // local|git|s3|sql
	SpaceRoot string // used by local/git for on-disk roots
	SpaceName string // isolates S3/SQL keys per space (required for shared backends)
	Backend   config.Backend
}

// Open returns a Backend for the space / server.
func Open(opts OpenOptions) (Backend, error) {
	driver := opts.Driver
	if driver == "" {
		driver = opts.Backend.Driver
	}
	if driver == "" {
		driver = DriverLocal
	}
	ctx := context.Background()

	switch driver {
	case DriverLocal:
		if opts.SpaceRoot == "" {
			return nil, fmt.Errorf("%w: empty space root", ErrInvalidArgument)
		}
		return OpenLocal(opts.SpaceRoot)

	case DriverGit:
		if opts.SpaceRoot == "" {
			return nil, fmt.Errorf("%w: empty space root for git", ErrInvalidArgument)
		}
		remote := opts.Backend.GitRemote
		autoPush := opts.Backend.GitAutoPush
		if remote != "" && !opts.Backend.GitAutoPush {
			// default: auto-push when remote configured unless explicitly false —
			// yaml false is indistinguishable; treat missing remote as off, and
			// if GitAutoPush is false but env CONTEXTVERSE_GIT_AUTOPUSH=0 keep off.
			// Prefer: if remote set, default autoPush true unless CONTEXTVERSE_GIT_AUTOPUSH=0.
			if os.Getenv("CONTEXTVERSE_GIT_AUTOPUSH") == "0" {
				autoPush = false
			} else {
				autoPush = true
			}
		}
		return OpenGit(GitConfig{
			LocalPath: filepath.Join(opts.SpaceRoot, localMetaDir, "git"),
			RemoteURL: remote,
			AutoPush:  autoPush,
			Auth: GitAuth{
				Username:          opts.Backend.GitUser,
				Token:             opts.Backend.GitToken,
				SSHPrivateKeyPath: opts.Backend.GitSSHKey,
			},
		})

	case DriverS3:
		access := opts.Backend.S3AccessKey
		secret := opts.Backend.S3SecretKey
		if access == "" {
			access = os.Getenv("CONTEXTVERSE_S3_ACCESS_KEY")
		}
		if access == "" {
			access = os.Getenv("AWS_ACCESS_KEY_ID")
		}
		if secret == "" {
			secret = os.Getenv("CONTEXTVERSE_S3_SECRET_KEY")
		}
		if secret == "" {
			secret = os.Getenv("AWS_SECRET_ACCESS_KEY")
		}
		bucket := opts.Backend.S3Bucket
		if bucket == "" {
			bucket = os.Getenv("CONTEXTVERSE_S3_BUCKET")
		}
		endpoint := opts.Backend.S3Endpoint
		if endpoint == "" {
			endpoint = os.Getenv("CONTEXTVERSE_S3_ENDPOINT")
		}
		prefix := opts.Backend.S3Prefix
		if opts.SpaceName != "" {
			if prefix != "" {
				prefix = strings.Trim(prefix, "/") + "/spaces/" + opts.SpaceName
			} else {
				prefix = "spaces/" + opts.SpaceName
			}
		}
		return OpenS3(ctx, S3Config{
			Endpoint:  endpoint,
			Region:    or(opts.Backend.S3Region, os.Getenv("AWS_REGION"), "us-east-1"),
			Bucket:    bucket,
			Prefix:    prefix,
			AccessKey: access,
			SecretKey: secret,
			PathStyle: opts.Backend.S3PathStyle || endpoint != "",
		})

	case DriverSQL:
		dsn := opts.Backend.SQLDSN
		if dsn == "" {
			dsn = os.Getenv("CONTEXTVERSE_SQL_DSN")
		}
		store, err := OpenSQL(ctx, SQLConfig{DSN: dsn})
		if err != nil {
			return nil, err
		}
		if opts.SpaceName != "" {
			return &Prefixed{Inner: store, Prefix: "spaces/" + opts.SpaceName}, nil
		}
		return store, nil

	default:
		return nil, fmt.Errorf("%w: unknown driver %q", ErrInvalidArgument, driver)
	}
}

// OpenFromConfig is a convenience wrapper.
func OpenFromConfig(spaceRoot string, b config.Backend) (Backend, error) {
	return Open(OpenOptions{SpaceRoot: spaceRoot, Backend: b, Driver: b.Driver})
}

// KnownDrivers returns driver ids supported in this build.
func KnownDrivers() []string {
	return []string{DriverLocal, DriverGit, DriverS3, DriverSQL}
}

func or(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
