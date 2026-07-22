package server

import (
	"bytes"
	"html/template"
	"path"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer/html"
)

func isMarkdownPath(p string) bool {
	ext := strings.ToLower(path.Ext(p))
	return ext == ".md" || ext == ".markdown" || ext == ".mdown"
}

func renderMarkdownHTML(src []byte) template.HTML {
	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithRendererOptions(
			html.WithHardWraps(),
			// No WithUnsafe — untrusted space content stays escaped.
		),
	)
	var buf bytes.Buffer
	if err := md.Convert(src, &buf); err != nil {
		return template.HTML("<pre class=\"file-pre\">" + template.HTMLEscapeString(string(src)) + "</pre>")
	}
	return template.HTML(buf.String())
}
