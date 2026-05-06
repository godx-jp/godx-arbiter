package main

import "encoding/json"

// jsonMarshalIndent is a convenience wrapper used by --json subcommands.
// Returns the formatted string + a hard error if marshal fails (which
// shouldn't happen for our straightforward map shapes).
func jsonMarshalIndent(v any) (string, error) {
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	return string(out), nil
}
