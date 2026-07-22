package plugins

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/abyssmemes/contextverse/internal/logx"
)

// ApplyOpts controls detect → ask → apply behavior.
type ApplyOpts struct {
	// Interactive enables ask-when-none-detected (TTY prompts).
	Interactive bool
	In          io.Reader // prompts; default os.Stdin
	Out         io.Writer // prompt text; default os.Stderr
}

func (o ApplyOpts) in() io.Reader {
	if o.In != nil {
		return o.In
	}
	return os.Stdin
}

func (o ApplyOpts) out() io.Writer {
	if o.Out != nil {
		return o.Out
	}
	return os.Stderr
}

// Catalog loads all integrations from dirs (embedded + optional extra roots).
func LoadCatalog(dirs ...string) ([]*Integration, error) {
	var out []*Integration
	seen := map[string]bool{}
	for _, root := range dirs {
		if root == "" {
			continue
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			dir := filepath.Join(root, e.Name())
			in, err := LoadIntegration(dir)
			if err != nil {
				logx.L().Warn("skip integration", "dir", dir, "err", err)
				continue
			}
			if seen[in.ID] {
				continue
			}
			seen[in.ID] = true
			out = append(out, in)
		}
	}
	return out, nil
}

// ApplyDetected detects installed clients and applies each matching integration.
// If none detected: ask (when Interactive) or print manual fallback for the catalog.
func ApplyDetected(catalog []*Integration, vars Vars, opts ApplyOpts) ([]ApplyResult, error) {
	found := Detect(catalog, vars)
	if len(found) == 0 {
		var chosen []*Integration
		if opts.Interactive {
			chosen = AskWhich(catalog, opts.in(), opts.out())
		}
		if len(chosen) == 0 {
			fmt.Fprint(os.Stderr, ManualInstructionsCatalog(catalog, vars))
			return nil, nil
		}
		var results []ApplyResult
		for _, in := range chosen {
			res, err := Apply(in, vars)
			if err != nil {
				return results, fmt.Errorf("%s: %w", in.ID, err)
			}
			if res != nil {
				results = append(results, *res)
			}
			logx.L().Info("client chosen", "id", in.ID)
		}
		return results, nil
	}
	var results []ApplyResult
	for _, d := range found {
		res, err := Apply(d.Integration, vars)
		if err != nil {
			return results, fmt.Errorf("%s: %w", d.Integration.ID, err)
		}
		if res != nil {
			results = append(results, *res)
		}
		logx.L().Info("client detected", "id", d.Integration.ID, "via", d.How)
	}
	return results, nil
}

// AskWhich prompts for catalog IDs / numbers / "all" / empty (skip → manual).
func AskWhich(catalog []*Integration, in io.Reader, out io.Writer) []*Integration {
	if len(catalog) == 0 {
		return nil
	}
	fmt.Fprintln(out, "No AI client auto-detected. Which should contextd wire? (Enter = print manual instructions)")
	for i, integ := range catalog {
		fmt.Fprintf(out, "  %d) %s\t%s\n", i+1, integ.ID, integ.Display)
	}
	fmt.Fprint(out, "Select number(s), id(s), or 'all': ")
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil {
		return nil
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}
	if strings.EqualFold(line, "all") {
		return append([]*Integration(nil), catalog...)
	}
	byID := map[string]*Integration{}
	for _, integ := range catalog {
		byID[integ.ID] = integ
	}
	var outList []*Integration
	seen := map[string]bool{}
	for _, tok := range strings.FieldsFunc(line, func(r rune) bool {
		return r == ',' || r == ' ' || r == ';'
	}) {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		if n, err := strconv.Atoi(tok); err == nil && n >= 1 && n <= len(catalog) {
			in := catalog[n-1]
			if !seen[in.ID] {
				seen[in.ID] = true
				outList = append(outList, in)
			}
			continue
		}
		if in, ok := byID[tok]; ok && !seen[in.ID] {
			seen[in.ID] = true
			outList = append(outList, in)
		}
	}
	return outList
}

// ApplyByID applies a single catalog entry by id.
func ApplyByID(catalog []*Integration, id string, vars Vars) (*ApplyResult, error) {
	id = strings.TrimSpace(id)
	for _, in := range catalog {
		if in.ID == id {
			return Apply(in, vars)
		}
	}
	return nil, fmt.Errorf("unknown client-integration %q", id)
}
