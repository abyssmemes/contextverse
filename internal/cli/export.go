package cli

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/abyssmemes/contextverse/internal/export"
	"github.com/abyssmemes/contextverse/internal/plugins"
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
		Long:  `Write a folder of markdown files ready to upload to closed web UIs (ChatGPT Knowledge, etc.).`,
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
			default:
				return fmt.Errorf("unknown export format %q (want: chatgpt)", format)
			}
		},
	}
	cmd.Flags().StringVar(&format, "format", "chatgpt", "export format (chatgpt)")
	cmd.Flags().StringVar(&outDir, "out", "", "output directory (default: ~/contextverse-export)")
	cmd.Flags().StringVar(&project, "project", "", "optional project name for 06-project.md")
	return cmd
}
