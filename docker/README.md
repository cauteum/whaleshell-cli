# cauteum-cli/docker/

Agent sandbox images for the `cauteum` CLI (`--from cursor|claude|codex`).  
Runtime base (`cauteum-sandbox:local` / GHCR `:cli`) is built from the [runtime image sources](https://github.com/cautem/cauteum-runtime/tree/main/images/sandbox) via `task runtime:image:cli` in the multi-repo workspace.

| Path | Tag | Contents |
|------|-----|----------|
| [`agents/cursor`](./agents/cursor/) | `cauteum-sandbox:cursor` | + Cursor Agent CLI under `/opt/cursor-agent` |
| [`agents/claude`](./agents/claude/) | `cauteum-sandbox:claude` | + Claude Code CLI |
| [`agents/codex`](./agents/codex/) | `cauteum-sandbox:codex` | + OpenAI Codex CLI |

Published images and BYOC: [image reference](https://cautem.github.io/cauteum-haven.github.io/reference/images/).

## Build

```bash
export GOWORK=$PWD/go.work
task runtime:image:cli          # base → cauteum-sandbox:local
task docker:agent:cursor        # → cauteum-sandbox:cursor
task docker:agent:claude
task docker:agent:codex
task docker:agent:all
```

## Use

```bash
cauteum sandbox create --name cursor \
  --from cursor \
  --workspace . \
  --policy cauteum-cli/policies/cursor.yaml \
  -- agent
```
