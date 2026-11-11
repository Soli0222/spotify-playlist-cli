package cli

import "github.com/spf13/cobra"

func newVersionCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "バージョンを表示",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return writeJSON(app.Stdout, map[string]string{"version": Version})
		},
	}
}
