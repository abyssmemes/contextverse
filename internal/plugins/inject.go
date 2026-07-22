package plugins

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// InjectFormats lists known inject --format values.
func InjectFormats() []string {
	return []string{"claude-hook", "text"}
}

// Inject builds session-start payload for a format and writes to w (stdout typically).
func Inject(format, spaceRoot, cwd, project string) (string, error) {
	format = strings.TrimSpace(strings.ToLower(format))
	if project == "" {
		project = ResolveProject(spaceRoot, cwd)
	}
	body, err := entrySet(spaceRoot, project)
	if err != nil {
		return "", err
	}
	switch format {
	case "claude-hook", "claude":
		payload := map[string]any{
			"hookSpecificOutput": map[string]any{
				"hookEventName":      "SessionStart",
				"additionalContext":  body,
			},
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			return "", err
		}
		return string(raw) + "\n", nil
	case "text", "":
		return body, nil
	default:
		return "", fmt.Errorf("unknown inject format %q (want: %s)", format, strings.Join(InjectFormats(), "|"))
	}
}

func entrySet(spaceRoot, project string) (string, error) {
	if spaceRoot == "" {
		return "", fmt.Errorf("space root required")
	}
	files := []string{
		"context-entry.md",
		"identity/me.md",
		"team/principles.md",
		"space-index.md",
		"decisions.md",
	}
	if project != "" {
		files = append(files, filepath.Join("projects", project, "project.md"))
	}
	var b strings.Builder
	b.WriteString("# ContextVerse session context\n\n")
	b.WriteString(fmt.Sprintf("Space root: %s\n\n", spaceRoot))
	const maxTotal = 100_000
	n := 0
	for _, rel := range files {
		path := filepath.Join(spaceRoot, rel)
		raw, err := os.ReadFile(path)
		if err != nil {
			b.WriteString(fmt.Sprintf("## %s\n\n_(missing)_\n\n", rel))
			continue
		}
		chunk := string(raw)
		if n+len(chunk) > maxTotal {
			remain := maxTotal - n
			if remain < 0 {
				remain = 0
			}
			chunk = chunk[:remain] + "\n…(truncated)\n"
		}
		b.WriteString(fmt.Sprintf("## %s\n\n", rel))
		b.WriteString(chunk)
		if !strings.HasSuffix(chunk, "\n") {
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
		n += len(chunk)
		if n >= maxTotal {
			break
		}
	}
	return b.String(), nil
}
