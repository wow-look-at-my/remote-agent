package agent

import (
	"fmt"
	"os"
	"strings"

	"github.com/wow-look-at-my/remote-agent/protocol"
)

// EditFile performs a find/replace on a file.
//
// Unless replaceAll is set, oldText must appear exactly once: an ambiguous
// edit is rejected rather than silently applied to whichever occurrence came
// first, because the caller (a human at the CLI or a model through the MCP
// edit tool) cannot tell from a success message which one was changed.
func EditFile(path, oldText, newText string, replaceAll bool) (*protocol.EditResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read file %s: %w", path, err)
	}
	if oldText == newText {
		return nil, fmt.Errorf("edit %s: old and new text are identical", path)
	}

	content := string(data)
	occurrences := strings.Count(content, oldText)
	switch {
	case occurrences == 0:
		return nil, fmt.Errorf("text not found in %s", path)
	case occurrences > 1 && !replaceAll:
		return nil, fmt.Errorf("found %d occurrences in %s: include more surrounding context to make the text unique, or use replace-all", occurrences, path)
	}

	// Get original file permissions
	fi, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat file %s: %w", path, err)
	}

	replacements := 1
	newContent := strings.Replace(content, oldText, newText, 1)
	if replaceAll {
		replacements = occurrences
		newContent = strings.ReplaceAll(content, oldText, newText)
	}
	if err := os.WriteFile(path, []byte(newContent), fi.Mode()); err != nil {
		return nil, fmt.Errorf("write file %s: %w", path, err)
	}

	return &protocol.EditResult{
		Modified:     true,
		Message:      fmt.Sprintf("replaced %d occurrence(s) in %s", replacements, path),
		Replacements: replacements,
	}, nil
}
