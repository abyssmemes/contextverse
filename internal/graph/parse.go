package graph

import (
	"bufio"
	"bytes"
	"path"
	"regexp"
	"strings"
)

// Link syntaxes accepted, in the order they are tried.
//
// All three, deliberately: the author's own vault runs on wikilinks, the
// ContextVerse templates use Markdown links and bare paths, and forcing either
// side to rewrite its content to be visible in the graph would guarantee the
// graph stays empty.
var (
	reWikilink = regexp.MustCompile(`\[\[([^\]|#]+)(?:#[^\]|]*)?(?:\|[^\]]*)?\]\]`)
	reMarkdown = regexp.MustCompile(`\[[^\]]*\]\(([^)\s]+)(?:\s+"[^"]*")?\)`)

	// A bare path needs a shape distinctive enough not to swallow prose: either
	// an explicit ./ or ../, or a slash plus a known extension. "the deploy
	// script" is not a link; "./scripts/deploy.sh" and "team/principles.md" are.
	reBarePath = regexp.MustCompile(`(?:^|[\s` + "`" + `(<])((?:\.{1,2}/)[^\s` + "`" + `)>,;]+|[A-Za-z0-9_.-]+(?:/[A-Za-z0-9_.-]+)+\.[A-Za-z0-9]{1,6})`)
)

// rawLink is a reference found in text, before it is resolved to a file.
type rawLink struct {
	Target string
	Line   int
	Kind   EdgeKind
}

// parseLinks extracts every outbound reference from a document.
//
// Fenced code blocks are read rather than skipped: a runbook's whole point is
// the command inside the fence, and `./scripts/deploy.sh` there is exactly the
// edge into code worth having. Inline code spans are likewise kept.
func parseLinks(content []byte) []rawLink {
	var out []rawLink
	seen := map[string]bool{} // one edge per target per line

	sc := bufio.NewScanner(bytes.NewReader(content))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	inFrontmatter := false
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()

		// Skip the YAML frontmatter block: its values are metadata, and a date
		// or an owner is not a link.
		if n == 1 && strings.TrimSpace(line) == "---" {
			inFrontmatter = true
			continue
		}
		if inFrontmatter {
			if strings.TrimSpace(line) == "---" {
				inFrontmatter = false
			}
			continue
		}

		add := func(target string, kind EdgeKind) {
			target = strings.TrimSpace(target)
			target = strings.Trim(target, "`\"'")
			if target == "" || isExternal(target) {
				return
			}
			key := target + "\x00" + string(rune(n))
			if seen[key] {
				return
			}
			seen[key] = true
			out = append(out, rawLink{Target: target, Line: n, Kind: kind})
		}

		for _, m := range reWikilink.FindAllStringSubmatch(line, -1) {
			add(m[1], EdgeWikilink)
		}
		for _, m := range reMarkdown.FindAllStringSubmatch(line, -1) {
			add(m[1], EdgeMarkdown)
		}
		for _, m := range reBarePath.FindAllStringSubmatch(line, -1) {
			add(m[1], EdgePath)
		}
	}
	return out
}

// isExternal filters out anything that does not point inside the space or the
// project — URLs, mail links, anchors and protocol handlers.
func isExternal(target string) bool {
	if strings.HasPrefix(target, "#") {
		return true
	}
	lower := strings.ToLower(target)
	for _, p := range []string{"http://", "https://", "mailto:", "ftp://", "tel:", "data:", "contextverse://"} {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	// A bare "example.com/page" is a URL missing its scheme far more often than
	// it is a file, and treating it as a broken link would be noise.
	if !strings.Contains(target, "/") {
		return false
	}
	first := strings.SplitN(target, "/", 2)[0]
	for _, tld := range []string{".com", ".org", ".net", ".io", ".dev", ".ai"} {
		if strings.HasSuffix(strings.ToLower(first), tld) {
			return true
		}
	}
	return false
}

// codeExtensions marks a target as pointing at code rather than context, which
// decides whether it is resolved inside the space or against a project anchor.
var codeExtensions = map[string]bool{
	".go": true, ".py": true, ".js": true, ".ts": true, ".tsx": true, ".jsx": true,
	".rs": true, ".rb": true, ".java": true, ".kt": true, ".c": true, ".h": true,
	".cpp": true, ".hpp": true, ".cs": true, ".php": true, ".swift": true,
	".sh": true, ".bash": true, ".zsh": true, ".fish": true, ".ps1": true,
	".sql": true, ".tf": true, ".hcl": true, ".proto": true,
	".yaml": true, ".yml": true, ".json": true, ".toml": true, ".ini": true,
	".dockerfile": true, ".mk": true, ".gradle": true,
}

func looksLikeCode(target string) bool {
	return codeExtensions[strings.ToLower(path.Ext(target))]
}
