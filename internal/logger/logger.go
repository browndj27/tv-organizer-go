package logger

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"tvorganizer/internal/config"
)

type Logger struct {
	Path   string
	buffer []string
	mu     sync.Mutex
	writer *bufio.Writer
	file   *os.File
}

func New(logDir string) (*Logger, error) {
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return nil, fmt.Errorf("create log directory: %w", err)
	}
	path := filepath.Join(logDir, fmt.Sprintf("tvfolderorganizer%d.log", time.Now().Unix()))
	f, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("create log file: %w", err)
	}
	return &Logger{
		Path:   path,
		writer: bufio.NewWriterSize(f, config.FileBufferSize),
		file:   f,
	}, nil
}

func (l *Logger) Write(msg string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buffer = append(l.buffer, msg)
	if len(l.buffer) >= config.LogBufferSize {
		l.flush()
	}
}

func (l *Logger) flush() {
	if l.writer == nil || len(l.buffer) == 0 {
		return
	}
	for _, line := range l.buffer {
		_, _ = fmt.Fprintln(l.writer, line)
	}
	l.buffer = l.buffer[:0]
	_ = l.writer.Flush()
}

func (l *Logger) Close() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.flush()
	if l.file != nil {
		_ = l.file.Close()
	}
}

func (l *Logger) Remove() {
	if l != nil && l.Path != "" {
		_ = os.Remove(l.Path)
	}
}
