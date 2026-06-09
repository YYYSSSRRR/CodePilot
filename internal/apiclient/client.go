package apiclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/YYYSSSRRR/codepilot/internal/types"
)

const DefaultBaseURL = "https://api.deepseek.com/anthropic"

// SSEEvent from the stream.
type SSEEvent struct {
	Type string
	Data json.RawMessage
}

// Stream reads SSE events from the Anthropic Messages API.
type Stream struct {
	reader *bufio.Reader
	body   io.ReadCloser
	closed bool
	event  string // current event type being assembled
}

// Recv reads the next SSE event. Returns io.EOF when the stream is done.
func (s *Stream) Recv() (*SSEEvent, error) {
	for {
		line, err := s.reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")

		switch {
		case strings.HasPrefix(line, "event: "):
			s.event = strings.TrimPrefix(line, "event: ")

		case strings.HasPrefix(line, "data: "):
			data := strings.TrimPrefix(line, "data: ")
			event := &SSEEvent{Type: s.event, Data: json.RawMessage(data)}
			s.event = ""
			// Handle the "data: [DONE]" sentinel for some SSE APIs
			if string(event.Data) == "[DONE]" {
				continue
			}
			return event, nil

		case line == "":
			continue

		default:
			continue
		}
	}
}

// Close closes the underlying response body.
func (s *Stream) Close() error {
	if !s.closed {
		s.closed = true
		return s.body.Close()
	}
	return nil
}

// Client manages HTTP connections to the Anthropic-compatible API.
type Client struct {
	apiKey  string
	baseURL string
	http    *http.Client
}

func NewClient(apiKey, baseURL string) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{
		apiKey:  apiKey,
		baseURL: baseURL,
		http:    http.DefaultClient,
	}
}

// StreamMessages establishes a streaming connection.
func (c *Client) StreamMessages(ctx context.Context, req types.APIRequest) (*Stream, error) {
	req.Stream = true

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	// Both auth headers for compatibility (Anthropic SDK uses x-api-key,
	// DeepSeek uses Authorization: Bearer).
	httpReq.Header.Set("x-api-key", c.apiKey)
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(respBody))
	}

	return &Stream{
		reader: bufio.NewReader(resp.Body),
		body:   resp.Body,
	}, nil
}