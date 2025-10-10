package cmd

import (
	"errors"

	"github.com/spf13/cobra"
	"github.com/x-atlas-consortia/filebackup/internal/core"
	"github.com/x-atlas-consortia/filebackup/internal/list"
)

var fileCmd = &cobra.Command{
	Use:   "file",
	Short: "Files operations",
}

var filesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List files",
	RunE: func(cmd *cobra.Command, args []string) error {
		config, ok := cmd.Context().Value("config").(core.Config)
		if !ok {
			panic("config not found in context")
		}

		listPath, err := cmd.Flags().GetString("path")
		if err != nil {
			return err
		}

		time, err := cmd.Flags().GetInt("time")
		if err != nil {
			return err
		}
		if time < 0 {
			return errors.New("time must be a positive integer")
		}

		outPath, err := cmd.Flags().GetString("out")
		if err != nil {
			return err
		}

		return list.ListFiles(config, listPath, outPath, time)
	},
}

func init() {
	fileCmd.AddCommand(filesListCmd)
	rootCmd.AddCommand(fileCmd)

	// Add output file flag
	filesListCmd.Flags().StringP("path", "p", "", "Path of the directory or file to list")
	filesListCmd.MarkFlagDirname("path")
	filesListCmd.MarkFlagFilename("path")

	// Add output file flag
	filesListCmd.Flags().StringP("out", "o", "", "Path to the output file (default: stdout)")
	filesListCmd.MarkFlagFilename("out")

	// Add time flag
	filesListCmd.Flags().IntP("time", "t", 0, "Time format (Unix timestamp)")
}
