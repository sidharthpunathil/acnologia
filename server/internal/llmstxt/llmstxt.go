// Package llmstxt embeds the llms.txt served at /llms.txt.
// The Makefile copies docs/llms.txt here before building.
package llmstxt

import _ "embed"

//go:embed llms.txt
var Content []byte
