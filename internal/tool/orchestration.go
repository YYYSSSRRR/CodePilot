package tool

// Orchestrator manages concurrent tool execution and result collection.
// Currently a placeholder for future multi-tool orchestration.
type Orchestrator struct {
	registry *Registry
}

func NewOrchestrator(registry *Registry) *Orchestrator {
	return &Orchestrator{registry: registry}
}
