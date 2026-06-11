package types

// ToolParam is the API wire format for tool definitions sent to the model.
type ToolParam struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema any         `json:"input_schema"`
}