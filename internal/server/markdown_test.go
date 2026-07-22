package server

import (
	"strings"
	"testing"
)

func TestRenderMarkdownHTML(t *testing.T) {
	html := string(renderMarkdownHTML([]byte("# Hi\n\n**x** and `y`")))
	if !strings.Contains(html, "<h1") || !strings.Contains(html, "Hi") {
		t.Fatalf("heading missing: %s", html)
	}
	if !strings.Contains(html, "<strong>") {
		t.Fatalf("bold missing: %s", html)
	}
	// No raw HTML from content
	evil := string(renderMarkdownHTML([]byte(`<script>alert(1)</script>`)))
	if strings.Contains(evil, "<script>") {
		t.Fatalf("unsafe HTML leaked: %s", evil)
	}
}

func TestIsMarkdownPath(t *testing.T) {
	if !isMarkdownPath("a/b.md") || isMarkdownPath("a.bin") {
		t.Fatal("ext check")
	}
}
