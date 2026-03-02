package agent

import (
	"fmt"
	"testing"
)

func TestNewLogger(t *testing.T) {
	logger := NewLogger()
	if logger == nil {
		t.Fatal("logger should not be nil")
	}
	defer logger.Close()
}

func TestLoggerFallback(t *testing.T) {
	// Force fallback mode
	logger := &Logger{fallback: true}

	// These should not panic
	logger.LogStartup("testuser", "10.0.0.1", "SHA256:abc")
	logger.LogAction("test", "detail")
	logger.LogShutdown()
	logger.LogError("test", fmt.Errorf("test error"))
	logger.Close()
}

func TestLoggerMethods(t *testing.T) {
	logger := NewLogger()
	defer logger.Close()

	// All methods should be safe to call regardless of syslog availability
	logger.LogStartup("user", "1.2.3.4", "SHA256:xyz")
	logger.LogAction("exec", "ls -la")
	logger.LogShutdown()
	logger.LogError("exec", fmt.Errorf("command failed"))
}
