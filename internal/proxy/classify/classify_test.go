package classify

import "testing"

func TestClassify_Cases(t *testing.T) {
	cases := []struct {
		name string
		in   Input
		want Tag
	}{
		{"summarize prompt", Input{UserMessage: "summarize the diff please"}, TagReadOnlySummarization},
		{"architecture prompt", Input{UserMessage: "Discuss the architecture tradeoffs"}, TagHardReasoning},
		{"refactor prompt → hard", Input{UserMessage: "Please refactor the auth module"}, TagHardReasoning},
		{"large refactor", Input{FilesAffected: 20}, TagCodeGenerationLarge},
		{"simple edit", Input{FileSizeLOC: 200, ToolNames: []string{"Edit"}}, TagSimpleEdit},
		{"new dependency", Input{HasNewDependency: true}, TagHardReasoning},
		{"read-only tool only", Input{ToolNames: []string{"Read", "Glob"}}, TagReadOnlySummarization},
		{"fallback", Input{UserMessage: "hello"}, TagOther},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, _ := Classify(c.in)
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}
