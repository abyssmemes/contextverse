package ui

import (
	"bytes"
	"html/template"
	"path"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer/html"
)

// Markdown rendering lives here, beside the templates that display it, because
// two hosts render the same pages: the server console and `contextd ui` against
// a local space. One renderer means a document cannot look different depending
// on which one is serving it — and, more importantly, cannot be *safe* in one
// and unsafe in the other.

// IsMarkdownPath reports whether a path should be offered a rendered preview.
func IsMarkdownPath(p string) bool {
	ext := strings.ToLower(path.Ext(p))
	return ext == ".md" || ext == ".markdown" || ext == ".mdown"
}

// RenderMarkdown converts space content to HTML.
//
// Deliberately without goldmark's Unsafe option: space content is written by
// whoever can write to the space, so raw HTML in a document must stay escaped
// rather than execute in the console's origin.
func RenderMarkdown(src []byte) template.HTML {
	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithRendererOptions(
			html.WithHardWraps(),
		),
	)
	var buf bytes.Buffer
	if err := md.Convert(src, &buf); err != nil {
		return template.HTML("<pre class=\"file-pre\">" + template.HTMLEscapeString(string(src)) + "</pre>")
	}
	return template.HTML(buf.String())
}
