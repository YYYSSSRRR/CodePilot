package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
)

// ToolDef is the tool definition returned by an MCP server.
type ToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// Client manages a JSON-RPC 2.0 connection to an MCP server over stdio.
type Client struct {
	cfg    ServerConfig
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	buf    *bufio.Reader
	mu     sync.Mutex
	nextID int
	done   chan struct{}
	once   sync.Once

	tools []ToolDef
}

// Connect starts the MCP server process and performs the handshake.
func Connect(ctx context.Context, cfg ServerConfig) (*Client, error) {
	cmd := exec.CommandContext(ctx, cfg.Command, cfg.Args...)
	if cfg.Env != nil {
		cmd.Env = os.Environ()
		for k, v := range cfg.Env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	// Redirect stderr to our stderr so server logs are visible
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", cfg.Command, err)
	}

	c := &Client{
		cfg:   cfg,
		cmd:   cmd,
		stdin: stdin,
		buf:   bufio.NewReader(stdout),
		done:  make(chan struct{}),
	}

	// Wait for process exit in background to signal close
	go func() {
		cmd.Wait()
		close(c.done)
	}()

	// Handshake: initialize
	if err := c.initialize(ctx); err != nil {
		c.Close()
		return nil, fmt.Errorf("initialize %s: %w", cfg.Name, err)
	}

	// Send initialized notification (fire-and-forget)
	c.notify(ctx, "notifications/initialized", nil)

	// List available tools
	tools, err := c.listTools(ctx)
	if err != nil {
		c.Close()
		return nil, fmt.Errorf("list tools %s: %w", cfg.Name, err)
	}
	c.tools = tools

	return c, nil
}

// Tools returns the discovered tool definitions.
func (c *Client) Tools() []ToolDef {
	return c.tools
}

// CallTool invokes a tool on the MCP server.
func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) (string, error) {
	result, err := c.call(ctx, "tools/call", map[string]any{
		"name":      name,
		"arguments": args,
	})
	if err != nil {
		return "", err
	}

	// Parse the tool call result
	// MCP tool result can contain multiple content items
	var callResult struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text,omitempty"`
		} `json:"content"`
		IsError bool `json:"isError,omitempty"`
	}
	if err := json.Unmarshal(result, &callResult); err != nil {
		// If we can't parse, return the raw result
		return string(result), nil
	}

	if callResult.IsError {
		text := ""
		for _, c := range callResult.Content {
			text += c.Text
		}
		return text, fmt.Errorf("MCP tool error: %s", text)
	}

	var out string
	for _, c := range callResult.Content {
		switch c.Type {
		case "text":
			out += c.Text
		default:
			out += string(result)
		}
	}
	return out, nil
}

// Close terminates the MCP server process.
func (c *Client) Close() error {
	c.once.Do(func() {
		c.stdin.Close()
		c.cmd.Process.Kill()
	})
	return nil
}

// Name returns the server name.
func (c *Client) Name() string { return c.cfg.Name }

// ---------------------------------------------------------------------------
// JSON-RPC 2.0 helpers
// ---------------------------------------------------------------------------

type jsonrpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type jsonrpcResponse struct {
	ID     int              `json:"id"`
	Result json.RawMessage  `json:"result,omitempty"`
	Error  *jsonrpcError    `json:"error,omitempty"`
}

type jsonrpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (c *Client) initialize(ctx context.Context) error {
	params := map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "codepilot",
			"version": "1.0.0",
		},
	}
	_, err := c.call(ctx, "initialize", params)
	return err
}

func (c *Client) notify(ctx context.Context, method string, params any) {
	data, _ := json.Marshal(jsonrpcRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	})
	c.mu.Lock()
	defer c.mu.Unlock()
	fmt.Fprintln(c.stdin, string(data))
}

func (c *Client) listTools(ctx context.Context) ([]ToolDef, error) {
	result, err := c.call(ctx, "tools/list", nil)
	if err != nil {
		return nil, err
	}
	var list struct {
		Tools []ToolDef `json:"tools"`
	}
	if err := json.Unmarshal(result, &list); err != nil {
		return nil, fmt.Errorf("parse tools/list: %w", err)
	}
	return list.Tools, nil
}

func (c *Client) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	select {
	case <-c.done:
		return nil, fmt.Errorf("MCP client %q is closed", c.cfg.Name)
	default:
	}

	c.nextID++
	id := c.nextID

	req := jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	if _, err := fmt.Fprintln(c.stdin, string(data)); err != nil {
		return nil, fmt.Errorf("write request: %w", err)
	}

	// Read responses until we find our matching ID
	for {
		line, err := c.buf.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("read response: %w", err)
		}

		var resp jsonrpcResponse
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			continue
		}

		if resp.ID == id {
			if resp.Error != nil {
				return nil, fmt.Errorf("MCP error (%d): %s", resp.Error.Code, resp.Error.Message)
			}
			return resp.Result, nil
		}
		// Non-matching ID or notification — skip
	}
}
