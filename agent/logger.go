//go:build !windows

package agent

import (
	"fmt"
	"log/syslog"
	"os"
)

// Logger wraps syslog for transparent audit logging.
type Logger struct {
	writer *syslog.Writer
	// Fallback to stderr if syslog unavailable
	fallback bool
}

// NewLogger creates a syslog logger. Falls back to stderr if syslog is
// unavailable.
func NewLogger() *Logger {
	w, err := syslog.New(syslog.LOG_INFO|syslog.LOG_DAEMON, "remote-agent")
	if err != nil {
		return &Logger{fallback: true}
	}
	return &Logger{writer: w}
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

// Close closes the syslog connection.
func (l *Logger) Close() {
	if l.writer != nil {
		l.writer.Close()
	}
}

func (l *Logger) info(msg string) {
	if l.fallback {
		fmt.Fprintf(os.Stderr, "[remote-agent] INFO: %s\n", msg)
		return
	}
	l.writer.Info(msg)
}

func (l *Logger) notice(msg string) {
	if l.fallback {
		fmt.Fprintf(os.Stderr, "[remote-agent] NOTICE: %s\n", msg)
		return
	}
	l.writer.Notice(msg)
}

func (l *Logger) err(msg string) {
	if l.fallback {
		fmt.Fprintf(os.Stderr, "[remote-agent] ERR: %s\n", msg)
		return
	}
	l.writer.Err(msg)
}
