// Package llmstxt embeds the llms.txt served at /llms.txt and the full
// reference served at /llms-full.txt. The Makefile and Dockerfile copy both
// from docs/ before building.
package llmstxt

import _ "embed"

//go:embed llms.txt
var Content []byte

//go:embed llms-full.txt
var Full []byte
