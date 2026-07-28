package server

import (
	"html/template"

	"github.com/orkcom-tech/contextverse/internal/server/ui"
)

// The implementation moved to internal/server/ui so `contextd ui` renders space
// content through exactly the same code — including the decision to keep raw
// HTML escaped. These wrappers keep the call sites in this package unchanged.

func isMarkdownPath(p string) bool { return ui.IsMarkdownPath(p) }

func renderMarkdownHTML(src []byte) template.HTML { return ui.RenderMarkdown(src) }
