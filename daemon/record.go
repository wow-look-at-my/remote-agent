package daemon

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// TargetRecord remembers which SSH target a daemon socket belongs to.
type TargetRecord struct {
	// Target is canonical, port included: socket, PID file and this record are
	Target string `json:"target"`
	Port   int    `json:"port"`
	// ControlPath is the master the daemon rode, empty when it dialed the host
	ControlPath string `json:"control_path,omitempty"`
}

// TargetPath keys on the canonical target like SocketPath, so record and
// socket sit together.
func TargetPath(target string) string {
	h := sha256.Sum256([]byte(normalizeTarget(target)))
	return filepath.Join(os.TempDir(), fmt.Sprintf("remote-agent-%x.target", h[:6]))
}

// WriteTargetRecord records a daemon's target.
func WriteTargetRecord(rec TargetRecord) error {
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return os.WriteFile(TargetPath(rec.Target), data, 0600)
}

func ReadTargetRecord(path string) (TargetRecord, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return TargetRecord{}, err
	}
	var rec TargetRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return TargetRecord{}, fmt.Errorf("parse target record %s: %w", path, err)
	}
	if rec.Target == "" {
		return TargetRecord{}, fmt.Errorf("target record %s has no target", path)
	}
	return rec, nil
}

// TargetForSocket names the host behind a socket, so a discovered daemon can
// still be routed to by target.
func TargetForSocket(sockPath string) (TargetRecord, error) {
	return ReadTargetRecord(strings.TrimSuffix(sockPath, ".sock") + ".target")
}

// ListTargetRecords returns every known target, in unspecified order. Damaged
// records are skipped rather than failing the lookup.
func ListTargetRecords() []TargetRecord {
	matches, _ := filepath.Glob(filepath.Join(os.TempDir(), "remote-agent-*.target"))
	recs := make([]TargetRecord, 0, len(matches))
	for _, m := range matches {
		if rec, err := ReadTargetRecord(m); err == nil {
			recs = append(recs, rec)
		}
	}
	return recs
}
