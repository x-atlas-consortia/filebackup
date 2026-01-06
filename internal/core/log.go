package core

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

const logDirectory = "$HOME/.local/share/filebackup/logs"

// NewLogger creates a new slog.Logger that logs to both console and a file.
func NewLogger(cmd, profile string, level slog.Leveler) (*slog.Logger, io.Writer, error) {
	// Ensure log directory exists
	logDir := os.ExpandEnv(logDirectory)
	err := os.MkdirAll(logDir, 0700)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create log directory: %w", err)
	}

	// Format filename as fileback-YYYY-MM-DD-HH-MM-SS.log
	logFileName := fmt.Sprintf("filebackup-%s-%s-%s.log", cmd, profile, time.Now().UTC().Format("2006-01-02-15-04-05"))
	logFilePath := filepath.Join(logDir, logFileName)
	logFile, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create log file: %w", err)
	}

	// Create multi-writer for both console and file
	multiWriter := io.MultiWriter(os.Stdout, logFile)
	opts := &slog.HandlerOptions{
		Level:     level,
		AddSource: true,
	}
	handler := slog.NewTextHandler(multiWriter, opts)
	logger := slog.New(handler)

	return logger, multiWriter, nil
}

// ParseLogLevel converts a string representation of log level to slog.Leveler.
func ParseLogLevel(levelStr string) (slog.Leveler, error) {
	switch levelStr {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return nil, fmt.Errorf("invalid log level: %s", levelStr)
	}
}
