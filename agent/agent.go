package agent

import (
	"encoding/json"
	"fmt"
	"os"
)

// ServeSysinfo gathers and outputs system information.
func ServeSysinfo() error {
	logger := NewLogger()
	defer logger.Close()

	logger.LogAction("sysinfo", "gathering system info")
	info, err := GatherSystemInfo()
	if err != nil {
		logger.LogError("sysinfo", err)
		writeJSONError(err)
		return nil
	}
	writeJSON(info)
	return nil
}

// ServePs lists processes with optional name filter.
func ServePs(filter string) error {
	logger := NewLogger()
	defer logger.Close()

	logger.LogAction("ps", fmt.Sprintf("filter=%q", filter))
	procs, err := GatherProcessList(filter)
	if err != nil {
		logger.LogError("ps", err)
		writeJSONError(err)
		return nil
	}
	writeJSON(procs)
	return nil
}

// ServeEdit performs a find/replace edit on a file.
func ServeEdit(path, oldText, newText string) error {
	logger := NewLogger()
	defer logger.Close()

	logger.LogAction("edit", fmt.Sprintf("path=%s", path))
	result, err := EditFile(path, oldText, newText)
	if err != nil {
		logger.LogError("edit", err)
		writeJSONError(err)
		return nil
	}
	writeJSON(result)
	return nil
}

// ServeAudit logs an audit event.
func ServeAudit(action, detail, user, clientIP, fingerprint string) error {
	logger := NewLogger()
	defer logger.Close()

	if action == "startup" {
		logger.LogStartup(user, clientIP, fingerprint)
		writeJSON(map[string]string{"status": "logged"})
		return nil
	}

	if action == "shutdown" {
		logger.LogShutdown()
		writeJSON(map[string]string{"status": "logged"})
		return nil
	}

	logger.LogAction(action, detail)
	writeJSON(map[string]string{"status": "logged"})
	return nil
}

func writeJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(v)
}

func writeJSONError(err error) {
	json.NewEncoder(os.Stdout).Encode(map[string]string{
		"error": err.Error(),
	})
}
