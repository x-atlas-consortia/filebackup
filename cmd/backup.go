package cmd

import (
	"errors"
	"log/slog"
	"os"
	"runtime"

	"github.com/spf13/cobra"
	"github.com/x-atlas-consortia/filebackup/internal/aws"
	"github.com/x-atlas-consortia/filebackup/internal/backup"
	"github.com/x-atlas-consortia/filebackup/internal/core"
	"github.com/x-atlas-consortia/filebackup/internal/list"
)

var backupCmd = &cobra.Command{
	Use:   "backup",
	Short: "Backup operations",
}

var backupStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start a new backup",
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

		directories, err := cmd.Flags().GetStringSlice("directories")
		if err != nil {
			return err
		}
		if len(directories) < 1 {
			return errors.New("at least one directory must be specified")
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
		if maxWorkers < 2 {
			return errors.New("max-workers must be at least 2")
		}
		if maxWorkers > runtime.NumCPU() {
			return errors.New("max-workers cannot be greater than the number of CPU cores")
		}

		return backup.Backup(cmd.Context(), config, logLevel, details, tempDir, profile, directories, maxWorkers)
	},
}

var backupListCmd = &cobra.Command{
	Use:   "list",
	Short: "List backups",
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

		return list.ListBackups(cmd.Context(), config, logLevel, outPath, profile)
	},
}

func init() {
	backupCmd.AddCommand(backupStartCmd)
	backupCmd.AddCommand(backupListCmd)
	rootCmd.AddCommand(backupCmd)

	// Backup start flags
	// directories flag
	backupStartCmd.Flags().StringSliceP("directories", "D", []string{}, "List of directories to back up (multiple -D flags allowed or comma-separated string)")
	backupStartCmd.MarkFlagRequired("directories")

	// details flag
	backupStartCmd.Flags().StringP("details", "d", "", "Details about the backup")

	// temp-dir flag
	backupStartCmd.Flags().StringP("temp-dir", "t", os.TempDir(), "Path to the temporary directory")
	backupStartCmd.MarkFlagDirname("temp-dir")

	// max-workers flag
	backupStartCmd.Flags().IntP("max-workers", "w", 4, "Maximum number of concurrent workers (default: 4)")

	// Backup list flags
	// out flag
	backupListCmd.Flags().StringP("out", "o", "", "Path to the output file (default: stdout)")
	backupListCmd.MarkFlagFilename("out")
}
