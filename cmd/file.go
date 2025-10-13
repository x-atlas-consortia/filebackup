package cmd

import (
	"fmt"
	"log/slog"
	"time"

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

		listPath, err := cmd.Flags().GetString("path")
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

		return list.ListFiles(config, logLevel, listPath, outPath, t)
	},
}

func init() {
	fileCmd.AddCommand(filesListCmd)
	rootCmd.AddCommand(fileCmd)

	// Files list flags
	// path flag
	filesListCmd.Flags().StringP("path", "p", "", "Path of the directory to list files")
	filesListCmd.MarkFlagDirname("path")

	// out flag
	filesListCmd.Flags().StringP("out", "o", "", "Path to the output file (default: stdout)")
	filesListCmd.MarkFlagFilename("out")

	// time flag
	filesListCmd.Flags().StringP("time", "t", "", "List files that existed at a specific time (ISO 8601 format i.e. 2025-10-13T15:04:05-04:00) (default: current time)")
}
