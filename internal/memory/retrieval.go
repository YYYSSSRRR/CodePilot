package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// ClassifierFunc is a function that classifies which memory entries are relevant
// to a user query, using a small/fast model. Returns the subset of relevant entries.
type ClassifierFunc func(ctx context.Context, query string, entries []IndexEntry) ([]IndexEntry, error)

// Retriever finds memories relevant to a user query using a small model classifier.
type Retriever struct {
	store      *Store
	classifier ClassifierFunc
}

// NewRetriever creates a Retriever backed by the given Store and classifier.
func NewRetriever(store *Store, classifier ClassifierFunc) *Retriever {
	return &Retriever{store: store, classifier: classifier}
}

// FindRelevant returns memories whose index descriptions are classified as relevant
// to the query by the small model. At most maxResults are returned.
func (r *Retriever) FindRelevant(ctx context.Context, query string, maxResults int) ([]File, error) {
	entries, err := r.store.ReadIndex()
	if err != nil {
		return nil, fmt.Errorf("read memory index: %w", err)
	}
	if len(entries) == 0 {
		return nil, nil
	}

	// Use classifier to find relevant entries
	var relevant []IndexEntry
	if r.classifier != nil {
		relevant, err = r.classifier(ctx, query, entries)
		if err != nil {
			// Fall back to keyword matching on classifier failure
			relevant = keywordMatch(query, entries)
		}
	} else {
		relevant = keywordMatch(query, entries)
	}

	if len(relevant) > maxResults && maxResults > 0 {
		relevant = relevant[:maxResults]
	}

	var result []File
	for _, e := range relevant {
		f, err := r.store.ReadMemory(e.Name)
		if err != nil {
			continue
		}
		result = append(result, *f)
	}
	return result, nil
}

// ---------------------------------------------------------------------------
// LLM classification prompt
// ---------------------------------------------------------------------------

// ClassificationPrompt builds the prompt for the small model classifier.
func ClassificationPrompt(query string, entries []IndexEntry) string {
	var b strings.Builder
	b.WriteString("You are a memory retrieval system. Given a user's question and a list of memory descriptions, identify which memories are relevant to answering the question.\n\n")
	fmt.Fprintf(&b, "Question: %s\n\n", query)
	b.WriteString("Memories:\n")
	for i, e := range entries {
		fmt.Fprintf(&b, "%d. name=%s desc=%s\n", i+1, e.Name, e.Description)
	}
	b.WriteString("\nRespond with ONLY a JSON array of relevant memory names, e.g. [\"name1\", \"name2\"]. If none are relevant, respond with [].")
	return b.String()
}

// ParseClassification parses the model's response into relevant entry names.
func ParseClassification(response string) []string {
	response = strings.TrimSpace(response)
	// Try JSON array
	if strings.HasPrefix(response, "[") {
		var names []string
		if err := json.Unmarshal([]byte(response), &names); err == nil {
			return names
		}
	}
	// Fallback: comma or newline separated names
	response = strings.Trim(response, "[]\"' \n")
	var names []string
	for _, part := range strings.FieldsFunc(response, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r'
	}) {
		name := strings.Trim(strings.TrimSpace(part), "\"'")
		if name != "" {
			names = append(names, name)
		}
	}
	return names
}

// ---------------------------------------------------------------------------
// Keyword fallback
// ---------------------------------------------------------------------------

func keywordMatch(query string, entries []IndexEntry) []IndexEntry {
	queryTokens := tokenize(query)
	var scored []struct {
		entry IndexEntry
		score int
	}
	for _, e := range entries {
		descTokens := tokenize(e.Description)
		nameTokens := tokenize(e.Name)
		score := countOverlap(queryTokens, descTokens)*2 + countOverlap(queryTokens, nameTokens)
		if score > 0 {
			scored = append(scored, struct {
				entry IndexEntry
				score int
			}{entry: e, score: score})
		}
	}
	for i := 0; i < len(scored); i++ {
		for j := i + 1; j < len(scored); j++ {
			if scored[j].score > scored[i].score {
				scored[i], scored[j] = scored[j], scored[i]
			}
		}
	}
	var result []IndexEntry
	for _, s := range scored {
		result = append(result, s.entry)
	}
	return result
}

// ---------------------------------------------------------------------------
// Tokenization helpers
// ---------------------------------------------------------------------------

func tokenize(text string) []string {
	text = strings.ToLower(text)
	var words []string
	var cur []rune
	for _, r := range text {
		if isWordRune(r) {
			cur = append(cur, r)
		} else if len(cur) > 0 {
			if len(cur) > 1 {
				words = append(words, string(cur))
			}
			cur = nil
		}
	}
	if len(cur) > 1 {
		words = append(words, string(cur))
	}
	return words
}

func isWordRune(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-'
}

func countOverlap(a, b []string) int {
	set := make(map[string]bool, len(a))
	for _, t := range a {
		set[t] = true
	}
	n := 0
	for _, t := range b {
		if set[t] {
			n++
		}
	}
	return n
}
