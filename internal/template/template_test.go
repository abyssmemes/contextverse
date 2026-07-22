package template

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestExtractTemplateFromTarGz(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	files := map[string]string{
		"contextverse-templates-main/templates/solo-default/context-entry.md": "# entry\n",
		"contextverse-templates-main/templates/solo-default/team/x.md":        "# x\n",
		"contextverse-templates-main/README.md":                               "skip\n",
	}
	for name, body := range files {
		hdr := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(body))}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(tw, body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}

	dest := t.TempDir()
	if err := extractTemplateFromTarGz(bytes.NewReader(buf.Bytes()), "templates/solo-default/", dest); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "context-entry.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "team", "x.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "README.md")); !os.IsNotExist(err) {
		t.Fatal("README should not be extracted")
	}
}

func TestResolveLocalPath(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "context-entry.md"), []byte("# hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Resolve(ResolveOptions{Path: dir})
	if err != nil {
		t.Fatal(err)
	}
	if res.Source != SourceLocalPath {
		t.Fatalf("source = %s", res.Source)
	}
}
