package export

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ChatGPTFile is one numbered knowledge file in the export bundle.
type ChatGPTFile struct {
	Name string // e.g. 01-context-entry.md
	Rel  string // source path under space root
}

// ChatGPTFiles is the ordered upload set from the AI-integration note.
func ChatGPTFiles(project string) []ChatGPTFile {
	files := []ChatGPTFile{
		{Name: "01-context-entry.md", Rel: "context-entry.md"},
		{Name: "02-identity.md", Rel: "identity/me.md"},
		{Name: "03-principles.md", Rel: "team/principles.md"},
		{Name: "04-space-index.md", Rel: "space-index.md"},
		{Name: "05-decisions.md", Rel: "decisions.md"},
	}
	if project != "" {
		files = append(files, ChatGPTFile{
			Name: "06-project.md",
			Rel:  filepath.ToSlash(filepath.Join("projects", project, "project.md")),
		})
	}
	return files
}

// Result summarizes a ChatGPT export.
type Result struct {
	OutDir  string
	Written []string
	Missing []string
}

// ChatGPT writes a knowledge-upload bundle under outDir.
func ChatGPT(spaceRoot, outDir, project string) (*Result, error) {
	if spaceRoot == "" {
		return nil, fmt.Errorf("space root required")
	}
	if outDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		outDir = filepath.Join(home, "contextverse-export")
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, err
	}
	res := &Result{OutDir: outDir}
	for _, f := range ChatGPTFiles(project) {
		src := filepath.Join(spaceRoot, filepath.FromSlash(f.Rel))
		raw, err := os.ReadFile(src)
		dst := filepath.Join(outDir, f.Name)
		if err != nil {
			res.Missing = append(res.Missing, f.Rel)
			body := fmt.Sprintf("# %s\n\n_(missing from space: %s)_\n", f.Name, f.Rel)
			if err := os.WriteFile(dst, []byte(body), 0o644); err != nil {
				return nil, err
			}
			res.Written = append(res.Written, f.Name)
			continue
		}
		if err := os.WriteFile(dst, raw, 0o644); err != nil {
			return nil, err
		}
		res.Written = append(res.Written, f.Name)
	}
	readme := chatgptREADME(spaceRoot, project, res.Missing)
	if err := os.WriteFile(filepath.Join(outDir, "README.md"), []byte(readme), 0o644); err != nil {
		return nil, err
	}
	res.Written = append(res.Written, "README.md")
	return res, nil
}

func chatgptREADME(spaceRoot, project string, missing []string) string {
	var b strings.Builder
	b.WriteString("# ContextVerse → ChatGPT export\n\n")
	b.WriteString(fmt.Sprintf("Generated: %s\n\n", time.Now().UTC().Format(time.RFC3339)))
	b.WriteString(fmt.Sprintf("Space root: `%s`\n", spaceRoot))
	if project != "" {
		b.WriteString(fmt.Sprintf("Project: `%s`\n", project))
	}
	b.WriteString("\n## Upload\n\n")
	b.WriteString("Upload all `.md` files in this folder (except this README, unless you want it) as **Knowledge** / project files in ChatGPT.\n\n")
	b.WriteString("Suggested order: `01-…` through `05-…` (then `06-project.md` if present).\n\n")
	if len(missing) > 0 {
		b.WriteString("### Missing from space\n\n")
		for _, m := range missing {
			b.WriteString(fmt.Sprintf("- `%s`\n", m))
		}
		b.WriteString("\n")
	}
	b.WriteString("## System prompt\n\n")
	b.WriteString("Use this as the custom instructions / system prompt:\n\n")
	b.WriteString("---\n")
	b.WriteString(`You are an AI assistant with access to a ContextSpace.
Before answering ANY question, read the uploaded context files in order
(01-context-entry.md → identity → principles → space-index → decisions).
Follow importance weights from principles.md.
Never modify files without explicit permission.
`)
	b.WriteString("---\n")
	return b.String()
}
