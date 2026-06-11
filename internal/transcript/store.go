package transcript

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/YYYSSSRRR/codepilot/pkg/types"
)

// Store provides UUID-based dedup and batched message chain insertion
// for a single session transcript file.
//
// Create via NewStore; it holds a reference to the shared Project for
// async writes and a per-session UUID set loaded from the JSONL file.
type Store struct {
	project  *Project
	filePath string

	mu     sync.Mutex
	uuids  map[[sha256.Size]byte]bool // content-hash -> seen
	loaded bool
}

// NewStore creates a Store for a session file backed by the given Project.
func NewStore(project *Project, filePath string) *Store {
	return &Store{
		project:  project,
		filePath: filePath,
		uuids:    make(map[[sha256.Size]byte]bool),
	}
}

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

// RecordTranscript filters messages to those not yet seen (by content hash)
// and enqueues them for async batch writing. Returns the count of new messages.
//
// Safe for concurrent use -- callers may fire-and-forget from streaming paths
// or await from synchronous paths (e.g., user input submission).
func (s *Store) RecordTranscript(messages []types.Message) (int, error) {
	return s.insertMessageChain(messages)
}

// LoadMessages reads all message entries from the JSONL file and reconstructs
// the message list in order. Metadata entries are skipped. Returns an empty
// slice if the file does not exist.
func (s *Store) LoadMessages() ([]types.Message, error) {
	f, err := os.Open(s.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // fresh session, no messages yet
		}
		return nil, fmt.Errorf("open transcript: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 4*1024*1024)

	var messages []types.Message
	for scanner.Scan() {
		var entry Entry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			continue
		}
		if entry.Type != EntryMessage || entry.Role == "" {
			continue
		}

		var blocks []types.ContentBlock
		if len(entry.Content) > 0 {
			if err := json.Unmarshal(entry.Content, &blocks); err != nil {
				continue
			}
		}

		msg := types.Message{
			Role:    entry.Role,
			Content: blocks,
			Usage:   entry.Usage,
		}
		messages = append(messages, msg)
	}
	if err := scanner.Err(); err != nil {
		return messages, fmt.Errorf("read transcript: %w", err)
	}
	return messages, nil
}

// Flush forces all pending writes for this session to disk.
func (s *Store) Flush() {
	s.project.Flush()
}

// AppendMetadata synchronously writes a metadata entry to the session file.
// Bypasses the async queue; intended for session teardown only.
func (s *Store) AppendMetadata(key string, value any) error {
	return s.project.AppendMetadata(s.filePath, key, value)
}

// ---------------------------------------------------------------------------
// Message chain insertion
// ---------------------------------------------------------------------------

// insertMessageChain computes a content-hash UUID for each message,
// skips any whose hash is already known, and enqueues the rest.
func (s *Store) insertMessageChain(messages []types.Message) (int, error) {
	if err := s.ensureLoaded(); err != nil {
		return 0, fmt.Errorf("load uuids: %w", err)
	}

	var count int
	for _, msg := range messages {
		hash := messageHash(msg)
		s.mu.Lock()
		if s.uuids[hash] {
			s.mu.Unlock()
			continue
		}
		s.uuids[hash] = true
		s.mu.Unlock()

		contentJSON, err := json.Marshal(msg.Content)
		if err != nil {
			continue
		}

		entry := Entry{
			Type:      EntryMessage,
			UUID:      hex.EncodeToString(hash[:16]),
			Role:      msg.Role,
			Content:   contentJSON,
			Usage:     msg.Usage,
			Timestamp: time.Now().UnixMilli(),
		}
		s.project.AppendEntry(s.filePath, entry)
		count++
	}
	return count, nil
}

// ---------------------------------------------------------------------------
// UUID set loading (once, lazy)
// ---------------------------------------------------------------------------

func (s *Store) ensureLoaded() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.loaded {
		return nil
	}
	s.loaded = true

	f, err := os.Open(s.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // fresh file, nothing to load
		}
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	// Increase scanner buffer for potentially long JSON lines
	scanner.Buffer(make([]byte, 0, 1024*1024), 4*1024*1024)

	for scanner.Scan() {
		var entry Entry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			continue
		}
		if entry.Type != EntryMessage || entry.UUID == "" {
			continue
		}
		// Reconstruct the hash from stored UUID (only first 16 bytes stored)
		var hash [sha256.Size]byte
		data, err := hex.DecodeString(entry.UUID)
		if err == nil && len(data) == 16 {
			copy(hash[:], data)
			s.uuids[hash] = true
		}
	}
	return scanner.Err()
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// messageHash returns a deterministic SHA-256 hash of the message's role
// and content. This serves as a stable UUID for dedup across sessions.
func messageHash(msg types.Message) [sha256.Size]byte {
	h := sha256.New()
	h.Write([]byte(msg.Role))
	for _, block := range msg.Content {
		// Write type first so empty blocks of different types don't collide
		h.Write([]byte(block.Type))
		switch block.Type {
		case types.ContentBlockText:
			h.Write([]byte(block.Text))
		case types.ContentBlockToolUse:
			h.Write([]byte(block.ToolUseID))
			h.Write([]byte(block.Name))
			if block.Input != nil {
				enc := json.NewEncoder(h)
				enc.Encode(block.Input) //nolint: errcheck
			}
		case types.ContentBlockToolResult:
			h.Write([]byte(block.ToolUseID))
			h.Write([]byte(block.Content))
			if block.IsError {
				h.Write([]byte("error"))
			} else {
				h.Write([]byte("ok"))
			}
		case types.ContentBlockThinking:
			h.Write([]byte(block.Thinking))
		}
	}
	var out [sha256.Size]byte
	h.Sum(out[:0])
	return out
}

// ---------------------------------------------------------------------------
// SidechainStore
// ---------------------------------------------------------------------------

// SidechainStore records sub-agent transcripts to an independent file
// that does not share the main session's UUID space.
type SidechainStore struct {
	store *Store
}

// NewSidechainStore creates a store for a sub-agent transcript file.
func NewSidechainStore(project *Project, filePath string) *SidechainStore {
	return &SidechainStore{
		store: NewStore(project, filePath),
	}
}

// RecordTranscript delegates to the inner Store.
func (s *SidechainStore) RecordTranscript(messages []types.Message) (int, error) {
	return s.store.RecordTranscript(messages)
}

// Flush delegates to the inner Store.
func (s *SidechainStore) Flush() {
	s.store.Flush()
}
