package tutor

// Provider selects the local coding agent that powers the tutor panel.
type Provider string

const (
	ProviderClaude Provider = "claude"
	ProviderCodex  Provider = "codex"
	// ProviderDeepSeek talks to an HTTP API instead of running an agent. It has
	// no tools and no access to the machine, which is what lets the panel be
	// offered to guests at all.
	ProviderDeepSeek Provider = "deepseek"
)

// Valid reports whether p names a supported provider.
func (p Provider) Valid() bool {
	return p == ProviderClaude || p == ProviderCodex || p == ProviderDeepSeek
}
