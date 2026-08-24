package client

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"

	"github.com/wow-look-at-my/remote-agent/protocol"
)

// Response printers: each daemon action renders to compact, greppable text
// (the --json flag bypasses all of this and prints the raw payload).

// printResponse outputs the daemon response to stdout.
// When OutputJSON is true, outputs indented JSON. Otherwise outputs compact text.
func printResponse(resp *protocol.DaemonResponse, action string) error {
	if resp.Error != "" {
		return fmt.Errorf("%s", resp.Error)
	}

	if OutputJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(resp.Data)
	}

	return printTextResponse(resp.Data, action)
}

// printTextResponse formats the response data as compact text based on the action type.
func printTextResponse(data any, action string) error {
	m, _ := data.(map[string]any)
	if m == nil {
		fmt.Fprintln(os.Stdout, data)
		return nil
	}

	switch action {
	case "exec":
		return printExecText(m)
	case "read":
		return printReadText(m)
	case "write", "upload", "download":
		return printWriteText(m)
	case "edit":
		return printEditText(m)
	case "ls":
		return printLsText(m)
	case "mount", "unmount":
		return printMountText(m)
	case "mounts":
		return printMountsText(m)
	case "glob":
		return printGlobText(m)
	case "grep":
		return printGrepText(m)
	case "readlink":
		return printReadlinkText(m)
	case "ps":
		return printPsText(m)
	case "sysinfo":
		return printSysinfoText(m)
	case "ping":
		return printPingText(m)
	case "disconnect":
		fmt.Fprintln(os.Stdout, "disconnecting")
		return nil
	default:
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(data)
	}
}

func printExecText(m map[string]any) error {
	stdout, _ := m["stdout"].(string)
	stderr, _ := m["stderr"].(string)

	// Each stream keeps its own channel, and cmd/exec.go carries the remote exit code.
	if stdout != "" {
		fmt.Fprint(os.Stdout, stdout)
	}
	if stderr != "" {
		fmt.Fprint(os.Stderr, stderr)
	}
	return nil
}

func printReadText(m map[string]any) error {
	// Binary files arrive base64-framed (JSON cannot carry invalid UTF-8);
	// decode back to the exact original bytes.
	if b64, _ := m["content_b64"].(string); b64 != "" {
		data, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return fmt.Errorf("decode content_b64: %w", err)
		}
		_, err = os.Stdout.Write(data)
		return err
	}
	content, _ := m["content"].(string)
	fmt.Fprint(os.Stdout, content)
	return nil
}

func printWriteText(m map[string]any) error {
	bytes, _ := m["bytes_written"].(float64)
	fmt.Fprintf(os.Stdout, "%d bytes written\n", int64(bytes))
	return nil
}

func printEditText(m map[string]any) error {
	modified, _ := m["modified"].(bool)
	msg, _ := m["message"].(string)
	if modified {
		fmt.Fprint(os.Stdout, "modified")
	} else {
		fmt.Fprint(os.Stdout, "not modified")
	}
	if msg != "" {
		fmt.Fprintf(os.Stdout, ": %s", msg)
	}
	fmt.Fprintln(os.Stdout)
	return nil
}

func printLsText(m map[string]any) error {
	entries, _ := m["entries"].([]any)
	if entries == nil {
		return nil
	}

	for _, e := range entries {
		entry, ok := e.(map[string]any)
		if !ok {
			continue
		}
		name, _ := entry["name"].(string)
		size, _ := entry["size"].(float64)
		mode, _ := entry["mode"].(string)
		isDir, _ := entry["is_dir"].(bool)
		isLink, _ := entry["is_link"].(bool)
		target, _ := entry["target"].(string)

		typeChar := "-"
		if isDir {
			typeChar = "d"
		}
		if isLink {
			typeChar = "l"
		}
		if isLink && target != "" {
			fmt.Fprintf(os.Stdout, "%s\t%s\t%d\t%s -> %s\n", typeChar, mode, int64(size), name, target)
		} else {
			fmt.Fprintf(os.Stdout, "%s\t%s\t%d\t%s\n", typeChar, mode, int64(size), name)
		}
	}
	return nil
}

func printMountText(m map[string]any) error {
	local, _ := m["local_path"].(string)
	remote, _ := m["remote_path"].(string)
	if mounted, _ := m["mounted"].(bool); mounted {
		fmt.Fprintf(os.Stdout, "mounted %s at %s\n", remote, local)
	} else {
		fmt.Fprintf(os.Stdout, "unmounted %s\n", local)
	}
	return nil
}

func printMountsText(m map[string]any) error {
	mounts, _ := m["mounts"].([]any)
	for _, entry := range mounts {
		mount, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		local, _ := mount["local_path"].(string)
		remote, _ := mount["remote_path"].(string)
		fmt.Fprintf(os.Stdout, "%s -> %s\n", local, remote)
	}
	return nil
}

