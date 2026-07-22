package hooks

import "testing"

func TestScanAWSAndGitHub(t *testing.T) {
	data := []byte("key = AKIAIOSFODNN7EXAMPLE\nok\nghp_abcdefghijklmnopqrstuvwxyz0123456789\n")
	f := ScanBytes("x.md", data)
	if len(f) < 2 {
		t.Fatalf("want >=2 findings, got %+v", f)
	}
	cfg := Config{SecretScan: SecretScanConfig{Enabled: true, OnViolation: "block"}}
	if err := cfg.CheckPut("x.md", data); err == nil {
		t.Fatal("expected block")
	}
	cfg.SecretScan.OnViolation = "warn"
	if err := cfg.CheckPut("x.md", data); err != nil {
		t.Fatal(err)
	}
	cfg.SecretScan.Enabled = false
	if err := cfg.CheckPut("x.md", data); err != nil {
		t.Fatal(err)
	}
}

func TestNoFalsePositivePlain(t *testing.T) {
	f := ScanBytes("readme.md", []byte("# Hello\n\nNo secrets here.\n"))
	if len(f) != 0 {
		t.Fatalf("%+v", f)
	}
}
