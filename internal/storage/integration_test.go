//go:build integration

package storage

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"
)

func TestS3IntegrationCAS(t *testing.T) {
	endpoint := envOr("CONTEXTVERSE_S3_ENDPOINT", "http://127.0.0.1:9000")
	store, err := OpenS3(context.Background(), S3Config{
		Endpoint:  endpoint,
		Region:    "us-east-1",
		Bucket:    envOr("CONTEXTVERSE_S3_BUCKET", "contextverse"),
		Prefix:    "test/" + t.Name(),
		AccessKey: envOr("CONTEXTVERSE_S3_ACCESS_KEY", "minioadmin"),
		SecretKey: envOr("CONTEXTVERSE_S3_SECRET_KEY", "minioadmin"),
		PathStyle: true,
	})
	if err != nil {
		t.Skipf("s3 unavailable: %v", err)
	}
	runBackendCAS(t, store)
}

func TestSQLIntegrationCAS(t *testing.T) {
	dsn := envOr("CONTEXTVERSE_SQL_DSN", "postgres://contextverse:contextverse@127.0.0.1:5432/contextverse?sslmode=disable")
	store, err := OpenSQL(context.Background(), SQLConfig{DSN: dsn})
	if err != nil {
		t.Skipf("sql unavailable: %v", err)
	}
	defer store.Close()
	runBackendCAS(t, store)
}

func TestGitPrivateRemoteAuthConfig(t *testing.T) {
	a := GitAuth{Username: "git", Token: "ghp_test"}
	m, err := a.AuthMethod("https://github.com/abyssmemes/contextverse-backup.git")
	if err != nil || m == nil {
		t.Fatalf("auth: %v %v", m, err)
	}
}

func runBackendCAS(t *testing.T, store Backend) {
	t.Helper()
	ctx := context.Background()
	path := "integration/" + t.Name() + ".md"
	if data, ver, err := store.Get(ctx, path); err == nil {
		_ = data
		_ = store.Delete(ctx, path, ver)
	}
	v1, err := store.Put(ctx, path, []byte("one"), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put(ctx, path, []byte("stale"), ""); err == nil {
		t.Fatal("expected conflict")
	}
	v2, err := store.Put(ctx, path, []byte("two"), v1)
	if err != nil {
		t.Fatal(err)
	}
	scope := "it-" + t.Name() + "-" + randomSuffix()
	if err := store.SetHead(ctx, scope, "", Version("v1")); err != nil {
		t.Fatal(err)
	}
	h, err := store.Head(ctx, scope)
	if err != nil || h != "v1" {
		t.Fatalf("head: %q %v", h, err)
	}
	_ = store.Delete(ctx, path, v2)
}

func randomSuffix() string {
	return strconv.FormatInt(time.Now().UnixNano(), 36)
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
