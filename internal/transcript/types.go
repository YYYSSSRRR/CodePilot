package transcript

import (
	"encoding/json"

	"github.com/YYYSSSRRR/codepilot/pkg/types"
)

// EntryType discriminates JSONL entry kinds.
type EntryType string

const (
	EntryMessage  EntryType = "message"
	EntryMetadata EntryType = "metadata"
)

// Entry is a single line in the JSONL transcript file.
// Each entry is either a message or a metadata key-value pair.
type Entry struct {
	Type EntryType `json:"type"`

	// Message fields
	UUID      string          `json:"uuid,omitempty"`
	Role      string          `json:"role,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
	Usage     *types.Usage    `json:"usage,omitempty"`
	Timestamp int64           `json:"timestamp,omitempty"`

	// Metadata fields
	Key   string `json:"key,omitempty"`
	Value any    `json:"value,omitempty"`
}