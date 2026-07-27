package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/abyssmemes/contextverse/internal/search"
	"github.com/abyssmemes/contextverse/internal/storage"
	"github.com/abyssmemes/contextverse/internal/textdiff"
)

func newSearchCmd() *cobra.Command {
	var (
		regex     bool
		caseSense bool
		pathGlob  string
		limit     int
		filesOnly bool
	)
	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Find text in your context space",
		Long: `Search paths and file contents in the space.

Case-insensitive substring by default; --regex for a pattern. This is the same
search the MCP server exposes to AI clients — which could look for things in
your space long before you could.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := resolveSpaceRoot()
			if err != nil {
				return err
			}
			res, err := search.Search(search.Options{
				Root:      root,
				Query:     strings.Join(args, " "),
				Regex:     regex,
				CaseSense: caseSense,
				PathGlob:  pathGlob,
				Limit:     limit,
			})
			if err != nil {
				return err
			}

			return emit(cmd.OutOrStdout(), res, func(w io.Writer) error {
				if len(res.Matches) == 0 {
					fmt.Fprintf(w, "no matches for %q in %d file(s)\n", res.Query, res.Scanned)
					return nil
				}
				if filesOnly {
					seen := map[string]bool{}
					for _, m := range res.Matches {
						if !seen[m.Path] {
							seen[m.Path] = true
							fmt.Fprintln(w, m.Path)
						}
					}
					return nil
				}
				for _, m := range res.Matches {
					if m.Name {
						fmt.Fprintf(w, "%s\t(filename)\n", m.Path)
						continue
					}
					fmt.Fprintf(w, "%s:%d\t%s\n", m.Path, m.Line, m.Text)
				}
				fmt.Fprintf(w, "\n%d match(es) in %d of %d file(s)\n", len(res.Matches), res.Files, res.Scanned)
				if res.Truncated {
					fmt.Fprintf(w, "stopped at the limit; raise it with --limit\n")
				}
				return nil
			})
		},
	}
	cmd.Flags().BoolVar(&regex, "regex", false, "treat the query as a regular expression")
	cmd.Flags().BoolVarP(&caseSense, "case-sensitive", "s", false, "match case exactly")
	cmd.Flags().StringVar(&pathGlob, "path", "", "only search paths matching this glob, e.g. 'team/*'")
	cmd.Flags().IntVar(&limit, "limit", 200, "stop after this many matches")
	cmd.Flags().BoolVarP(&filesOnly, "files", "l", false, "print matching paths only")
	return cmd
}

// DiffResult is the structured form of `file diff`.
type DiffResult struct {
	Path    string `json:"path" yaml:"path"`
	From    string `json:"from" yaml:"from"`
	To      string `json:"to" yaml:"to"`
	Added   int    `json:"added" yaml:"added"`
	Removed int    `json:"removed" yaml:"removed"`
	Diff    string `json:"diff" yaml:"diff"`
}

func newFileDiffCmd() *cobra.Command {
	var (
		from     int
		to       int
		context  int
		statOnly bool
	)
	cmd := &cobra.Command{
		Use:   "diff <path>",
		Short: "Show what changed between two versions of a file",
		Long: `Compare two versions of a file as a unified diff.

With no flags this compares the previous version to the current one — the usual
question, "what did that last change actually do". file history says a version
exists and file get prints one; this is what tells you what moved.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := args[0]
			fl, err := openFileLog()
			if err != nil {
				return err
			}
			ctx := cmd.Context()

			meta, versions, err := fl.ListVersions(ctx, path)
			if err != nil {
				return err
			}
			if meta == nil || meta.Current == 0 || len(versions) == 0 {
				return fmt.Errorf("%s has no version history — write it through contextd (file edit, file put) to start one", path)
			}
			current := meta.Current

			if to == 0 {
				to = current
			}
			if from == 0 {
				from = previousVersion(versions, to)
				if from == 0 {
					return fmt.Errorf("%s has only one version (v%d); nothing to compare it against", path, to)
				}
			}
			if from == to {
				return fmt.Errorf("--from and --to are both v%d", from)
			}

			oldBody, err := bodyAt(ctx, fl, path, from, current)
			if err != nil {
				return fmt.Errorf("v%d: %w", from, err)
			}
			newBody, err := bodyAt(ctx, fl, path, to, current)
			if err != nil {
				return fmt.Errorf("v%d: %w", to, err)
			}

			added, removed := textdiff.Stat(oldBody, newBody)
			out := DiffResult{
				Path:    path,
				From:    storage.DisplayVersion(storage.FormatFileVersion(from)),
				To:      storage.DisplayVersion(storage.FormatFileVersion(to)),
				Added:   added,
				Removed: removed,
			}
			if !statOnly {
				out.Diff = textdiff.Unified(oldBody, newBody,
					fmt.Sprintf("%s v%d", path, from),
					fmt.Sprintf("%s v%d", path, to), context)
			}

			return emit(cmd.OutOrStdout(), out, func(w io.Writer) error {
				if added == 0 && removed == 0 {
					fmt.Fprintf(w, "%s: v%d and v%d are identical\n", path, from, to)
					return nil
				}
				fmt.Fprintf(w, "%s  v%d → v%d  (+%d −%d)\n\n", path, from, to, added, removed)
				if statOnly {
					return nil
				}
				_, err := io.WriteString(w, out.Diff)
				return err
			})
		},
	}
	cmd.Flags().IntVar(&from, "from", 0, "older version (default: the one before --to)")
	cmd.Flags().IntVar(&to, "to", 0, "newer version (default: current)")
	cmd.Flags().IntVarP(&context, "context", "U", 3, "lines of context around each change")
	cmd.Flags().BoolVar(&statOnly, "stat", false, "counts only, no diff body")
	return cmd
}

// previousVersion is the highest live version below n, skipping destroyed ones
// whose content is gone and cannot be compared.
func previousVersion(versions []storage.FileVersionInfo, n int) int {
	best := 0
	for _, v := range versions {
		if v.Destroyed || v.Version >= n {
			continue
		}
		if v.Version > best {
			best = v.Version
		}
	}
	return best
}

func bodyAt(ctx context.Context, fl *storage.FileLog, path string, n, current int) (string, error) {
	if n == current {
		data, _, err := fl.Get(ctx, path)
		if err != nil && !errors.Is(err, storage.ErrNotFound) {
			return "", err
		}
		return string(data), nil
	}
	data, _, err := fl.GetVersion(ctx, path, n)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
