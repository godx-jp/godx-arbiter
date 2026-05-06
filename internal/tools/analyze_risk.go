package tools

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
)

// AnalyzeRisk produces a quick risk profile for a proposed tool call.
//
// The current impl is heuristic-only — fast, deterministic, free.
// Future iterations may add a Haiku call (cached) for fuzzy
// classification per docs/MCP_TOOLS.md, but heuristics already cover
// the obvious cases the agent needs.
type AnalyzeRisk struct{}

// NewAnalyzeRisk constructs the tool.
func NewAnalyzeRisk() *AnalyzeRisk { return &AnalyzeRisk{} }

// Name implements Tool.
func (AnalyzeRisk) Name() string { return "analyze_risk" }

// Description implements Tool.
func (AnalyzeRisk) Description() string {
	return "Estimate risk of a proposed tool call. Returns score (0..1), category, concerns[], reversibility, blast_radius."
}

// InputSchema implements Tool.
func (AnalyzeRisk) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"tool":  map[string]any{"type": "string", "description": "Tool name (Bash, Edit, Write, ...)"},
			"input": map[string]any{"type": "object", "description": "Raw tool input as the calling CLI produced it"},
			"cwd":   map[string]any{"type": "string", "description": "Working directory of the calling session"},
		},
		"required": []string{"tool"},
	}
}

type analyzeRiskInput struct {
	Tool  string         `json:"tool"`
	Input map[string]any `json:"input"`
	Cwd   string         `json:"cwd"`
}

type analyzeRiskOutput struct {
	Score         float64  `json:"score"`
	Category      string   `json:"category"`
	Concerns      []string `json:"concerns"`
	Reversibility string   `json:"reversibility"`
	BlastRadius   string   `json:"blast_radius"`
}

// Execute implements Tool.
func (a *AnalyzeRisk) Execute(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var in analyzeRiskInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	out := classify(in)
	return json.Marshal(out)
}

var (
	rxRmRf       = regexp.MustCompile(`\brm\s+-rf\b`)
	rxRmRfSystem = regexp.MustCompile(`\brm\s+-rf\s+/(etc|usr|var|opt|bin|sbin|home|root|boot|sys|proc|lib|lib64|dev)\b`)
	rxForcePush  = regexp.MustCompile(`\bgit\s+push\b.*--force`)
	rxCurlPipe   = regexp.MustCompile(`curl[^|]*\|\s*(bash|sh|zsh)`)
	rxSudo       = regexp.MustCompile(`\bsudo\b`)
	rxSecretFile = regexp.MustCompile(`(?i)(\.env(\.|$)|\.pem$|\.key$|\.crt$|credentials|secret|\btoken\b)`)
	rxRegenerable = regexp.MustCompile(`(?i)(node_modules|dist|build|\.next|coverage|target|venv|__pycache__)`)
)

func classify(in analyzeRiskInput) analyzeRiskOutput {
	out := analyzeRiskOutput{
		Score:         0.1,
		Category:      "low-risk",
		Reversibility: "easy",
		BlastRadius:   "single-file",
	}
	concerns := []string{}

	switch in.Tool {
	case "Read", "Glob", "Grep":
		out.Score = 0.0
		out.Category = "read-only"
		out.Reversibility = "trivial"
		out.BlastRadius = "none"
		out.Concerns = concerns
		return out

	case "Bash":
		cmd := stringField(in.Input, "command")
		switch {
		case rxRmRfSystem.MatchString(cmd):
			out.Score = 0.95
			out.Category = "catastrophic"
			out.Reversibility = "impossible"
			out.BlastRadius = "system-wide"
			concerns = append(concerns, "rm -rf rooted at a system-critical path")
		case rxRmRf.MatchString(cmd) && rxRegenerable.MatchString(cmd):
			out.Score = 0.3
			out.Category = "destructive-reversible"
			out.Reversibility = "easy"
			out.BlastRadius = "single-directory"
			concerns = append(concerns, "destructive but path is regenerable")
		case rxRmRf.MatchString(cmd):
			out.Score = 0.7
			out.Category = "destructive"
			out.Reversibility = "hard"
			out.BlastRadius = "single-directory"
			concerns = append(concerns, "rm -rf of a non-regenerable path")
		case rxForcePush.MatchString(cmd):
			out.Score = 0.85
			out.Category = "history-rewriting"
			out.Reversibility = "very-hard"
			out.BlastRadius = "shared-branch"
			concerns = append(concerns, "force push rewrites history visible to others")
		case rxCurlPipe.MatchString(cmd):
			out.Score = 0.9
			out.Category = "supply-chain"
			out.Reversibility = "depends-on-payload"
			out.BlastRadius = "system-wide"
			concerns = append(concerns, "executing remote shell content unverified")
		case rxSudo.MatchString(cmd):
			out.Score = 0.6
			out.Category = "privileged"
			out.Reversibility = "depends"
			out.BlastRadius = "system-wide"
			concerns = append(concerns, "elevated privileges requested")
		default:
			out.Score = 0.25
			out.Category = "shell-other"
			out.Reversibility = "depends"
			out.BlastRadius = "single-directory"
		}

	case "Edit", "Write":
		path := stringField(in.Input, "file_path")
		if rxSecretFile.MatchString(path) {
			out.Score = 0.85
			out.Category = "secret-file"
			out.Reversibility = "hard"
			out.BlastRadius = "secret-leak"
			concerns = append(concerns, "edit targets a secret-bearing file")
		} else if strings.Contains(path, "/migrations/") || strings.Contains(path, "/migrate") {
			out.Score = 0.6
			out.Category = "migration"
			out.Reversibility = "hard"
			out.BlastRadius = "shared-database"
			concerns = append(concerns, "edit touches a migration — append-only by convention")
		} else {
			out.Score = 0.2
			out.Category = "file-edit"
			out.Reversibility = "easy"
		}

	case "Task":
		out.Score = 0.2
		out.Category = "subagent-spawn"
	}

	out.Concerns = concerns
	return out
}

func stringField(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}
