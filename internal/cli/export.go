package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/orkcom-tech/contextverse/internal/export"
	"github.com/orkcom-tech/contextverse/internal/plugins"
)

func newExportCmd() *cobra.Command {
	var (
		format  string
		outDir  string
		project string
	)
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export context for manual AI upload (ChatGPT, …)",
		Long: `Package your context for a tool contextd cannot wire directly.

  --format chatgpt   a folder of numbered Markdown files, for ChatGPT Knowledge
                     and anything else that takes file uploads
  --format single    one Markdown document on stdout, for a chat box, a UI with
                     a single context field, or a colleague who asked

The single form goes to stdout so it composes:

    contextd export --format single | pbcopy
    contextd export --format single --out pack.md`,
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := resolveSpaceRoot()
			if err != nil {
				return err
			}
			switch format {
			case "chatgpt", "gpt":
				if project == "" {
					cwd, _ := filepath.Abs(".")
					project = plugins.ResolveProject(root, cwd)
				}
				res, err := export.ChatGPT(root, outDir, project)
				if err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Exported to %s\n", res.OutDir)
				for _, w := range res.Written {
					fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", w)
				}
				if len(res.Missing) > 0 {
					fmt.Fprintf(cmd.ErrOrStderr(), "note: %d source file(s) missing in space\n", len(res.Missing))
				}
				return nil
			case "single", "markdown", "md":
				if project == "" {
					cwd, _ := filepath.Abs(".")
					project = plugins.ResolveProject(root, cwd)
				}
				doc, res, err := export.Single(root, project)
				if err != nil {
					return err
				}
				// To stdout by default, so it composes: pipe it to a clipboard,
				// a file, or another tool. --out writes it instead.
				if outDir == "" {
					fmt.Fprint(cmd.OutOrStdout(), doc)
				} else {
					if err := os.WriteFile(outDir, []byte(doc), 0o644); err != nil {
						return err
					}
					fmt.Fprintf(cmd.OutOrStdout(), "Wrote %s (%d documents)\n", outDir, len(res.Written))
				}
				if len(res.Missing) > 0 {
					fmt.Fprintf(cmd.ErrOrStderr(), "note: %d source file(s) missing in space\n", len(res.Missing))
				}
				return nil
			default:
				return fmt.Errorf("unknown export format %q (want: chatgpt, single)", format)
			}
		},
	}
	cmd.Flags().StringVar(&format, "format", "chatgpt", "export format (chatgpt, single)")
	cmd.Flags().StringVar(&outDir, "out", "", "output directory, or file for --format single (default: ~/contextverse-export, stdout)")
	cmd.Flags().StringVar(&project, "project", "", "optional project name for 06-project.md")
	return cmd
}
