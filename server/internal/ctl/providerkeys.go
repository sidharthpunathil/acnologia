package ctl

import (
	"regexp"
	"strings"
)

// Provider key knowledge shared by acn run and acn scan. Provider specs do
// not name an environment variable, so the CLI keeps the usual names of the
// built in providers here: the first name is the one their SDKs read.

// providerEnvNames maps a provider slug to the environment variables its
// official SDKs and common tools read the key from.
var providerEnvNames = map[string][]string{
	"openai":          {"OPENAI_API_KEY"},
	"anthropic":       {"ANTHROPIC_API_KEY", "CLAUDE_API_KEY"},
	"gemini":          {"GEMINI_API_KEY", "GOOGLE_API_KEY", "GOOGLE_GENERATIVE_AI_API_KEY"},
	"groq":            {"GROQ_API_KEY"},
	"xai":             {"XAI_API_KEY", "GROK_API_KEY"},
	"mistral":         {"MISTRAL_API_KEY"},
	"cohere":          {"COHERE_API_KEY", "CO_API_KEY"},
	"together":        {"TOGETHER_API_KEY", "TOGETHERAI_API_KEY"},
	"fireworks":       {"FIREWORKS_API_KEY"},
	"deepseek":        {"DEEPSEEK_API_KEY"},
	"openrouter":      {"OPENROUTER_API_KEY"},
	"perplexity":      {"PERPLEXITY_API_KEY", "PPLX_API_KEY"},
	"cerebras":        {"CEREBRAS_API_KEY"},
	"tavily":          {"TAVILY_API_KEY"},
	"exa":             {"EXA_API_KEY"},
	"firecrawl":       {"FIRECRAWL_API_KEY"},
	"jina":            {"JINA_API_KEY"},
	"serpapi":         {"SERPAPI_API_KEY", "SERPAPI_KEY", "SERP_API_KEY"},
	"serper":          {"SERPER_API_KEY"},
	"brave-search":    {"BRAVE_API_KEY", "BRAVE_SEARCH_API_KEY"},
	"searchapi":       {"SEARCHAPI_API_KEY"},
	"linkup":          {"LINKUP_API_KEY"},
	"youcom":          {"YDC_API_KEY", "YOU_API_KEY"},
	"elevenlabs":      {"ELEVENLABS_API_KEY", "XI_API_KEY"},
	"tinyfish-fetch":  {"TINYFISH_API_KEY"},
	"tinyfish-search": {"TINYFISH_API_KEY"},
}

// keyFormat is a provider's key shape, specific enough that a match names
// the provider whatever variable holds it.
type keyFormat struct {
	provider string
	re       *regexp.Regexp
}

// keyFormats are tried in order; more specific prefixes come first (sk-ant-
// and sk-or- before the plain sk- of OpenAI).
var keyFormats = []keyFormat{
	{"anthropic", regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{20,}`)},
	{"openrouter", regexp.MustCompile(`\bsk-or-[A-Za-z0-9_-]{20,}`)},
	{"openai", regexp.MustCompile(`\bsk-(?:proj|svcacct|admin)-[A-Za-z0-9_-]{20,}`)},
	{"openai", regexp.MustCompile(`\bsk-[A-Za-z0-9]{20}T3BlbkFJ[A-Za-z0-9]{20}\b`)},
	{"groq", regexp.MustCompile(`\bgsk_[A-Za-z0-9]{40,}`)},
	{"xai", regexp.MustCompile(`\bxai-[A-Za-z0-9]{40,}`)},
	{"gemini", regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}\b`)},
	{"perplexity", regexp.MustCompile(`\bpplx-[A-Za-z0-9]{40,}`)},
	{"tavily", regexp.MustCompile(`\btvly-[A-Za-z0-9-]{20,}`)},
	{"firecrawl", regexp.MustCompile(`\bfc-[a-f0-9]{32}\b`)},
	{"jina", regexp.MustCompile(`\bjina_[A-Za-z0-9_-]{30,}`)},
	{"fireworks", regexp.MustCompile(`\bfw_[A-Za-z0-9]{20,}`)},
	{"cerebras", regexp.MustCompile(`\bcsk-[a-z0-9]{40,}`)},
	{"brave-search", regexp.MustCompile(`\bBSA[A-Za-z0-9_-]{20,}`)},
	{"elevenlabs", regexp.MustCompile(`\bsk_[a-f0-9]{48}\b`)},
}

// providerByFormat names the provider whose key format value has, or "".
func providerByFormat(value string) string {
	for _, f := range keyFormats {
		if m := f.re.FindString(value); m == value {
			return f.provider
		}
	}
	return ""
}

// providerByEnvName names the provider an environment variable belongs to:
// a known name, or <SLUG>_API_KEY, <SLUG>_KEY or <SLUG>_TOKEN for a slug in
// known (the server's providers, when the CLI is signed in).
func providerByEnvName(name string, known map[string]bool) string {
	up := strings.ToUpper(name)
	for slug, names := range providerEnvNames {
		for _, n := range names {
			if n == up {
				// tinyfish has two services on one key; name the first.
				if strings.HasPrefix(slug, "tinyfish") {
					return "tinyfish-fetch"
				}
				return slug
			}
		}
	}
	for _, suf := range []string{"_API_KEY", "_APIKEY", "_KEY", "_TOKEN"} {
		if base, ok := strings.CutSuffix(up, suf); ok && base != "" {
			slug := strings.ReplaceAll(strings.ToLower(base), "_", "-")
			if known[slug] {
				return slug
			}
		}
	}
	return ""
}

// placeholderKey is the value acn run gives a provider's key variable. The
// forward proxy replaces the auth header, so the value never matters; it
// only lets SDKs that refuse to start without a key start.
const placeholderKey = "acn-placeholder"
