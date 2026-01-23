package cmd

import (
	"errors"
	"log/slog"
	"os"
	"runtime"

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
	RunE: func(cmd *cobra.Command, args []string) error {
		config, ok := cmd.Context().Value("config").(core.Config)
		if !ok {
			panic("config not found in context")
		}

		logLevel, ok := cmd.Context().Value("log-level").(slog.Leveler)
		if !ok {
			panic("log-level not found in context")
		}

		profile, ok := cmd.Context().Value("profile").(string)
		if !ok {
			panic("profile not found in context")
		}

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

		details, err := cmd.Flags().GetString("details")
		if err != nil {
			return err
		}

		tempDir, err := cmd.Flags().GetString("temp-dir")
		if err != nil {
			return err
		}
		if stat, err := os.Stat(tempDir); err != nil || !stat.IsDir() {
			return errors.New("temp-dir must be a valid directory")
		}

		maxWorkers, err := cmd.Flags().GetInt("max-workers")
		if err != nil {
			return err
		}
		if maxWorkers < 1 {
			return errors.New("max-workers must be at least 1")
		}
		if maxWorkers > runtime.NumCPU() {
			return errors.New("max-workers cannot be greater than the number of CPU cores")
		}

		return restore.Restore(config, logLevel, manifest, outDir, details, tempDir, profile, maxWorkers)
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

		logLevel, ok := cmd.Context().Value("log-level").(slog.Leveler)
		if !ok {
			panic("log-level not found in context")
		}

		outPath, err := cmd.Flags().GetString("out")
		if err != nil {
			return err
		}

		if aws.IsS3Path(outPath) {
			bucket, _, err := aws.ParseS3Path(outPath)
			if err != nil {
				return err
			}
			if bucket != config.AWSS3Bucket {
				return errors.New("output S3 bucket does not match configured backup bucket")
			}
		}

		profile, ok := cmd.Context().Value("profile").(string)
		if !ok {
			panic("profile not found in context")
		}

		return list.ListRestores(config, logLevel, outPath, profile)
	},
}

var restoreStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Get the status of a restore",
	RunE: func(cmd *cobra.Command, args []string) error {
		config, ok := cmd.Context().Value("config").(core.Config)
		if !ok {
			panic("config not found in context")
		}

		logLevel, ok := cmd.Context().Value("log-level").(slog.Leveler)
		if !ok {
			panic("log-level not found in context")
		}

		profile, ok := cmd.Context().Value("profile").(string)
		if !ok {
			panic("profile not found in context")
		}

		// Validate manifest file path
		manifestPath, err := cmd.Flags().GetString("manifest")
		if err != nil {
			return err
		}
		manifest, err := aws.ParseManifestFile(manifestPath)
		if err != nil {
			return err
		}

		outPath, err := cmd.Flags().GetString("out")
		if err != nil {
			return err
		}

		if aws.IsS3Path(outPath) {
			bucket, _, err := aws.ParseS3Path(outPath)
			if err != nil {
				return err
			}
			if bucket != config.AWSS3Bucket {
				return errors.New("output S3 bucket does not match configured backup bucket")
			}
		}

		// Implementation for restore status would go here
		return list.ListRestoreStatus(config, logLevel, manifest, outPath, profile)
	},
}

func init() {
	restoreCmd.AddCommand(restoreStartCmd)
	restoreCmd.AddCommand(restoreListCmd)
	restoreCmd.AddCommand(restoreStatusCmd)
	rootCmd.AddCommand(restoreCmd)

	// Restore start flags
	// manifest flag
	restoreStartCmd.Flags().StringP("manifest", "m", "", "Path to the restore manifest file (required)")
	restoreStartCmd.MarkFlagRequired("manifest")
	restoreStartCmd.MarkFlagFilename("manifest")

	// out flag
	restoreStartCmd.Flags().StringP("out", "o", ".", "Path to the output directory where files will be restored (default is current directory)")
	restoreStartCmd.MarkFlagRequired("out")
	restoreStartCmd.MarkFlagDirname("out")

	// details flag
	restoreStartCmd.Flags().StringP("details", "d", "", "Details about the restore")

	// temp-dir flag
	restoreStartCmd.Flags().StringP("temp-dir", "t", os.TempDir(), "Path to the temporary directory")
	restoreStartCmd.MarkFlagDirname("temp-dir")

	// max-workers flag
	restoreStartCmd.Flags().IntP("max-workers", "w", 4, "Maximum number of concurrent workers")

	// Restore list flags
	// Add output file flag
	restoreListCmd.Flags().StringP("out", "o", "", "Path to the output file (default: stdout)")
	restoreListCmd.MarkFlagFilename("out")

	// Restore status flags
	// manifest flag
	restoreStatusCmd.Flags().StringP("manifest", "m", "", "Path to the restore manifest file (required)")
	restoreStatusCmd.MarkFlagRequired("manifest")
	restoreStatusCmd.MarkFlagFilename("manifest")

	// out flag
	restoreStatusCmd.Flags().StringP("out", "o", "", "Path to the output file (default: stdout)")
	restoreStatusCmd.MarkFlagFilename("out")
}
