package template

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func TestExtractTreeFromTarGz(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	files := map[string]string{
		"contextverse-templates-main/client-integrations/foo/integration.yaml": "id: foo\n",
		"contextverse-templates-main/client-integrations/foo/payload.tmpl":     "hi",
		"contextverse-templates-main/templates/solo-default/context-entry.md":  "skip",
	}
	for name, body := range files {
		hdr := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(body))}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	_ = tw.Close()
	_ = gz.Close()

	dest := t.TempDir()
	if err := extractTreeFromTarGz(bytes.NewReader(buf.Bytes()), ClientIntegrationsPrefix, dest); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dest, "foo", "integration.yaml"))
	if err != nil || string(raw) != "id: foo\n" {
		t.Fatalf("%q %v", raw, err)
	}
}
