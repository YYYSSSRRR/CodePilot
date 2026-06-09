package tool

import (
	"context"
	"fmt"

	"github.com/YYYSSSRRR/codepilot/internal/types"
)

// Tool is the interface every tool must implement.
type Tool interface {
	Name() string
	Description() string
	InputSchema() map[string]any
	Call(ctx context.Context, input map[string]any) (string, error)
}

// PermissionedTool is an optional interface for tools with custom permission logic.
type PermissionedTool interface {
	Tool
	// CheckPermissions returns true/false and a machine-readable behavior hint.
	// behavior may be "allow", "deny", or "ask".
	CheckPermissions(input map[string]any) (allowed bool, behavior string, message string, err error)
	// IsWriteOperation returns true if this tool invocation would modify state.
	IsWriteOperation(input map[string]any) bool
}

// Registry holds the set of registered tools.
type Registry struct {
	tools []Tool
	index map[string]Tool
}

func NewRegistry(tools ...Tool) *Registry {
	r := &Registry{
		tools: tools,
		index: make(map[string]Tool, len(tools)),
	}
	for _, t := range tools {
		r.index[t.Name()] = t
	}
	return r
}

func (r *Registry) FindByName(name string) Tool {
	return r.index[name]
}

func (r *Registry) GetAll() []Tool {
	return r.tools
}

func (r *Registry) Definitions() []types.APIToolParam {
	out := make([]types.APIToolParam, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, types.APIToolParam{
			Name:        t.Name(),
			Description: t.Description(),
			InputSchema: t.InputSchema(),
		})
	}
	return out
}

func (r *Registry) FindPermissioned(name string) (PermissionedTool, error) {
	t := r.index[name]
	if t == nil {
		return nil, fmt.Errorf("tool %q not found", name)
	}
	pt, ok := t.(PermissionedTool)
	if !ok {
		return nil, nil
	}
	return pt, nil
}