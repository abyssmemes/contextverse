// Package search finds text in a context space.
//
// It exists as its own package because two callers need the same answers: the
// CLI (`contextd search`) and the MCP server, which exposes search to the AI.
// Those were about to be two implementations of "what is in my space", which is
// exactly how a tool ends up telling a person and their assistant different
// things.
package search

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// Options controls a search.
type Options struct {
	Root       string // space root
	Query      string
	Regex      bool   // treat Query as a regular expression
	CaseSense  bool   // match case exactly (default is case-insensitive)
	PathGlob   string // only search paths matching this glob, e.g. "team/*"
	Limit      int    // max matches returned (0 = 200)
	MaxFileKiB int    // skip files larger than this (0 = 2048)
}

// Match is one hit. Line is 1-indexed; a match on the file's name rather than
// its contents has Line 0.
type Match struct {
	Path string `json:"path" yaml:"path"`
	Line int    `json:"line,omitempty" yaml:"line,omitempty"`
	Text string `json:"text,omitempty" yaml:"text,omitempty"`
	Name bool   `json:"name_match,omitempty" yaml:"name_match,omitempty"`
}

// Result is a completed search.
type Result struct {
	Query     string  `json:"query" yaml:"query"`
	Matches   []Match `json:"matches" yaml:"matches"`
	Files     int     `json:"files_matched" yaml:"files_matched"`
	Scanned   int     `json:"files_scanned" yaml:"files_scanned"`
	Truncated bool    `json:"truncated" yaml:"truncated"`
}

// Search walks the space and returns matches in path order.
func Search(opts Options) (*Result, error) {
	if strings.TrimSpace(opts.Query) == "" {
		return nil, fmt.Errorf("a query is required")
	}
	if opts.Root == "" {
		return nil, fmt.Errorf("space root is required")
	}
	if opts.Limit <= 0 {
		opts.Limit = 200
	}
	if opts.MaxFileKiB <= 0 {
		opts.MaxFileKiB = 2048
	}

	matcher, err := buildMatcher(opts)
	if err != nil {
		return nil, err
	}

	res := &Result{Query: opts.Query, Matches: []Match{}}
	seenFiles := map[string]bool{}

	walkErr := filepath.WalkDir(opts.Root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(opts.Root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if skipFile(rel) {
			return nil
		}
		if opts.PathGlob != "" {
			ok, err := path.Match(opts.PathGlob, rel)
			if err != nil {
				return fmt.Errorf("bad --path pattern %q: %w", opts.PathGlob, err)
			}
			if !ok {
				return nil
			}
		}
		res.Scanned++

		if len(res.Matches) >= opts.Limit {
			res.Truncated = true
			return filepath.SkipAll
		}

		// A name match is reported once, without a line, so "where is the file
		// about deploys" and "where is the word deploy" both work.
		if matcher(rel) {
			res.Matches = append(res.Matches, Match{Path: rel, Name: true})
			seenFiles[rel] = true
		}

		info, err := d.Info()
		if err != nil || info.Size() > int64(opts.MaxFileKiB)*1024 {
			return nil
		}
		if isBinary(p) {
			return nil
		}

		f, err := os.Open(p)
		if err != nil {
			return nil // unreadable file is not a reason to abandon the search
		}
		defer f.Close()

		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for n := 1; sc.Scan(); n++ {
			if len(res.Matches) >= opts.Limit {
				res.Truncated = true
				return filepath.SkipAll
			}
			line := sc.Text()
			if matcher(line) {
				res.Matches = append(res.Matches, Match{
					Path: rel,
					Line: n,
					Text: strings.TrimSpace(line),
				})
				seenFiles[rel] = true
			}
		}
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	res.Files = len(seenFiles)
	return res, nil
}

func buildMatcher(opts Options) (func(string) bool, error) {
	if opts.Regex {
		expr := opts.Query
		if !opts.CaseSense {
			expr = "(?i)" + expr
		}
		re, err := regexp.Compile(expr)
		if err != nil {
			return nil, fmt.Errorf("bad regular expression %q: %w", opts.Query, err)
		}
		return re.MatchString, nil
	}
	if opts.CaseSense {
		q := opts.Query
		return func(s string) bool { return strings.Contains(s, q) }, nil
	}
	q := strings.ToLower(opts.Query)
	return func(s string) bool { return strings.Contains(strings.ToLower(s), q) }, nil
}

// skipDir keeps the search inside content: contextd's own storage, version
// control and dependency trees are not the user's context.
func skipDir(name string) bool {
	switch name {
	case ".contextverse", ".sync", ".git", "node_modules", ".venv", "__pycache__":
		return true
	}
	return false
}

func skipFile(rel string) bool {
	if strings.HasPrefix(rel, ".") {
		return true
	}
	switch rel {
	case "config.yaml", "template.yaml":
		return true
	}
	return false
}

// isBinary samples the first bytes for a NUL, the cheap test that keeps grep
// output readable.
func isBinary(p string) bool {
	f, err := os.Open(p)
	if err != nil {
		return true
	}
	defer f.Close()
	buf := make([]byte, 512)
	n, _ := f.Read(buf)
	for _, b := range buf[:n] {
		if b == 0 {
			return true
		}
	}
	return false
}
