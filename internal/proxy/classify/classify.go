// Package classify tags a model invocation with a task class so the
// router (internal/proxy/route) can apply rules.md routing rules.
//
// Today the classifier is heuristic-only. The roadmap leaves room for a
// Haiku-backed fuzzy classifier; the heuristic implements all the obvious
// signals (prompt keywords, tool patterns, file size) and is the right
// default to ship.
package classify

import (
	"regexp"
	"strings"
)

// Tag is an opaque, lower-case task tag returned by the classifier.
type Tag string

const (
	TagReadOnlySummarization Tag = "read-only-summarization"
	TagSimpleEdit            Tag = "simple-edit"
	TagHardReasoning         Tag = "hard-reasoning"
	TagCodeGenerationLarge   Tag = "code-generation-large"
	TagOther                 Tag = "other"
)

// Input is what the classifier sees: the user message, recent tool
// calls (if any), and a few hints from the request.
type Input struct {
	UserMessage      string
	ToolNames        []string
	FileSizeLOC      int
	NewFileLOC       int
	FilesAffected    int
	HasNewDependency bool
}

// Classify returns the best-match tag and a confidence in [0, 1]. A
// confidence below 0.6 means "fall back to LLM classifier" — which the
// caller may or may not implement.
func Classify(in Input) (Tag, float64) {
	prompt := strings.ToLower(in.UserMessage)

	// Tool-only hints (no prompt context): assume read-only summarization
	// when every tool is read-only and there is no user message.
	if len(in.ToolNames) > 0 && in.UserMessage == "" {
		if allReadOnly(in.ToolNames) {
			return TagReadOnlySummarization, 0.85
		}
	}

	switch {
	case rxSummarize.MatchString(prompt):
		return TagReadOnlySummarization, 0.85
	case rxArchitecture.MatchString(prompt):
		return TagHardReasoning, 0.85
	case in.FilesAffected > 5 || in.NewFileLOC > 200:
		return TagCodeGenerationLarge, 0.8
	case in.FileSizeLOC > 0 && in.FileSizeLOC < 500 && hasTool(in.ToolNames, "Edit"):
		return TagSimpleEdit, 0.7
	case in.HasNewDependency:
		return TagHardReasoning, 0.65
	case rxQuestion.MatchString(prompt):
		return TagReadOnlySummarization, 0.6
	}
	return TagOther, 0.5
}

var (
	rxSummarize    = regexp.MustCompile(`\b(summari[sz]e|tldr|tl;dr|explain briefly|what does this do)\b`)
	rxArchitecture = regexp.MustCompile(`\b(architect|design|tradeoff|tradeoff[s]?|rewrite|refactor)\b`)
	rxQuestion     = regexp.MustCompile(`\b(what|how|why|when|where|who)\b.*\?`)
)

func allReadOnly(tools []string) bool {
	for _, t := range tools {
		switch t {
		case "Read", "Glob", "Grep":
			continue
		default:
			return false
		}
	}
	return true
}

func hasTool(tools []string, name string) bool {
	for _, t := range tools {
		if t == name {
			return true
		}
	}
	return false
}
