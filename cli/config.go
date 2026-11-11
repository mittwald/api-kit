package cli

import (
	"github.com/spf13/cobra"

	"github.com/mittwald/cli-kit/format"

	apikit "github.com/mittwald/api-kit"
	"github.com/mittwald/api-kit/definition"
	"github.com/mittwald/api-kit/output"
)

// configCommand returns the command that prints the definition the tool
// runs with: the built-in one with the user's files merged in.
func (a *App) configCommand(doc *definition.Document) *cobra.Command {
	return &cobra.Command{
		Use:   "config",
		Short: "Print the definition of the API, with the user's files merged in",
		Long: "Print the definition of the API the commands are generated from: the\n" +
			"built-in definition with the user's files merged in. The output is YAML,\n" +
			"or JSON with -o json.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			spec, err := a.globals.spec()
			if err != nil {
				return err
			}
			// The hint goes to the error stream, so that redirecting the
			// output into a file gives a clean document.
			for _, overlay := range a.opts.Overlays {
				writeLine(a.opts.Streams.Err, "# A file of the same shape extends this definition: "+overlay.Path)
			}
			if len(a.opts.Overlays) > 0 {
				writeLine(a.opts.Streams.Err, "# It can add to the definition; with --"+flagConfigOverride+" it can also replace values.")
			}
			switch spec.Format {
			case output.JSON, output.JSONRaw:
				return failed(format.WriteJSON(cmd.OutOrStdout(), doc.Raw()))
			case output.Text, output.YAML:
				return failed(format.WriteYAML(cmd.OutOrStdout(), doc.Raw()))
			}
			return apikit.Errorf(apikit.KindUsage, "the definition can be printed as yaml or json, not as %s", spec.Format)
		},
	}
}
