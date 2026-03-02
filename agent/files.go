package agent

import (
	"fmt"
	"os"
	"strings"

	"github.com/wow-look-at-my/remote-agent/protocol"
)

// EditFile performs a find/replace on a file.
func EditFile(path, oldText, newText string) (*protocol.EditResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read file %s: %w", path, err)
	}

	content := string(data)
	if !strings.Contains(content, oldText) {
		return nil, fmt.Errorf("text not found in %s", path)
	}

	// Get original file permissions
	fi, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat file %s: %w", path, err)
	}

	newContent := strings.Replace(content, oldText, newText, 1)
	if err := os.WriteFile(path, []byte(newContent), fi.Mode()); err != nil {
		return nil, fmt.Errorf("write file %s: %w", path, err)
	}

	return &protocol.EditResult{
		Modified: true,
		Message:  fmt.Sprintf("replaced in %s", path),
	}, nil
}
