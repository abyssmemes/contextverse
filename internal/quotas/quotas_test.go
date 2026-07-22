package quotas

import "testing"

func TestCheckFileAndSpace(t *testing.T) {
	c := Config{MaxFileSize: 100, MaxSpaceSize: 1000, MaxFiles: 3}
	if err := c.CheckFileSize(101); err == nil {
		t.Fatal("expected file size fail")
	}
	if err := c.CheckSpace(900, 2, 200, 1); err == nil {
		t.Fatal("expected space size fail")
	}
	if err := c.CheckSpace(100, 3, 1, 1); err == nil {
		t.Fatal("expected max files fail")
	}
	if err := c.CheckSpace(100, 1, 10, 1); err != nil {
		t.Fatal(err)
	}
}

func TestParseSize(t *testing.T) {
	n, err := ParseSize("5 MB")
	if err != nil || n != 5_000_000 {
		t.Fatalf("%d %v", n, err)
	}
	n, err = ParseSize("1MiB")
	if err != nil || n != 1<<20 {
		t.Fatalf("%d %v", n, err)
	}
}
