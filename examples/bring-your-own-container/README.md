# Bring Your Own Container

Run a sandbox with a **custom** image (OpenShell-style BYOC). cautem does not require
its first-party agent layers — any standard Linux image works if it meets the
contract in the [image reference](https://cautem.github.io/sandbox.dev/reference/images/).

## Quick start

```bash
docker build -t cautem-byoc:latest cautem-cli/examples/bring-your-own-container

cautem sandbox create --name byoc \
  --image cautem-byoc:latest \
  --workspace "$PWD" \
  --policy cautem-cli/policies/default.yaml \
  --no-proxy \
  -- python /sandbox/app.py

cautem sandbox exec byoc -- curl -sf http://127.0.0.1:8080/hello
cautem sandbox rm byoc
```

Or register an alias:

```yaml
# ~/.config/cautem/config.yaml
images:
  byoc: cautem-byoc:latest
```

```bash
cautem sandbox create --name byoc --from byoc --workspace . --policy cautem-cli/policies/default.yaml --no-proxy -- python /sandbox/app.py
```

## Requirements

| Rule | Why |
|------|-----|
| Non-root `USER` (or policy `run_as_*`) | Matches Docker/Podman sandbox identity |
| Writable `/sandbox` or `/workspace` | Agent/workdir |
| `iproute2` recommended | Netns / routing |
| No distroless / `FROM scratch` | Need a real userland |
| Pass command after `--` | Image CMD is replaced by `cautem-init` |

First-party images (`--from cursor`, …): build locally or `task images:pull` / GHCR — [image reference](https://cautem.github.io/sandbox.dev/reference/images/).
