package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/abyssmemes/contextverse/internal/auth"
	"github.com/abyssmemes/contextverse/internal/authz"
	"github.com/abyssmemes/contextverse/internal/config"
)

func newPolicyCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "policy", Short: "Manage ACL policies on the server"}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List policy names",
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := openPolicyEngine()
			if err != nil {
				return err
			}
			for _, n := range eng.List() {
				fmt.Fprintln(cmd.OutOrStdout(), n)
			}
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "show <name>",
		Short: "Show a policy YAML",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := openPolicyEngine()
			if err != nil {
				return err
			}
			p, ok := eng.Get(args[0])
			if !ok {
				return fmt.Errorf("policy %q not found", args[0])
			}
			raw, err := yaml.Marshal(p)
			if err != nil {
				return err
			}
			fmt.Fprint(cmd.OutOrStdout(), string(raw))
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "write <name> [file|-]",
		Short: "Write a policy from YAML file (or stdin with -)",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := openPolicyEngine()
			if err != nil {
				return err
			}
			var raw []byte
			if len(args) == 1 || args[1] == "-" {
				raw, err = io.ReadAll(cmd.InOrStdin())
				if err != nil {
					return err
				}
			} else {
				raw, err = os.ReadFile(args[1])
				if err != nil {
					return err
				}
			}
			var p authz.Policy
			if err := yaml.Unmarshal(raw, &p); err != nil {
				return err
			}
			if p.Name == "" {
				p.Name = args[0]
			}
			if err := eng.Write(p); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "wrote policy %s\n", p.Name)
			return nil
		},
	})
	testCmd := &cobra.Command{
		Use:   "test",
		Short: "Test whether a user's policies allow a capability",
		RunE: func(cmd *cobra.Command, args []string) error {
			user, _ := cmd.Flags().GetString("user")
			path, _ := cmd.Flags().GetString("path")
			capName, _ := cmd.Flags().GetString("cap")
			if user == "" || path == "" {
				return fmt.Errorf("--user and --path required")
			}
			dir, err := resolveServerDir()
			if err != nil {
				return err
			}
			store, err := auth.OpenStore(dir)
			if err != nil {
				return err
			}
			users, err := store.ListUsers()
			if err != nil {
				return err
			}
			var pols []string
			for _, u := range users {
				if u.Name == user {
					pols = u.EffectivePolicies()
					break
				}
			}
			if len(pols) == 0 {
				return fmt.Errorf("user %q not found or has no policies", user)
			}
			eng, err := authz.Open(store.PoliciesDir())
			if err != nil {
				return err
			}
			cfg, _ := config.LoadServer(dir)
			def := "team"
			if cfg != nil && cfg.Defaults.Space != "" {
				def = cfg.Defaults.Space
			}
			ok := eng.Allow(pols, path, authz.Capability(capName), authz.Vars{"default": def})
			fmt.Fprintf(cmd.OutOrStdout(), "allow=%v user=%s policies=%v path=%s cap=%s\n", ok, user, pols, path, capName)
			if !ok {
				return fmt.Errorf("denied")
			}
			return nil
		},
	}
	testCmd.Flags().String("user", "", "username")
	testCmd.Flags().String("path", "", "ACL path")
	testCmd.Flags().String("cap", "read", "capability")
	cmd.AddCommand(testCmd)
	return cmd
}

func openPolicyEngine() (*authz.Engine, error) {
	dir, err := resolveServerDir()
	if err != nil {
		return nil, err
	}
	store, err := auth.OpenStore(dir)
	if err != nil {
		return nil, err
	}
	return authz.Open(store.PoliciesDir())
}

func remoteUserpassLogin(base, user, pass string) (string, error) {
	body, _ := json.Marshal(map[string]string{"username": user, "password": pass})
	req, err := http.NewRequest(http.MethodPost, base+"/api/v1/auth/userpass/login", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 15 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		return "", fmt.Errorf("login failed: %s", strings.TrimSpace(string(raw)))
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	if out.Token == "" {
		return "", fmt.Errorf("empty token in response")
	}
	return out.Token, nil
}
