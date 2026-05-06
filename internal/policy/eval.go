package policy

import "github.com/godx-team/godx-arbiter/internal/config"

// Eval applies a Policy to an Action and returns a fast-path Decision.
//
// Evaluation order matches docs/POLICY_SPEC.md:
//
//  1. Deny rules (top-to-bottom; first match → OutcomeDeny)
//  2. Allow rules (top-to-bottom; first match → OutcomeAllow)
//  3. ToAgent rules (top-to-bottom; first match → OutcomeAgent,
//     overriding what would otherwise have been the default)
//  4. Default — Policy.Default decides: "agent" | "approve" | "deny"
//
// A nil Policy means "no fast-path defined": always returns
// OutcomeAgent so the slow-path agent decides.
func Eval(p *config.Policy, a *Action) *Decision {
	if p == nil {
		return &Decision{
			Outcome:   OutcomeAgent,
			RuleType:  "no-policy",
			RuleIndex: -1,
		}
	}

	if d := evalList("deny", p.Deny, OutcomeDeny, a); d != nil {
		return d
	}
	if d := evalList("allow", p.Allow, OutcomeAllow, a); d != nil {
		return d
	}
	if d := evalList("to_agent", p.ToAgent, OutcomeAgent, a); d != nil {
		return d
	}

	return defaultDecision(p)
}

func evalList(label string, rules []config.PolicyRule, outcome Outcome, a *Action) *Decision {
	for i, r := range rules {
		if matches(r, a) {
			return &Decision{
				Outcome:   outcome,
				RuleType:  label,
				RuleIndex: i,
				Reason:    r.Reason,
				Pattern:   r.Pattern,
			}
		}
	}
	return nil
}

func defaultDecision(p *config.Policy) *Decision {
	out := OutcomeAgent
	switch p.Default {
	case config.DefaultApprove:
		out = OutcomeAllow
	case config.DefaultDeny:
		out = OutcomeDeny
	}
	return &Decision{
		Outcome:   out,
		RuleType:  "default",
		RuleIndex: -1,
	}
}
