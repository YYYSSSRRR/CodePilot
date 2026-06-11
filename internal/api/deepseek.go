package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const DefaultBaseURL = "https://api.deepseek.com/anthropic"

type deepseekStream struct {
	reader *bufio.Reader
	body   io.ReadCloser
	closed bool
	event  string
}

func (s *deepseekStream) Recv() (*StreamEvent, error) {
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
			raw := json.RawMessage(data)
			ev := &StreamEvent{Type: s.event, Data: raw}
			s.event = ""
			if string(raw) == "[DONE]" {
				continue
			}
			return ev, nil

		case line == "":
			continue

		default:
			continue
		}
	}
}

func (s *deepseekStream) Close() error {
	if !s.closed {
		s.closed = true
		return s.body.Close()
	}
	return nil
}

// DeepSeek implements the Client interface for DeepSeek's Anthropic-compatible API.
type DeepSeek struct {
	apiKey  string
	baseURL string
	http    *http.Client
}

func NewDeepSeek(apiKey, baseURL string) *DeepSeek {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &DeepSeek{
		apiKey:  apiKey,
		baseURL: baseURL,
		http:    http.DefaultClient,
	}
}

// CallMessages sends a non-streaming request and returns the response text.
func (c *DeepSeek) CallMessages(ctx context.Context, req *Request) (string, error) {
	req.Stream = false

	body, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", c.apiKey)
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("API error %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	if len(result.Content) > 0 {
		return result.Content[0].Text, nil
	}
	return "", nil
}

func (c *DeepSeek) StreamMessages(ctx context.Context, req *Request) (Streamer, error) {
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

	return &deepseekStream{
		reader: bufio.NewReader(resp.Body),
		body:   resp.Body,
	}, nil
}
