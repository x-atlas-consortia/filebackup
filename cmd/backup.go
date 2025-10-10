package cmd

import (
	"github.com/spf13/cobra"
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

		details, err := cmd.Flags().GetString("details")
		if err != nil {
			return err
		}

		return backup.Backup(config, details)
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

		outPath, err := cmd.Flags().GetString("out")
		if err != nil {
			return err
		}

		return list.ListBackups(config, outPath)
	},
}

func init() {
	backupCmd.AddCommand(backupStartCmd)
	backupCmd.AddCommand(backupListCmd)
	rootCmd.AddCommand(backupCmd)

	// Add details flag
	backupStartCmd.Flags().StringP("details", "d", "", "Details about the backup")

	// Add output file flag
	backupListCmd.Flags().StringP("out", "o", "", "Path to the output file (default: stdout)")
}
