package ui

import (
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"sync"
)

//go:embed templates/*.html static/*
var content embed.FS

var (
	tplOnce sync.Once
	tpl     *template.Template
	tplErr  error
)

func templates() (*template.Template, error) {
	tplOnce.Do(func() {
		tpl, tplErr = template.New("").Funcs(template.FuncMap{
			"eq": func(a, b any) bool {
				return fmt.Sprint(a) == fmt.Sprint(b)
			},
		}).ParseFS(content, "templates/*.html")
	})
	return tpl, tplErr
}

// Static returns the embedded static file server.
func Static() http.Handler {
	sub, err := fs.Sub(content, "static")
	if err != nil {
		return http.NotFoundHandler()
	}
	return http.FileServer(http.FS(sub))
}

// Render executes a named template.
func Render(w http.ResponseWriter, name string, data any) error {
	t, err := templates()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return err
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	return t.ExecuteTemplate(w, name, data)
}

// Page is shared view model for authenticated pages.
type Page struct {
	Title      string
	User       string
	Role       string
	Flash      string
	FlashError string
	Active     string // nav key
	Version    string
	Data       any
}
