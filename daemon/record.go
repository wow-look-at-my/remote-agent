package daemon

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// TargetRecord remembers which SSH target a daemon socket belongs to. Socket
// paths are a one-way hash of the target, so without this a client that finds
// a dead or missing socket has no way back to the target it should reconnect.
type TargetRecord struct {
	Target string `json:"target"`
	Port   int    `json:"port"`
}

// TargetPath returns the target-record path for a given target.
func TargetPath(target string) string {
	h := sha256.Sum256([]byte(target))
	return filepath.Join(os.TempDir(), fmt.Sprintf("remote-agent-%x.target", h[:6]))
}

// WriteTargetRecord records the target a daemon was started for. The record
// deliberately outlives the daemon: it is what lets the next command restart a
// daemon that idled out or died.
func WriteTargetRecord(target string, port int) error {
	data, err := json.Marshal(TargetRecord{Target: target, Port: port})
	if err != nil {
		return err
	}
	return os.WriteFile(TargetPath(target), data, 0600)
}

// ReadTargetRecord loads one record file.
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

// TargetForSocket returns the record for the daemon listening on sockPath.
// Socket and record are named from the same hash of the target, so a client
// that found a daemon by discovery can still name the host it is talking to --
// which is what lets it pass that target on to a tool call.
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
