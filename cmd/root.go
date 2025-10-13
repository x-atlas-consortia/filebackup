package cmd

import (
	"context"
	"errors"
	"os"

	"github.com/spf13/cobra"
	"github.com/x-atlas-consortia/filebackup/internal/core"
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "filebackup",
	Short: "FileBackup is a simple file backup tool",
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		exists := core.DoesConfigFileExist()
		if !exists {
			return errors.New("configuration file not found, please run 'filebackup init' to create one")
		}

		config, err := core.ParseConfigFile()
		if err != nil {
			return errors.New("failed to parse configuration file, please run 'filebackup init' to recreate it")
		}

		logLevelStr, err := cmd.Flags().GetString("log-level")
		if err != nil {
			return err
		}

		logLevel, err := core.ParseLogLevel(logLevelStr)
		if err != nil {
			return err
		}

		cmd.SetContext(context.WithValue(cmd.Context(), "config", config))
		cmd.SetContext(context.WithValue(cmd.Context(), "log-level", logLevel))

		return nil
	},
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

// Initialize persistent flags
func init() {
	rootCmd.PersistentFlags().StringP("log-level", "l", "info", "Set the log level (debug, info, warn, error)")
}
