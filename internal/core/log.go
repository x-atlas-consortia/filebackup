package core

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

func NewLogger(dir, cmd string, level slog.Leveler) (*slog.Logger, io.Writer, error) {
	// Format filename as fileback-YYYY-MM-DD-HH-MM-SS.log
	logFileName := fmt.Sprintf("fileback-%s-%s.log", cmd, time.Now().UTC().Format("2006-01-02-15-04-05"))
	logFilePath := filepath.Join(dir, logFileName)
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
