package storage

import (
	"fmt"
	"os"

	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
	sshgit "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"golang.org/x/crypto/ssh"
)

// GitAuth holds credentials for private remotes (HTTPS PAT or SSH key).
type GitAuth struct {
	// Username for HTTPS basic auth (GitHub: any non-empty, often the username or "git").
	Username string
	// Token / password for HTTPS (PAT). Also read from CONTEXTVERSE_GIT_TOKEN / GITHUB_TOKEN.
	Token string
	// SSHPrivateKeyPath is a PEM private key for git@ / ssh:// remotes.
	SSHPrivateKeyPath string
	// SSHPassword optional passphrase for the key.
	SSHPassword string
}

func (a GitAuth) resolveToken() string {
	if a.Token != "" {
		return a.Token
	}
	if v := os.Getenv("CONTEXTVERSE_GIT_TOKEN"); v != "" {
		return v
	}
	if v := os.Getenv("GITHUB_TOKEN"); v != "" {
		return v
	}
	if v := os.Getenv("GH_TOKEN"); v != "" {
		return v
	}
	return ""
}

// AuthMethod builds a go-git AuthMethod for the remote URL scheme.
func (a GitAuth) AuthMethod(remoteURL string) (transport.AuthMethod, error) {
	if remoteURL == "" {
		return nil, nil
	}
	if isSSHRemote(remoteURL) {
		keyPath := a.SSHPrivateKeyPath
		if keyPath == "" {
			if home, err := os.UserHomeDir(); err == nil {
				for _, cand := range []string{"id_ed25519", "id_rsa", "id_ecdsa"} {
					p := home + "/.ssh/" + cand
					if _, err := os.Stat(p); err == nil {
						keyPath = p
						break
					}
				}
			}
		}
		if keyPath == "" {
			// Fall back to ssh-agent
			auth, err := sshgit.NewSSHAgentAuth("git")
			if err != nil {
				return nil, fmt.Errorf("ssh auth: no key path and agent failed: %w", err)
			}
			return auth, nil
		}
		auth, err := sshgit.NewPublicKeysFromFile("git", keyPath, a.SSHPassword)
		if err != nil {
			return nil, fmt.Errorf("ssh key %s: %w", keyPath, err)
		}
		auth.HostKeyCallback = ssh.InsecureIgnoreHostKey() //nolint:gosec // local/dev convenience; harden later
		return auth, nil
	}
	// HTTPS / HTTP
	tok := a.resolveToken()
	if tok == "" {
		return nil, nil // public repo or cred helper elsewhere
	}
	user := a.Username
	if user == "" {
		user = "git"
	}
	return &http.BasicAuth{Username: user, Password: tok}, nil
}

func isSSHRemote(url string) bool {
	if len(url) >= 4 && url[:4] == "git@" {
		return true
	}
	if len(url) >= 6 && url[:6] == "ssh://" {
		return true
	}
	return false
}