func printGlobText(m map[string]any) error {
	files, _ := m["files"].([]any)
	for _, f := range files {
		if name, ok := f.(string); ok {
			fmt.Fprintln(os.Stdout, name)
		}
	}
	if truncated, _ := m["truncated"].(bool); truncated {
		fmt.Fprintln(os.Stderr, "(results truncated; raise --limit for more)")
	}
	return nil
}

func printGrepText(m map[string]any) error {
	mode, _ := m["mode"].(string)
	switch mode {
	case protocol.GrepModeFiles:
		for _, f := range sliceOf(m["files"]) {
			if name, ok := f.(string); ok {
				fmt.Fprintln(os.Stdout, name)
			}
		}
	case protocol.GrepModeCount:
		for _, c := range sliceOf(m["counts"]) {
			entry, ok := c.(map[string]any)
			if !ok {
				continue
			}
			path, _ := entry["path"].(string)
			count, _ := entry["count"].(float64)
			fmt.Fprintf(os.Stdout, "%s:%d\n", path, int(count))
		}
	default:
		for _, mm := range sliceOf(m["matches"]) {
			entry, ok := mm.(map[string]any)
			if !ok {
				continue
			}
			path, _ := entry["path"].(string)
			line, _ := entry["line"].(float64)
			text, _ := entry["text"].(string)
			// ':' marks a match and '-' a context line, as grep -C does.
			sep := ":"
			if isContext, _ := entry["is_context"].(bool); isContext {
				sep = "-"
			}
			fmt.Fprintf(os.Stdout, "%s%s%d%s%s\n", path, sep, int(line), sep, text)
		}
	}
	if truncated, _ := m["truncated"].(bool); truncated {
		fmt.Fprintln(os.Stderr, "(results truncated; raise --limit for more)")
	}
	return nil
}

func sliceOf(v any) []any {
	s, _ := v.([]any)
	return s
}

func printReadlinkText(m map[string]any) error {
	target, _ := m["target"].(string)
	fmt.Fprintln(os.Stdout, target)
	return nil
}

func printPsText(m map[string]any) error {
	processes, _ := m["processes"].([]any)
	if processes == nil {
		return nil
	}

	fmt.Fprintf(os.Stdout, "PID\tUSER\tSTATE\tRSS\tCOMMAND\n")
	for _, p := range processes {
		proc, ok := p.(map[string]any)
		if !ok {
			continue
		}
		pid, _ := proc["pid"].(float64)
		user, _ := proc["user"].(string)
		state, _ := proc["state"].(string)
		rss, _ := proc["rss_bytes"].(float64)
		cmd, _ := proc["command"].(string)
		fmt.Fprintf(os.Stdout, "%d\t%s\t%s\t%d\t%s\n", int(pid), user, state, int64(rss), cmd)
	}
	return nil
}

func printSysinfoText(m map[string]any) error {
	hostname, _ := m["hostname"].(string)
	osName, _ := m["os"].(string)
	arch, _ := m["arch"].(string)
	uptime, _ := m["uptime"].(string)

	fmt.Fprintf(os.Stdout, "%s %s %s up %s\n", hostname, osName, arch, uptime)

	if cpu, ok := m["cpu"].(map[string]any); ok {
		model, _ := cpu["model"].(string)
		cores, _ := cpu["cores"].(float64)
		threads, _ := cpu["threads"].(float64)
		mhz, _ := cpu["mhz"].(float64)
		fmt.Fprintf(os.Stdout, "cpu: %s %dc/%dt %.0fMHz\n", model, int(cores), int(threads), mhz)
	}

	if mem, ok := m["memory"].(map[string]any); ok {
		total, _ := mem["total_bytes"].(float64)
		avail, _ := mem["available_bytes"].(float64)
		fmt.Fprintf(os.Stdout, "mem: %.1fG total %.1fG avail\n", total/1e9, avail/1e9)
	}

	if disks, ok := m["disk"].([]any); ok {
		for _, d := range disks {
			disk, ok := d.(map[string]any)
			if !ok {
				continue
			}
			mount, _ := disk["mount_point"].(string)
			total, _ := disk["total_bytes"].(float64)
			pct, _ := disk["use_pct"].(float64)
			fmt.Fprintf(os.Stdout, "disk: %s %.1fG %.0f%%\n", mount, total/1e9, pct)
		}
	}

	return nil
}

func printPingText(m map[string]any) error {
	pong, _ := m["pong"].(bool)
	if pong {
		fmt.Fprintln(os.Stdout, "pong")
	} else {
		fmt.Fprintln(os.Stdout, "fail")
	}
	return nil
}
