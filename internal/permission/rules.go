package permission

// Rules manages permission rule evaluation and persistence.
type Rules struct {
	rules []PermissionRule
}

// NewRules creates a Rules instance from a settings slice.
func NewRules(rules []PermissionRule) *Rules {
	return &Rules{rules: rules}
}

// Match checks if any rule matches the given tool name + content + behavior.
func (r *Rules) Match(toolName, content string, behavior RuleBehavior) bool {
	for _, rule := range r.rules {
		if rule.RuleBehavior == behavior && rule.RuleValue.ToolName == toolName {
			if rule.RuleValue.RuleContent == "*" || rule.RuleValue.RuleContent == content {
				return true
			}
		}
	}
	return false
}

// Add appends a new rule.
func (r *Rules) Add(rule PermissionRule) {
	r.rules = append(r.rules, rule)
}

// All returns all rules.
func (r *Rules) All() []PermissionRule {
	return r.rules
}
