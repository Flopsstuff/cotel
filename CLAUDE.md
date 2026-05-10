# CLAUDE.md

## Scratch files

Any throwaway artifact you create during a session — screenshots, one-off debug scripts, curl payload dumps, temporary shell scripts, ad-hoc Go programs for inspecting DB state, etc. — goes in `.playwright-mcp/` (gitignored). Do not leave such files in the repo root or other source directories. If the artifact is worth keeping, promote it to a proper location (e.g. `scripts/`, `cmd/<real-tool>/`) with a real name and intentional structure.
