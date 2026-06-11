package transcript

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ProjectDirHash returns a short hex hash of the working directory,
// used as the subdirectory name in ~/.codepilot/projects/<hash>/.
func ProjectDirHash(wd string) string {
	abs, err := filepath.Abs(wd)
	if err != nil {
		abs = wd
	}
	h := sha256.Sum256([]byte(abs))
	return fmt.Sprintf("%x", h[:16]) // 32 hex characters
}

// CodePilotDir returns $HOME/.codepilot.
func CodePilotDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".codepilot"), nil
}

// SessionPath returns the full path for a session JSONL file:
// ~/.codepilot/projects/<wdHash>/<sessionID>.jsonl
func SessionPath(wdHash, sessionID string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".codepilot", "projects", wdHash, sessionID+".jsonl"), nil
}

// ProjectsDir returns ~/.codepilot/projects/<wdHash>.
func ProjectsDir(wdHash string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".codepilot", "projects", wdHash), nil
}

// SessionInfo describes one saved session.
type SessionInfo struct {
	ID        string // filename without .jsonl extension
	Path      string
	MsgCount  int
}

// ListSessions returns all session files in the project directory, sorted
// by name descending (newest first). Message count is estimated by line
// count in the JSONL file.
func ListSessions(wdHash string) ([]SessionInfo, error) {
	dir, err := ProjectsDir(wdHash)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("list sessions: %w", err)
	}

	var sessions []SessionInfo
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".jsonl")
		info, _ := e.Info()
		// Count messages roughly by line count
		msgCount := 0
		if data, err := os.ReadFile(filepath.Join(dir, e.Name())); err == nil {
			msgCount = strings.Count(string(data), "\n")
		}
		_ = info
		sessions = append(sessions, SessionInfo{
			ID:       id,
			Path:     filepath.Join(dir, e.Name()),
			MsgCount: msgCount,
		})
	}

	// Sort newest first by name (timestamp-based IDs sort lexicographically)
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].ID > sessions[j].ID
	})
	return sessions, nil
}

// SidechainDir returns the subdirectory for sub-agent transcripts:
// ~/.codepilot/projects/<wdHash>/<sessionID>/subagents/
func SidechainDir(wdHash, sessionID string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".codepilot", "projects", wdHash, sessionID, "subagents"), nil
}