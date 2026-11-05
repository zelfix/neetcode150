package tutor

// Provider selects the local coding agent that powers the tutor panel.
type Provider string

const (
	ProviderClaude Provider = "claude"
	ProviderCodex  Provider = "codex"
)

// Valid reports whether p names a supported provider.
func (p Provider) Valid() bool { return p == ProviderClaude || p == ProviderCodex }
