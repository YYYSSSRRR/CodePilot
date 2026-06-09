package permissions

// PermissionMode is the default behaviour when no rule matches.
type PermissionMode string

const (
	ModeDefault PermissionMode = "default" // read allow, write ask
	ModePlan    PermissionMode = "plan"    // read allow, write deny
	ModeBypass  PermissionMode = "bypass"  // everything allow
)

// RuleBehavior is the action to take when a rule matches.
type RuleBehavior string

const (
	BehaviorAllow RuleBehavior = "allow"
	BehaviorAsk   RuleBehavior = "ask"
	BehaviorDeny  RuleBehavior = "deny"
)

// RuleSource identifies which config layer the rule came from.
type RuleSource string

const (
	SourceBuiltIn RuleSource = "built-in"
	SourceGlobal  RuleSource = "global"
	SourceProject RuleSource = "project"
)

// RuleValue defines a single permission rule.
type RuleValue struct {
	ToolName    string `json:"toolName"`
	RuleContent string `json:"ruleContent"` // glob pattern (* matches everything)
}

// PermissionRule is one entry in the configuration.
type PermissionRule struct {
	Source      RuleSource  `json:"source"`
	RuleBehavior RuleBehavior `json:"ruleBehavior"`
	RuleValue   RuleValue   `json:"ruleValue"`
}

// Settings is the top-level configuration.
type Settings struct {
	PermissionMode  PermissionMode  `json:"permissionMode"`
	PermissionRules []PermissionRule `json:"permissionRules"`
}

// Decision is the result of the permission check pipeline.
type Decision int

const (
	DecisionAllow Decision = iota
	DecisionAsk
	DecisionDeny
)

func (d Decision) String() string {
	switch d {
	case DecisionAllow:
		return "allow"
	case DecisionAsk:
		return "ask"
	case DecisionDeny:
		return "deny"
	default:
		return "unknown"
	}
}

// Result carries the pipeline decision and any message.
type Result struct {
	Decision Decision
	Message  string
}