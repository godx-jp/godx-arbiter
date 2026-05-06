package policy

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/godx-team/godx-arbiter/internal/config"
)

// loadPolicy is a tiny helper that writes YAML to a temp file and
// loads it via config.LoadPolicy so we exercise the production parser.
func loadPolicy(t *testing.T, yaml string) *config.Policy {
	t.Helper()
	p := filepath.Join(t.TempDir(), "policy.yaml")
	if err := os.WriteFile(p, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}
	pol, err := config.LoadPolicy(p)
	if err != nil {
		t.Fatalf("LoadPolicy: %v", err)
	}
	return pol
}

func TestEval_NilPolicy_AlwaysAgent(t *testing.T) {
	d := Eval(nil, &Action{ToolName: "Bash", ToolInput: json.RawMessage(`{"command":"ls"}`)})
	if d.Outcome != OutcomeAgent {
		t.Errorf("Outcome = %v", d.Outcome)
	}
	if d.RuleType != "no-policy" {
		t.Errorf("RuleType = %q", d.RuleType)
	}
}

func TestEval_TableDriven(t *testing.T) {
	policyYAML := `
version: 1
default: agent

deny:
  - tool: Bash
    pattern: '\brm\s+-rf\s+/etc\b'
    reason: catastrophic
  - tool: Edit
    field: file_path
    pattern: '\.env(\.|$)'
    reason: secret file

allow:
  - tool: Read
  - tool: Bash
    pattern: '^(ls|cat|head)\b'
    reason: read-only

to_agent:
  - tool: Edit
    field: file_path
    pattern: 'backend/internal/config/'
    reason: deployment-critical
`
	p := loadPolicy(t, policyYAML)

	cases := []struct {
		name      string
		tool      string
		input     string
		wantOut   Outcome
		wantType  string
		wantIndex int
	}{
		{"deny rm -rf /etc", "Bash", `{"command":"rm -rf /etc/passwd"}`, OutcomeDeny, "deny", 0},
		{"deny .env edit", "Edit", `{"file_path":"backend/.env"}`, OutcomeDeny, "deny", 1},
		{"deny .env.production", "Edit", `{"file_path":".env.production"}`, OutcomeDeny, "deny", 1},
		{"allow Read tool only", "Read", `{"file_path":"/anything"}`, OutcomeAllow, "allow", 0},
		{"allow ls", "Bash", `{"command":"ls -la"}`, OutcomeAllow, "allow", 1},
		{"allow cat", "Bash", `{"command":"cat /etc/hostname"}`, OutcomeAllow, "allow", 1},
		{"to_agent for sensitive edit", "Edit", `{"file_path":"backend/internal/config/values.go"}`, OutcomeAgent, "to_agent", 0},
		{"unmatched bash → default agent", "Bash", `{"command":"go test ./..."}`, OutcomeAgent, "default", -1},
		{"unmatched tool → default agent", "Glob", `{"pattern":"**/*.go"}`, OutcomeAgent, "default", -1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := Eval(p, &Action{ToolName: c.tool, ToolInput: json.RawMessage(c.input)})
			if d.Outcome != c.wantOut {
				t.Errorf("Outcome = %v, want %v", d.Outcome, c.wantOut)
			}
			if d.RuleType != c.wantType {
				t.Errorf("RuleType = %q, want %q", d.RuleType, c.wantType)
			}
			if d.RuleIndex != c.wantIndex {
				t.Errorf("RuleIndex = %d, want %d", d.RuleIndex, c.wantIndex)
			}
		})
	}
}

func TestEval_DenyBeatsAllow(t *testing.T) {
	// rm -rf is denied even though "rm" is not in allow; if it were,
	// deny must still win because it runs first.
	p := loadPolicy(t, `
version: 1
deny:
  - tool: Bash
    pattern: '\brm\s+-rf\b'
    reason: too dangerous
allow:
  - tool: Bash
    pattern: 'rm'
    reason: rm allowed
`)
	d := Eval(p, &Action{ToolName: "Bash", ToolInput: json.RawMessage(`{"command":"rm -rf foo"}`)})
	if d.Outcome != OutcomeDeny {
		t.Errorf("Outcome = %v, want deny", d.Outcome)
	}
}

func TestEval_DefaultApprove(t *testing.T) {
	p := loadPolicy(t, `
version: 1
default: approve
`)
	d := Eval(p, &Action{ToolName: "Bash", ToolInput: json.RawMessage(`{"command":"anything"}`)})
	if d.Outcome != OutcomeAllow {
		t.Errorf("Outcome = %v, want allow", d.Outcome)
	}
	if d.RuleType != "default" {
		t.Errorf("RuleType = %q", d.RuleType)
	}
}

func TestEval_DefaultDeny(t *testing.T) {
	p := loadPolicy(t, `
version: 1
default: deny
`)
	d := Eval(p, &Action{ToolName: "Bash", ToolInput: json.RawMessage(`{"command":"anything"}`)})
	if d.Outcome != OutcomeDeny {
		t.Errorf("Outcome = %v", d.Outcome)
	}
}

func TestEval_ToolWildcard(t *testing.T) {
	p := loadPolicy(t, `
version: 1
deny:
  - tool: '*'
    pattern: 'panic'
    reason: any tool, any pattern containing 'panic'
`)
	cases := []string{"Bash", "Edit", "Read", "Whatever"}
	for _, tool := range cases {
		d := Eval(p, &Action{ToolName: tool, ToolInput: json.RawMessage(`{"command":"panic now"}`)})
		if d.Outcome != OutcomeDeny {
			t.Errorf("tool=%q Outcome = %v", tool, d.Outcome)
		}
	}
}

func TestEval_ExplicitFieldOverride(t *testing.T) {
	p := loadPolicy(t, `
version: 1
deny:
  - tool: Edit
    field: new_string
    pattern: 'eval\('
    reason: no eval()
`)
	d := Eval(p, &Action{
		ToolName:  "Edit",
		ToolInput: json.RawMessage(`{"file_path":"src/x.js","new_string":"return eval('hi')"}`),
	})
	if d.Outcome != OutcomeDeny {
		t.Errorf("Outcome = %v", d.Outcome)
	}
}

func TestEval_RawField(t *testing.T) {
	p := loadPolicy(t, `
version: 1
deny:
  - tool: Bash
    field: raw
    pattern: '"description"\s*:\s*"production"'
    reason: don't touch production
`)
	d := Eval(p, &Action{
		ToolName:  "Bash",
		ToolInput: json.RawMessage(`{"command":"ls","description":"production"}`),
	})
	if d.Outcome != OutcomeDeny {
		t.Errorf("Outcome = %v", d.Outcome)
	}
}

func TestEval_MalformedToolInput(t *testing.T) {
	p := loadPolicy(t, `
version: 1
deny:
  - tool: Bash
    pattern: 'x'
`)
	// Unparseable JSON falls back to matching against the raw bytes.
	d := Eval(p, &Action{ToolName: "Bash", ToolInput: json.RawMessage(`xxxxx`)})
	if d.Outcome != OutcomeDeny {
		t.Errorf("Outcome = %v", d.Outcome)
	}
}

func TestEval_OutcomeStringer(t *testing.T) {
	cases := []struct {
		o Outcome
		s string
	}{
		{OutcomeAllow, "allow"},
		{OutcomeDeny, "deny"},
		{OutcomeAgent, "agent"},
	}
	for _, c := range cases {
		if got := c.o.String(); got != c.s {
			t.Errorf("Outcome(%d).String() = %q, want %q", c.o, got, c.s)
		}
	}
}
