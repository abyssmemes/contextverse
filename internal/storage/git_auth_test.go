package storage

import (
	"testing"
)

func TestGitAuthHTTPSToken(t *testing.T) {
	t.Parallel()
	a := GitAuth{Username: "git", Token: "secret-pat"}
	m, err := a.AuthMethod("https://github.com/abyssmemes/private-repo.git")
	if err != nil {
		t.Fatal(err)
	}
	if m == nil {
		t.Fatal("expected auth method for private HTTPS")
	}
	m, err = a.AuthMethod("https://github.com/public/repo.git")
	if err != nil || m == nil {
		t.Fatalf("token should still apply: %v %v", m, err)
	}
	// no token → nil auth (public)
	m, err = GitAuth{}.AuthMethod("https://github.com/public/repo.git")
	if err != nil {
		t.Fatal(err)
	}
	if m != nil {
		t.Fatal("expected nil auth without token")
	}
}

func TestIsSSHRemote(t *testing.T) {
	t.Parallel()
	if !isSSHRemote("git@github.com:org/repo.git") {
		t.Fatal("git@")
	}
	if !isSSHRemote("ssh://git@github.com/org/repo.git") {
		t.Fatal("ssh://")
	}
	if isSSHRemote("https://github.com/org/repo.git") {
		t.Fatal("https")
	}
}
