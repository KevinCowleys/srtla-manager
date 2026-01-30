package logger

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
)

// Logger wraps the standard logger and adds file rotation and debug mode
type Logger struct {
	mu          sync.RWMutex
	debug       bool
	file        *os.File
	filePath    string
	maxSizeMB   int
	maxBackups  int
	currentSize int64
	stdLogger   *log.Logger
}

// New creates a new logger instance
func New(filePath string, maxSizeMB, maxBackups int, debug bool) (*Logger, error) {
	l := &Logger{
		debug:      debug,
		filePath:   filePath,
		maxSizeMB:  maxSizeMB,
		maxBackups: maxBackups,
	}

	// Create log directory if it doesn't exist
	if filePath != "" {
		dir := filepath.Dir(filePath)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create log directory: %w", err)
		}

		// Open log file
		if err := l.openFile(); err != nil {
			return nil, fmt.Errorf("failed to open log file: %w", err)
		}

		// Create multi-writer for both stdout and file
		multiWriter := io.MultiWriter(os.Stdout, l.file)
		l.stdLogger = log.New(multiWriter, "", log.LstdFlags)
	} else {
		// No file logging, just stdout
		l.stdLogger = log.New(os.Stdout, "", log.LstdFlags)
	}

	return l, nil
}

// openFile opens the log file for writing
func (l *Logger) openFile() error {
	file, err := os.OpenFile(l.filePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}

	// Get current file size
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return err
	}

	l.file = file
	l.currentSize = info.Size()
	return nil
}

// rotateIfNeeded checks if rotation is needed and performs it
func (l *Logger) rotateIfNeeded() error {
	if l.filePath == "" || l.maxSizeMB <= 0 {
		return nil
	}

	maxBytes := int64(l.maxSizeMB) * 1024 * 1024
	if l.currentSize < maxBytes {
		return nil
	}

	// Close current file
	if l.file != nil {
		l.file.Close()
	}

	// Rotate backups
	for i := l.maxBackups - 1; i >= 1; i-- {
		oldPath := fmt.Sprintf("%s.%d", l.filePath, i)
		newPath := fmt.Sprintf("%s.%d", l.filePath, i+1)
		os.Rename(oldPath, newPath)
	}

	// Move current file to .1
	if l.maxBackups > 0 {
		os.Rename(l.filePath, fmt.Sprintf("%s.1", l.filePath))
	}

	// Open new file
	if err := l.openFile(); err != nil {
		return err
	}

	// Update writer
	multiWriter := io.MultiWriter(os.Stdout, l.file)
	l.stdLogger.SetOutput(multiWriter)

	return nil
}

// write is the internal write method that handles rotation
func (l *Logger) write(prefix, format string, v ...interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()

	msg := fmt.Sprintf(format, v...)
	if prefix != "" {
		msg = prefix + " " + msg
	}

	l.stdLogger.Println(msg)

	// Update size
	if l.file != nil {
		l.currentSize += int64(len(msg) + 1) // +1 for newline
		l.rotateIfNeeded()
	}
}

// Printf writes a formatted message
func (l *Logger) Printf(format string, v ...interface{}) {
	l.write("", format, v...)
}

// Println writes a message with newline
func (l *Logger) Println(v ...interface{}) {
	l.write("", fmt.Sprint(v...))
}

// Debug writes a debug message (only if debug mode is enabled)
func (l *Logger) Debug(format string, v ...interface{}) {
	l.mu.RLock()
	debug := l.debug
	l.mu.RUnlock()

	if debug {
		l.write("[DEBUG]", format, v...)
	}
}

// Info writes an info message
func (l *Logger) Info(format string, v ...interface{}) {
	l.write("[INFO]", format, v...)
}

// Warn writes a warning message
func (l *Logger) Warn(format string, v ...interface{}) {
	l.write("[WARN]", format, v...)
}

// Error writes an error message
func (l *Logger) Error(format string, v ...interface{}) {
	l.write("[ERROR]", format, v...)
}

// Fatal writes a fatal message and exits
func (l *Logger) Fatal(format string, v ...interface{}) {
	l.write("[FATAL]", format, v...)
	os.Exit(1)
}

// SetDebug enables or disables debug mode
func (l *Logger) SetDebug(enabled bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.debug = enabled
	if enabled {
		l.stdLogger.Println("[INFO] Debug mode enabled")
	} else {
		l.stdLogger.Println("[INFO] Debug mode disabled")
	}
}

// IsDebug returns whether debug mode is enabled
func (l *Logger) IsDebug() bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.debug
}

// GetFilePath returns the current log file path
func (l *Logger) GetFilePath() string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.filePath
}

// Close closes the log file
func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.file != nil {
		return l.file.Close()
	}
	return nil
}

// Writer returns the underlying io.Writer for standard library compatibility
func (l *Logger) Writer() io.Writer {
	return l.stdLogger.Writer()
}

// Global logger instance
var (
	globalLogger *Logger
	globalMu     sync.RWMutex
)

// Init initializes the global logger
func Init(filePath string, maxSizeMB, maxBackups int, debug bool) error {
	globalMu.Lock()
	defer globalMu.Unlock()

	l, err := New(filePath, maxSizeMB, maxBackups, debug)
	if err != nil {
		return err
	}

	// Close old logger if exists
	if globalLogger != nil {
		globalLogger.Close()
	}

	globalLogger = l
	return nil
}

// Get returns the global logger instance
func Get() *Logger {
	globalMu.RLock()
	defer globalMu.RUnlock()
	return globalLogger
}

// Global convenience functions
func Printf(format string, v ...interface{}) {
	if l := Get(); l != nil {
		l.Printf(format, v...)
	}
}

func Println(v ...interface{}) {
	if l := Get(); l != nil {
		l.Println(v...)
	}
}

func Debug(format string, v ...interface{}) {
	if l := Get(); l != nil {
		l.Debug(format, v...)
	}
}

func Info(format string, v ...interface{}) {
	if l := Get(); l != nil {
		l.Info(format, v...)
	}
}

func Warn(format string, v ...interface{}) {
	if l := Get(); l != nil {
		l.Warn(format, v...)
	}
}

func Error(format string, v ...interface{}) {
	if l := Get(); l != nil {
		l.Error(format, v...)
	}
}

func Fatal(format string, v ...interface{}) {
	if l := Get(); l != nil {
		l.Fatal(format, v...)
	}
}

func SetDebug(enabled bool) {
	if l := Get(); l != nil {
		l.SetDebug(enabled)
	}
}

func IsDebug() bool {
	if l := Get(); l != nil {
		return l.IsDebug()
	}
	return false
}
