package transcript

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	FlushIntervalMs = 100               // batch window in milliseconds
	MaxChunkBytes   = 100 * 1024 * 1024 // 100 MB max per single appendFile
	MaxQueueLen     = 1000              // max queued entries per file to prevent memory leak
)

// Project manages async batch writing of transcript entries to JSONL files.
// It holds per-file write queues and flushes them on a timer.
// Create via NewProject.
type Project struct {
	mu       sync.Mutex
	queues   map[string][]Entry
	draining bool
}

// NewProject creates a Project ready for use.
func NewProject() *Project {
	return &Project{
		queues: make(map[string][]Entry),
	}
}

// AppendEntry queues an entry for async batch writing.
// It is safe for concurrent use.
func (p *Project) AppendEntry(filePath string, entry Entry) {
	p.mu.Lock()

	q := p.queues[filePath]
	if len(q) >= MaxQueueLen {
		// Drop oldest to prevent memory leak
		q = q[1:]
	}
	p.queues[filePath] = append(q, entry)

	if !p.draining {
		p.draining = true
		time.AfterFunc(FlushIntervalMs*time.Millisecond, p.drain)
	}

	p.mu.Unlock()
}

// AppendMetadata synchronously writes a metadata entry to the file.
// Bypasses the async queue so that metadata is deterministically persisted
// even during session exit. Must only be called on session teardown.
func (p *Project) AppendMetadata(filePath string, key string, value any) error {
	entry := Entry{
		Type:  EntryMetadata,
		Key:   key,
		Value: value,
	}
	return appendSync(filePath, entry)
}

// Flush forces an immediate drain of all queues.
func (p *Project) Flush() {
	p.drain()
}

// Close flushes pending writes.
func (p *Project) Close() error {
	p.drain()
	return nil
}

// ---------------------------------------------------------------------------
// internal
// ---------------------------------------------------------------------------

// drain processes all queued entries across every file path.
func (p *Project) drain() {
	p.mu.Lock()
	queues := p.queues
	p.queues = make(map[string][]Entry)
	p.draining = false
	p.mu.Unlock()

	for filePath, entries := range queues {
		if len(entries) == 0 {
			continue
		}
		flushFile(filePath, entries)
	}
}

// flushFile writes a batch of entries to a single JSONL file.
func flushFile(filePath string, entries []Entry) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)

	for _, entry := range entries {
		if err := enc.Encode(entry); err != nil {
			continue // skip unencodable entries
		}
		if buf.Len() > MaxChunkBytes {
			break
		}
	}

	if buf.Len() == 0 {
		return
	}

	if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
		return
	}
	f, err := os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	// Best-effort; errors from short writes on a local file are extremely rare.
	_, _ = f.Write(buf.Bytes())
}

// appendSync immediately serializes and appends a single entry to the file.
func appendSync(filePath string, entry Entry) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(entry); err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(buf.Bytes())
	return err
}
