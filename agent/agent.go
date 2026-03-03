package agent

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
)

// RunServe is the remote helper entry point. It dispatches to the appropriate action.
func RunServe(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: remote-agent serve <action> [flags]")
	}

	action := args[0]
	remaining := args[1:]

	logger := NewLogger()
	defer logger.Close()

	switch action {
	case "sysinfo":
		return serveSysinfo(logger)
	case "ps":
		return servePs(logger, remaining)
	case "edit":
		return serveEdit(logger, remaining)
	case "audit":
		return serveAudit(logger, remaining)
	default:
		return fmt.Errorf("unknown serve action: %s", action)
	}
}

func serveSysinfo(logger *Logger) error {
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

func servePs(logger *Logger, args []string) error {
	fs := flag.NewFlagSet("ps", flag.ContinueOnError)
	filter := fs.String("filter", "", "filter processes by name")
	if err := fs.Parse(args); err != nil {
		return err
	}

	logger.LogAction("ps", fmt.Sprintf("filter=%q", *filter))
	procs, err := GatherProcessList(*filter)
	if err != nil {
		logger.LogError("ps", err)
		writeJSONError(err)
		return nil
	}
	writeJSON(procs)
	return nil
}

func serveEdit(logger *Logger, args []string) error {
	fs := flag.NewFlagSet("edit", flag.ContinueOnError)
	path := fs.String("path", "", "file path to edit")
	oldText := fs.String("old", "", "text to find")
	newText := fs.String("new", "", "replacement text")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *path == "" || *oldText == "" {
		return fmt.Errorf("usage: remote-agent serve edit --path <file> --old <text> --new <text>")
	}

	logger.LogAction("edit", fmt.Sprintf("path=%s", *path))
	result, err := EditFile(*path, *oldText, *newText)
	if err != nil {
		logger.LogError("edit", err)
		writeJSONError(err)
		return nil
	}
	writeJSON(result)
	return nil
}

func serveAudit(logger *Logger, args []string) error {
	fs := flag.NewFlagSet("audit", flag.ContinueOnError)
	action := fs.String("action", "", "action to audit")
	detail := fs.String("detail", "", "action detail")
	user := fs.String("user", "", "connecting user")
	clientIP := fs.String("client-ip", "", "client IP address")
	fingerprint := fs.String("fingerprint", "", "SSH key fingerprint")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *action == "" {
		return fmt.Errorf("usage: remote-agent serve audit --action <action> [--detail <detail>]")
	}

	if *action == "startup" {
		logger.LogStartup(*user, *clientIP, *fingerprint)
		writeJSON(map[string]string{"status": "logged"})
		return nil
	}

	if *action == "shutdown" {
		logger.LogShutdown()
		writeJSON(map[string]string{"status": "logged"})
		return nil
	}

	logger.LogAction(*action, *detail)
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
