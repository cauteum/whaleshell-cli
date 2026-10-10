# Bring Your Own Container

Run a sandbox with a **custom** image (OpenShell-style BYOC). cauteum does not require
its first-party agent layers — any standard Linux image works if it meets the
contract in the [image reference](https://cautem.github.io/cauteum-haven.github.io/reference/images/).

## Quick start

```bash
docker build -t cauteum-byoc:latest cauteum-cli/examples/bring-your-own-container

cauteum sandbox create --name byoc \
  --image cauteum-byoc:latest \
  --workspace "$PWD" \
  --policy cauteum-cli/policies/default.yaml \
  --no-proxy \
  -- python /sandbox/app.py

cauteum sandbox exec byoc -- curl -sf http://127.0.0.1:8080/hello
cauteum sandbox rm byoc
```

Or register an alias:

```yaml
# ~/.config/cauteum/config.yaml
images:
  byoc: cauteum-byoc:latest
```

```bash
cauteum sandbox create --name byoc --from byoc --workspace . --policy cauteum-cli/policies/default.yaml --no-proxy -- python /sandbox/app.py
```

## Requirements

| Rule | Why |
|------|-----|
| Non-root `USER` (or policy `run_as_*`) | Matches Docker/Podman sandbox identity |
| Writable `/sandbox` or `/workspace` | Agent/workdir |
| `iproute2` recommended | Netns / routing |
| No distroless / `FROM scratch` | Need a real userland |
| Pass command after `--` | Image CMD is replaced by `cauteum-init` |

First-party images (`--from cursor`, …): build locally or `task images:pull` / GHCR — [image reference](https://cautem.github.io/cauteum-haven.github.io/reference/images/).
