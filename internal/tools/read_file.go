package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// ReadFile lets the agent inspect a file inside the project for
// context. Refuses to escape the project root, refuses binary files,
// caps output at MaxBytes (default 8 KiB).
type ReadFile struct{}

// NewReadFile constructs the tool.
func NewReadFile() *ReadFile { return &ReadFile{} }

// Name implements Tool.
func (ReadFile) Name() string { return "read_file" }

// Description implements Tool.
func (ReadFile) Description() string {
	return "Read a file inside the project for context. UTF-8 only; binaries are refused. Capped output (default 8 KiB)."
}

// InputSchema implements Tool.
func (ReadFile) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"project_root": map[string]any{"type": "string"},
			"path":         map[string]any{"type": "string", "description": "Path relative to project_root (or absolute, but must remain under project_root)"},
			"max_bytes":    map[string]any{"type": "integer", "default": 8192},
		},
		"required": []string{"project_root", "path"},
	}
}

type readFileInput struct {
	ProjectRoot string `json:"project_root"`
	Path        string `json:"path"`
	MaxBytes    int    `json:"max_bytes"`
}

type readFileOutput struct {
	Path      string `json:"path"`
	Bytes     int    `json:"bytes"`
	Truncated bool   `json:"truncated,omitempty"`
	Content   string `json:"content"`
}

// Execute implements Tool.
func (r *ReadFile) Execute(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var in readFileInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	if in.ProjectRoot == "" || in.Path == "" {
		return nil, errors.New("project_root and path are required")
	}
	max := in.MaxBytes
	if max <= 0 {
		max = 8192
	}

	root, err := filepath.Abs(in.ProjectRoot)
	if err != nil {
		return nil, err
	}

	abs := in.Path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(root, in.Path)
	}
	abs = filepath.Clean(abs)
	if !strings.HasPrefix(abs, root+string(filepath.Separator)) && abs != root {
		return nil, fmt.Errorf("read_file: path escapes project root")
	}

	f, err := os.Open(abs)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	buf := make([]byte, max+1)
	n, _ := f.Read(buf)
	truncated := n > max
	if truncated {
		n = max
	}
	body := buf[:n]
	if !utf8.Valid(body) {
		return nil, fmt.Errorf("read_file: refusing binary content")
	}
	out := readFileOutput{
		Path:      abs,
		Bytes:     n,
		Truncated: truncated,
		Content:   string(body),
	}
	return json.Marshal(out)
}
