package tool

import (
	"github.com/YYYSSSRRR/codepilot/pkg/types"
)

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

// Register adds a tool to the registry. If a tool with the same name already
// exists (e.g., a built-in tool), the new tool is silently skipped — existing
// tools take priority.
func (r *Registry) Register(t Tool) {
	if _, exists := r.index[t.Name()]; exists {
		return
	}
	r.tools = append(r.tools, t)
	r.index[t.Name()] = t
}

func (r *Registry) GetAll() []Tool {
	return r.tools
}

// Definitions returns the API-compatible tool definitions for the model.
func (r *Registry) Definitions() []types.ToolParam {
	out := make([]types.ToolParam, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, types.ToolParam{
			Name:        t.Name(),
			Description: t.Description(),
			InputSchema: t.InputSchema(),
		})
	}
	return out
}
