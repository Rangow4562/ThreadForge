package handoff

const (
	HarnessClaude   = "claude_code"
	HarnessCodex    = "codex_cli"
	HarnessOpenCode = "opencode"
	HarnessCline    = "cline"
	HarnessPi       = "pi"
	HarnessPrime    = "prime"
	HarnessFactory  = "factory"
	HarnessCopilot  = "copilot_cli"
	HarnessHermes   = "hermes"
	HarnessDSH      = "deepseek_harness"
	HarnessFx       = "fx"
	HarnessGrok     = "grok"
	HarnessOpenClaw = "openclaw_gateway"
	HarnessCursor   = "cursor"
)

// StoreDirs overrides native session-store roots by canonical harness name.
// The values are passed only to read-only source constructors.
type StoreDirs map[string]string

// DefaultSources returns one read-only native-store reader for each supported runtime.
func DefaultSources(dirs StoreDirs) []Source {
	return []Source{
		&claudeSource{dir: dirs[HarnessClaude]},
		&codexSource{dir: dirs[HarnessCodex]},
		&openCodeSource{dir: dirs[HarnessOpenCode]},
		&clineSource{dir: dirs[HarnessCline]},
		&piSource{dir: dirs[HarnessPi]},
		&primeSource{dir: dirs[HarnessPrime]},
		&factorySource{dir: dirs[HarnessFactory]},
		&copilotSource{dir: dirs[HarnessCopilot]},
		&hermesSource{path: dirs[HarnessHermes]},
		&dshSource{dir: dirs[HarnessDSH]},
		&fxSource{dir: dirs[HarnessFx]},
		&grokSource{dir: dirs[HarnessGrok]},
		&openClawSource{dir: dirs[HarnessOpenClaw]},
		&cursorSource{dir: dirs[HarnessCursor]},
	}
}
