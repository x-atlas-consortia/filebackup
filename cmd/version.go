package cmd

import (
	"github.com/spf13/cobra"
	"github.com/x-atlas-consortia/filebackup/internal/core"
	"github.com/x-atlas-consortia/filebackup/internal/list"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Versions operations",
}

var versionListCmd = &cobra.Command{
	Use:   "list",
	Short: "List versions",
	RunE: func(cmd *cobra.Command, args []string) error {
		config, ok := cmd.Context().Value("config").(core.Config)
		if !ok {
			panic("config not found in context")
		}

		listPath, err := cmd.Flags().GetString("path")
		if err != nil {
			return err
		}

		outPath, err := cmd.Flags().GetString("out")
		if err != nil {
			return err
		}

		return list.ListVersions(config, listPath, outPath)
	},
}

func init() {
	versionCmd.AddCommand(versionListCmd)
	rootCmd.AddCommand(versionCmd)

	// Add output file flag
	versionListCmd.Flags().StringP("path", "p", "", "Path of the file to list versions for")
	versionListCmd.MarkFlagFilename("path")

	// Add output file flag
	versionListCmd.Flags().StringP("out", "o", "", "Path to the output file (default: stdout)")
	versionListCmd.MarkFlagFilename("out")
}
