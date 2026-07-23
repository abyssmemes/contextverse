package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestCompletionGenerates(t *testing.T) {
	root := newRoot()
	root.SetArgs([]string{"completion", "zsh"})
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "contextd") {
		t.Fatalf("unexpected completion output: %s", out[:min(200, len(out))])
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
