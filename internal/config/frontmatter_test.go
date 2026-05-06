package config

import "testing"

func TestSplitFrontMatter_Standard(t *testing.T) {
	in := []byte("---\nfoo: bar\nbaz: 1\n---\n# Hello\n\nbody text")
	front, body := splitFrontMatter(in)
	if string(front) != "foo: bar\nbaz: 1" {
		t.Errorf("front = %q", front)
	}
	if string(body) != "# Hello\n\nbody text" {
		t.Errorf("body = %q", body)
	}
}

func TestSplitFrontMatter_NoFrontMatter(t *testing.T) {
	in := []byte("# Just markdown\n\nno yaml here")
	front, body := splitFrontMatter(in)
	if front != nil {
		t.Errorf("expected nil front, got %q", front)
	}
	if string(body) != string(in) {
		t.Errorf("body = %q, want full content", body)
	}
}

func TestSplitFrontMatter_OnlyFrontMatter(t *testing.T) {
	in := []byte("---\nfoo: bar\n---")
	front, body := splitFrontMatter(in)
	if string(front) != "foo: bar" {
		t.Errorf("front = %q", front)
	}
	if len(body) != 0 {
		t.Errorf("body should be empty, got %q", body)
	}
}

func TestSplitFrontMatter_Malformed(t *testing.T) {
	in := []byte("---\nfoo: bar\n# No closing fence\nbody")
	front, body := splitFrontMatter(in)
	if front != nil {
		t.Errorf("expected nil front (malformed), got %q", front)
	}
	if string(body) != string(in) {
		t.Errorf("expected body = full content, got %q", body)
	}
}

func TestSplitFrontMatter_EmptyFrontMatter(t *testing.T) {
	in := []byte("---\n\n---\nbody")
	front, body := splitFrontMatter(in)
	if string(front) != "" {
		t.Errorf("front = %q, want empty", front)
	}
	if string(body) != "body" {
		t.Errorf("body = %q", body)
	}
}
