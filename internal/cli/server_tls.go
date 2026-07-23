package cli

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/abyssmemes/contextverse/internal/config"
)

func newServerTLSCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tls",
		Short: "TLS helpers (lab self-signed + Let's Encrypt ACME)",
	}
	cmd.AddCommand(newServerTLSGenCmd())
	cmd.AddCommand(newServerTLSACMECmd())
	return cmd
}

func newServerTLSACMECmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "acme",
		Short: "Let's Encrypt (ACME) for production TLS",
	}
	cmd.AddCommand(newServerTLSACMEEnableCmd())
	cmd.AddCommand(newServerTLSACMEStatusCmd())
	return cmd
}

func newServerTLSACMEEnableCmd() *cobra.Command {
	var (
		email    string
		domains  []string
		httpAddr string
	)
	cmd := &cobra.Command{
		Use:   "enable",
		Short: "Enable ACME in server config.yaml (HTTP-01)",
		Long: `Writes tls.enabled + tls.acme into config.yaml and clears static cert_file/key_file.

Requires a public hostname pointing at this host. HTTP-01 challenges are served
on --http-addr (default :80) unless the main listen port is 80.

SSO/OIDC is not configured here — that is ContextVerse Cloud only.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := resolveServerDir()
			if err != nil {
				return err
			}
			if !config.ServerExists(dir) {
				return fmt.Errorf("server not initialized at %s (run contextd init server)", dir)
			}
			if email == "" {
				return fmt.Errorf("--email is required")
			}
			if len(domains) == 0 {
				return fmt.Errorf("at least one --domain is required")
			}
			cfg, err := config.LoadServer(dir)
			if err != nil {
				return err
			}
			cfg.TLS.Enabled = true
			cfg.TLS.CertFile = ""
			cfg.TLS.KeyFile = ""
			cfg.TLS.ACME = config.ACMEConfig{
				Enabled:  true,
				Email:    email,
				Domains:  domains,
				HTTPAddr: httpAddr,
			}
			if err := cfg.TLS.Validate(); err != nil {
				return err
			}
			if err := config.SaveServer(cfg); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "updated %s\n", config.ServerConfigPathIn(dir))
			fmt.Fprintf(cmd.OutOrStdout(), "acme: email=%s domains=%v http_addr=%s\n", email, domains, orDefault(httpAddr, ":80"))
			fmt.Fprintf(cmd.OutOrStdout(), "restart: contextd server stop && contextd server start\n")
			return nil
		},
	}
	cmd.Flags().StringVar(&email, "email", "", "contact email for Let's Encrypt")
	cmd.Flags().StringArrayVar(&domains, "domain", nil, "hostname to obtain a cert for (repeatable)")
	cmd.Flags().StringVar(&httpAddr, "http-addr", ":80", "bind address for HTTP-01 challenges")
	return cmd
}

func newServerTLSACMEStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show ACME / TLS settings from server config",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := resolveServerDir()
			if err != nil {
				return err
			}
			if !config.ServerExists(dir) {
				return fmt.Errorf("server not initialized at %s", dir)
			}
			cfg, err := config.LoadServer(dir)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "tls.enabled: %v\n", cfg.TLS.Enabled)
			if cfg.TLS.ACME.Enabled {
				cache := filepath.Join(dir, "tls", "acme")
				if cfg.TLS.ACME.CacheDir != "" {
					cache = cfg.TLS.ACME.CacheDir
				}
				httpAddr := cfg.TLS.ACME.HTTPAddr
				if httpAddr == "" {
					httpAddr = ":80"
				}
				fmt.Fprintf(out, "tls.acme.enabled: true\n")
				fmt.Fprintf(out, "tls.acme.email: %s\n", cfg.TLS.ACME.Email)
				fmt.Fprintf(out, "tls.acme.domains: %v\n", cfg.TLS.ACME.Domains)
				fmt.Fprintf(out, "tls.acme.cache_dir: %s\n", cache)
				fmt.Fprintf(out, "tls.acme.http_addr: %s\n", httpAddr)
				return nil
			}
			fmt.Fprintf(out, "tls.acme.enabled: false\n")
			if cfg.TLS.CertFile != "" {
				fmt.Fprintf(out, "tls.cert_file: %s\n", cfg.TLS.CertFile)
				fmt.Fprintf(out, "tls.key_file: %s\n", cfg.TLS.KeyFile)
			}
			return nil
		},
	}
	return cmd
}

func newServerTLSGenCmd() *cobra.Command {
	var (
		outDir string
		host   string
		days   int
	)
	cmd := &cobra.Command{
		Use:   "gen",
		Short: "Generate a self-signed cert+key for lab TLS",
		Long: `Writes cert.pem and key.pem under the server data dir (or --out).
Then set in config.yaml:

  tls:
    enabled: true
    cert_file: <path>/cert.pem
    key_file: <path>/key.pem

Lab only. For production Let's Encrypt: contextd server tls acme enable …`,
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := resolveServerDir()
			if err != nil {
				return err
			}
			if outDir == "" {
				outDir = filepath.Join(dir, "tls")
			}
			if err := os.MkdirAll(outDir, 0o700); err != nil {
				return err
			}
			certPath := filepath.Join(outDir, "cert.pem")
			keyPath := filepath.Join(outDir, "key.pem")
			if host == "" {
				host = "localhost"
			}
			if days <= 0 {
				days = 365
			}
			if err := writeSelfSigned(certPath, keyPath, host, days); err != nil {
				return err
			}
			// Optionally patch existing config if present.
			if config.ServerExists(dir) {
				cfg, err := config.LoadServer(dir)
				if err == nil {
					cfg.TLS.Enabled = true
					cfg.TLS.CertFile = certPath
					cfg.TLS.KeyFile = keyPath
					cfg.TLS.ACME = config.ACMEConfig{} // mutual exclusion with static files
					_ = config.SaveServer(cfg)
					fmt.Fprintf(cmd.OutOrStdout(), "updated %s tls.enabled=true\n", config.ServerConfigPathIn(dir))
				}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "wrote %s\nwrote %s\n", certPath, keyPath)
			fmt.Fprintf(cmd.OutOrStdout(), "restart: contextd server stop && contextd server start\n")
			return nil
		},
	}
	cmd.Flags().StringVar(&outDir, "out", "", "output directory (default: <server-dir>/tls)")
	cmd.Flags().StringVar(&host, "host", "localhost", "DNS/IP SAN for the certificate")
	cmd.Flags().IntVar(&days, "days", 365, "validity days")
	return cmd
}

func writeSelfSigned(certPath, keyPath, host string, days int) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{Organization: []string{"ContextVerse lab"}, CommonName: host},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Duration(days) * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{host},
	}
	if ip := net.ParseIP(host); ip != nil {
		tmpl.IPAddresses = []net.IP{ip}
		tmpl.DNSNames = nil
	} else if host == "localhost" {
		tmpl.IPAddresses = []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return err
	}
	certOut, err := os.OpenFile(certPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if err := pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: der}); err != nil {
		_ = certOut.Close()
		return err
	}
	_ = certOut.Close()

	keyBytes, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	keyOut, err := os.OpenFile(keyPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if err := pem.Encode(keyOut, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes}); err != nil {
		_ = keyOut.Close()
		return err
	}
	return keyOut.Close()
}
