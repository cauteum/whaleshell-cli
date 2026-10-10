# cautem-cli/docker/

Agent sandbox images for the `cautem` CLI (`--from cursor|claude|codex`).
Runtime base (`cautem-sandbox:local` / GHCR `:cli`) is built from the [runtime image sources](https://github.com/cautem/cautem-runtime/tree/main/images/sandbox) via `task runtime:image:cli` in the multi-repo workspace.

| Path | Tag | Contents |
|------|-----|----------|
| [`agents/cursor`](./agents/cursor/) | `cautem-sandbox:cursor` | + Cursor Agent CLI under `/opt/cursor-agent` |
| [`agents/claude`](./agents/claude/) | `cautem-sandbox:claude` | + Claude Code CLI |
| [`agents/codex`](./agents/codex/) | `cautem-sandbox:codex` | + OpenAI Codex CLI |

Published images and BYOC: [image reference](https://cautem.github.io/sandbox.dev/reference/images/).

## Build

```bash
export GOWORK=$PWD/go.work
task runtime:image:cli          # base → cautem-sandbox:local
task docker:agent:cursor        # → cautem-sandbox:cursor
task docker:agent:claude
task docker:agent:codex
task docker:agent:all
```

## Use

```bash
cautem sandbox create --name cursor \
  --from cursor \
  --workspace . \
  --policy cautem-cli/policies/cursor.yaml \
  -- agent
```
