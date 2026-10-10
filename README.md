<h1 align="center">cauteum-cli</h1>

<p align="center">
  <strong>Agent sandbox CLI</strong><br>
  Create, harden, and operate policy-bound sandboxes — Cursor, Claude, Codex, and BYOC.
</p>
<p align="center">
  <a href="https://github.com/cautem/cauteum-cli/actions/workflows/ci.yml"><img src="https://github.com/cautem/cauteum-cli/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://github.com/cautem/cauteum-cli/releases"><img src="https://img.shields.io/github/v/release/cautem/cauteum-cli?include_prereleases&sort=semver&label=release" alt="release"></a>
  <a href="https://img.shields.io/badge/status-v0.1.5-blue"><img src="https://img.shields.io/badge/status-v0.1.5-blue" alt="v0.1.5"></a>
  <a href="https://www.apache.org/licenses/LICENSE-2.0"><img src="https://img.shields.io/badge/License-Apache--2.0-blue.svg" alt="License"></a>
  <a href="https://github.com/cautem/cauteum-cli"><img src="https://img.shields.io/badge/Go-1.27+-00ADD8?logo=go" alt="Go Version"></a>
</p>
<p align="center">
  <sub>Part of the <a href="https://github.com/cautem">cauteum / cauteum</a> ecosystem</sub>
</p>

---

## Overview

The [quick start](https://cautem.github.io/cauteum-haven.github.io/get-started/) contains the supported installation and first-sandbox flow.

**cauteum-cli** is the user-facing `cauteum` binary for the cauteum ecosystem: sandbox lifecycle, policy checks, provider attach, gateway selection, live logs, and agent images.

### Key Features

| Category | Capabilities |
|----------|--------------|
| **Sandboxes** | create / exec / connect / rm — Docker-first via `cauteum-driver` |
| **Policy** | YAML check/compose with `cauteum-core` + provider presets |
| **Agents** | `--from cursor\|claude\|codex\|base\|gui\|gpu` (GHCR or local) |
| **Providers** | Cursor, GitHub, NVIDIA, local inference (`host.cauteum.internal`) |
| **Observability** | `cauteum logs --tail`, TUI (`cauteum term`), OCSF audit lines |
| **Gateway** | register with `cauteum-gateway`, relay exec, policy proposals |

---

## Installation

Install the current release (`v0.1.5`) into `~/.local/bin`:

```bash
curl -LsSf https://raw.githubusercontent.com/cautem/cauteum-cli/main/install.sh \
  | CAUTEUM_VERSION=v0.1.5 sh
```

From source in the [multi-repo workspace](https://github.com/cautem):

```bash
go build -C cauteum-cli -o ../cauteum ./cmd/cauteum
go build -C cauteum-cli -o ../cauteum-console ./cmd/cauteum-console
./cauteum install
```

**Requirements:** Docker or Podman. Go 1.27+ only if building from source.

`v0.1.5` is the current stable numbered release. It builds from published module dependencies; OpenShell behavioral compatibility remains partial. See [development and releases](https://cautem.github.io/cauteum-haven.github.io/reference/development/) and the [compatibility status](https://cautem.github.io/cauteum-haven.github.io/reference/openshell-compatibility/).

---

## Quick Start

```bash
./cauteum status
./cauteum policy check ./policies/default.yaml

./cauteum sandbox create --name demo --workspace . --policy ./policies/default.yaml
./cauteum sandbox exec demo -- uname -a
./cauteum sandbox rm demo
```

Testcontainers cleanup is exposed through the CLI rather than a broad Docker
prune:

```bash
./cauteum doctor cleanup --dry-run
./cauteum -o json doctor cleanup --yes
```

Only dangling anonymous test volumes and stopped containers labeled
`cauteum=1` are in scope.

### Browser console

The Go console ships as `cauteum-console` in Linux and macOS release archives
and is installed by `install.sh`. It uses the public Go SDK and keeps OIDC
tokens in server-side sessions. Local development can use the loopback auth
bootstrap:

```bash
cauteum-console \
  -listen 127.0.0.1:8080 \
  -public-url http://127.0.0.1:8080 \
  -gateway http://127.0.0.1:7443
```

For a network deployment, set an HTTPS `-public-url`, configure gateway OIDC,
and publish the console and gateway through the same trusted ingress. The
console supports workspace inventory, overview, sandbox detail/logs, and
create/start/stop/delete actions. The gateway remains the authorization and
policy boundary.

### Cursor agent

```bash
./cauteum sandbox create --name cursor --from cursor --workspace "$PWD" \
  --provider cursor --provider gh --policy ./policies/cursor.yaml
./cauteum sandbox connect cursor -- agent
```

Agent Dockerfiles live in [`docker/agents/`](./docker/agents/). Policies: [`policies/`](./policies/).
Runnable recipes live in [`examples/`](./examples/).

---

## Package Structure

| Path | Purpose |
|------|---------|
| `cmd/cauteum` | CLI entrypoint |
| `cmd/cauteum-console` | Go browser console entrypoint |
| `internal/app` | Commands (sandbox, proxy, gateway, rules, …) |
| `internal/console` | Server-side sessions, OIDC PKCE, handlers, templates, and assets |
| `policies/` | Builtin policy YAML |
| `docker/agents/` | cursor / claude / codex images |


---

## Related

| Resource | Link |
|----------|------|
| Roadmap | [ROADMAP.md](./ROADMAP.md) |
| Organization | [https://github.com/cautem](https://github.com/cautem) |
| Organization overview | [github.com/cautem](https://github.com/cautem) |
| pkg.go.dev | [`github.com/cautem/cauteum-cli`](https://pkg.go.dev/github.com/cautem/cauteum-cli) |

## License

[Apache-2.0](./LICENSE) © cauteum
