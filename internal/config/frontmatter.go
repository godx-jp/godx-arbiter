package config

import "bytes"

// splitFrontMatter separates a YAML front matter block from the body of
// a Markdown file.
//
// Format:
//
//	---\n
//	<yaml content>\n
//	---\n
//	<body>
//
// The leading "---\n" must be the very first 4 bytes; otherwise content
// is treated as having no front matter and (nil, content) is returned.
//
// Returns (frontMatterBytes, bodyBytes). Either may be nil/empty.
func splitFrontMatter(content []byte) (front, body []byte) {
	const sep = "---\n"
	if !bytes.HasPrefix(content, []byte(sep)) {
		return nil, content
	}
	rest := content[len(sep):]
	// Look for closing "---" on its own line.
	if idx := bytes.Index(rest, []byte("\n---\n")); idx >= 0 {
		return rest[:idx], rest[idx+len("\n---\n"):]
	}
	// File ends right after the closing "---"
	if bytes.HasSuffix(rest, []byte("\n---")) {
		return rest[:len(rest)-len("\n---")], nil
	}
	// Malformed — no closing fence. Treat as no front matter.
	return nil, content
}
