package agent

import (
	"fmt"
	"os"
)

// Logger wraps logging for audit purposes. On Windows, falls back to stderr.
type Logger struct {
	fallback bool
}

// NewLogger creates a stderr-based logger on Windows.
func NewLogger() *Logger {
	return &Logger{fallback: true}
}

// LogStartup logs the initial connection audit entry.
func (l *Logger) LogStartup(user, clientIP, fingerprint string) {
	msg := fmt.Sprintf("STARTUP pid=%d user=%s client_ip=%s key_fp=%s",
		os.Getpid(), user, clientIP, fingerprint)
	l.info(msg)
}

// LogAction logs a general action.
func (l *Logger) LogAction(action, detail string) {
	msg := fmt.Sprintf("ACTION=%s %s", action, detail)
	l.info(msg)
}

// LogShutdown logs a shutdown event.
func (l *Logger) LogShutdown() {
	msg := fmt.Sprintf("SHUTDOWN pid=%d", os.Getpid())
	l.notice(msg)
}

// LogError logs an error.
func (l *Logger) LogError(action string, err error) {
	msg := fmt.Sprintf("ERROR action=%s error=%v", action, err)
	l.err(msg)
}

// Close is a no-op on Windows.
func (l *Logger) Close() {}

func (l *Logger) info(msg string) {
	fmt.Fprintf(os.Stderr, "[remote-agent] INFO: %s\n", msg)
}

func (l *Logger) notice(msg string) {
	fmt.Fprintf(os.Stderr, "[remote-agent] NOTICE: %s\n", msg)
}

func (l *Logger) err(msg string) {
	fmt.Fprintf(os.Stderr, "[remote-agent] ERR: %s\n", msg)
}
