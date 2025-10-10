package cmd

import (
	"context"
	"os"

	"github.com/spf13/cobra"
	"github.com/x-atlas-consortia/filebackup/internal/aws"
	"github.com/x-atlas-consortia/filebackup/internal/core"
	"github.com/x-atlas-consortia/filebackup/internal/list"
	"github.com/x-atlas-consortia/filebackup/internal/restore"
)

var restoreCmd = &cobra.Command{
	Use:   "restore",
	Short: "Restore operations",
}

var restoreStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start a new restore",
	PreRunE: func(cmd *cobra.Command, args []string) error {
		// Validate manifest file path
		manifestPath, err := cmd.Flags().GetString("manifest")
		if err != nil {
			return err
		}

		manifest, err := aws.ParseManifestFile(manifestPath)
		if err != nil {
			return err
		}

		// Validate output directory
		outDir, err := cmd.Flags().GetString("out")
		if err != nil {
			return err
		}
		if stat, err := os.Stat(outDir); err != nil || !stat.IsDir() {
			return err
		}

		cmd.SetContext(context.WithValue(cmd.Context(), "manifest", manifest))
		cmd.SetContext(context.WithValue(cmd.Context(), "out", outDir))

		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		config, ok := cmd.Context().Value("config").(core.Config)
		if !ok {
			panic("config not found in context")
		}

		manifest, ok := cmd.Context().Value("manifest").([]aws.ManifestItem)
		if !ok {
			panic("manifest not found in context")
		}

		outDir, ok := cmd.Context().Value("out").(string)
		if !ok {
			panic("out directory not found in context")
		}

		details, err := cmd.Flags().GetString("details")
		if err != nil {
			return err
		}

		return restore.Restore(config, manifest, outDir, details)
	},
}

var restoreListCmd = &cobra.Command{
	Use:   "list",
	Short: "List restores",
	RunE: func(cmd *cobra.Command, args []string) error {
		config, ok := cmd.Context().Value("config").(core.Config)
		if !ok {
			panic("config not found in context")
		}

		outPath, err := cmd.Flags().GetString("out")
		if err != nil {
			return err
		}

		return list.ListRestores(config, outPath)
	},
}

func init() {
	restoreCmd.AddCommand(restoreStartCmd)
	restoreCmd.AddCommand(restoreListCmd)
	rootCmd.AddCommand(restoreCmd)

	// Add manifest flag
	restoreStartCmd.Flags().StringP("manifest", "m", "", "Path to the restore manifest file (required)")
	restoreStartCmd.MarkFlagRequired("manifest")

	// Add restore path
	restoreStartCmd.Flags().StringP("out", "o", ".", "Path to the output directory where files will be restored (default is current directory)")
	restoreStartCmd.MarkFlagRequired("out")

	// Add details flag
	restoreStartCmd.Flags().StringP("details", "d", "", "Details about the restore")

	// Add output file flag
	restoreListCmd.Flags().StringP("out", "o", "", "Path to the output file (default: stdout)")
}
