# Starter policies

These YAML files are **hand-authored starters** for the `cautem` CLI, not generated dumps.

| File | Role |
|------|------|
| [`default.yaml`](./default.yaml) | Generic sandbox: default-deny network, Anthropic/OpenAI inference presets, no display |
| [`cursor.yaml`](./cursor.yaml) | Cursor Agent (C1): allowlist for `*.cursor.sh` / `*.cursor.com`, apt/debian, credential keys |
| [`github-push-cautem.yaml`](./github-push-cautem.yaml) | GitHub write widen for org `cautem` (create repo + push) |
| [`cursor-github-push-cautem.yaml`](./cursor-github-push-cautem.yaml) | Cursor allowlist + GitHub write for org `cautem` |

## Compared with OpenShell

OpenShell ships a **default policy baked into the community base image** (`dev-sandbox-policy.yaml`) tuned mainly for Claude Code. Other agents need a custom `--policy`. OpenShell does **not** keep a large policy library in the CLI tree; agent-specific network access increasingly comes from **provider profiles** plus `policy set`.

cautem follows the same idea with a thin CLI tree:

- `default.yaml` — create-without-thinking baseline (like OpenShell default, but inference-oriented)
- `cursor.yaml` — one first-class agent recipe (also copied by `cautem init --agent cursor`)
- Further agents: [`../examples/`](../examples/) recipes and [provider profiles](https://cautem.github.io/sandbox.dev/guides/provider-profiles/) (`github`, `nvidia`, …)

You do not need more files under `policies/` unless you want another first-class `cautem init --agent …` target.
