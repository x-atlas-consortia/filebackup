package cmd

import (
	"errors"
	"log/slog"

	"github.com/spf13/cobra"
	"github.com/x-atlas-consortia/filebackup/internal/aws"
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

		logLevel, ok := cmd.Context().Value("log-level").(slog.Leveler)
		if !ok {
			panic("log-level not found in context")
		}

		profile, ok := cmd.Context().Value("profile").(string)
		if !ok {
			panic("profile not found in context")
		}

		filePath, err := cmd.Flags().GetString("file")
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

		return list.ListVersions(cmd.Context(), config, logLevel, filePath, outPath, profile)
	},
}

func init() {
	versionCmd.AddCommand(versionListCmd)
	rootCmd.AddCommand(versionCmd)

	// Versions list flags
	// path flag
	versionListCmd.Flags().StringP("file", "f", "", "Path of the file to list versions for")
	versionListCmd.MarkFlagRequired("file")
	versionListCmd.MarkFlagFilename("file")

	// out flag
	versionListCmd.Flags().StringP("out", "o", "", "Path to the output file (default: stdout)")
	versionListCmd.MarkFlagFilename("out")
}
