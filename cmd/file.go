package cmd

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/spf13/cobra"
	"github.com/x-atlas-consortia/filebackup/internal/aws"
	"github.com/x-atlas-consortia/filebackup/internal/core"
	"github.com/x-atlas-consortia/filebackup/internal/list"
)

var fileCmd = &cobra.Command{
	Use:   "file",
	Short: "Files operations",
}

var filesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List files at a given path",
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

		dirPath, err := cmd.Flags().GetString("directory")
		if err != nil {
			return err
		}

		timeStr, err := cmd.Flags().GetString("time")
		if err != nil {
			return err
		}
		var t time.Time
		if timeStr != "" {
			t, err = time.Parse(time.RFC3339, timeStr)
			if err != nil {
				return fmt.Errorf("invalid time format: %w", err)
			}
		} else {
			t = time.Now()
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

		manifest, err := cmd.Flags().GetBool("manifest")
		if err != nil {
			return err
		}

		return list.ListFiles(cmd.Context(), config, logLevel, dirPath, outPath, profile, t, manifest)
	},
}

var filesRandomCmd = &cobra.Command{
	Use:   "random",
	Short: "List latest version of a random set of files",
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

		number, err := cmd.Flags().GetInt("number")
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

		manifest, err := cmd.Flags().GetBool("manifest")
		if err != nil {
			return err
		}

		return list.ListRandomFiles(cmd.Context(), config, logLevel, outPath, profile, manifest, number)
	},
}

func init() {
	fileCmd.AddCommand(filesListCmd)
	fileCmd.AddCommand(filesRandomCmd)
	rootCmd.AddCommand(fileCmd)

	// Files list flags
	// path flag
	filesListCmd.Flags().StringP("directory", "D", "", "Path of the directory to list files")
	filesListCmd.MarkFlagDirname("directory")

	// out flag
	filesListCmd.Flags().StringP("out", "o", "", "Path to the output file (default: stdout)")
	filesListCmd.MarkFlagFilename("out")

	// time flag
	filesListCmd.Flags().StringP("time", "t", "", "List files that existed at a specific time (ISO 8601 format i.e. 2025-10-13T15:04:05-04:00) (default: current time)")

	// manifest flag
	filesListCmd.Flags().BoolP("manifest", "m", false, "Generate a manifest formatted output (default: false)")

	// Files random flags
	// out flag
	filesRandomCmd.Flags().StringP("out", "o", "", "Path to the output file (default: stdout)")
	filesRandomCmd.MarkFlagFilename("out")

	// number flag
	filesRandomCmd.Flags().IntP("number", "n", 100, "Number of random files to list (default: 100)")

	// manifest flag
	filesRandomCmd.Flags().BoolP("manifest", "m", false, "Generate a manifest formatted output (default: false)")
}
