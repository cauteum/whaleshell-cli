# GitHub provider — agent push inside a sandbox

Goal: an agent **inside** the sandbox can `git push` / `gh` to your org.
The host only seeds the token once; the guest sees `cauteum:resolve:env:GITHUB_TOKEN`,
and the sidecar rewrites it on egress.

**Общий запуск + create:** [профили провайдеров](https://cautem.github.io/cauteum-haven.github.io/ru/guides/provider-profiles/)\
**Cursor+Git:** [Docker](https://cautem.github.io/cauteum-haven.github.io/ru/providers/docker/).

## Prerequisites

- Docker (or Podman) running on the host
- Go toolchain + this workspace (`go.work`)
- GitHub PAT with **`repo`** (classic) or fine-grained **Contents: Read and write**
  on the target repos
- Empty (or existing) repos under the org, e.g. `cautem/cauteum-cli`

## 1. Build CLI + gateway

```bash
cd /path/to/workspace
export GOWORK=$PWD/go.work
go build -C cauteum-cli -o ../cauteum ./cmd/cauteum
go build -C cauteum-gateway -o ../cauteum-gateway ./cmd/cauteum-gateway
./cauteum install   # or: export PATH="$PWD:$PATH"
```

## 2. Start gateway + register it

```bash
./cauteum-gateway --listen 127.0.0.1:7443 &
./cauteum gateway add http://127.0.0.1:7443 --local --name local
./cauteum gateway select local
```

## 3. Seed GitHub token (one-shot — not a sticky shell export)

```bash
# PAT only for this process; do not leave export in your interactive shell
GITHUB_TOKEN=ghp_… ./cauteum provider create --name gh --type github --credential GITHUB_TOKEN
./cauteum provider list   # shows name/type/keys only — never the value
```

## 4. Create a sandbox that can push

Use the write-capable **base policy** from the start (org-scoped `cauteum`):

```bash
./cauteum sandbox create --name push \
  --workspace "$PWD" \
  --policy cauteum-cli/policies/github-push-cauteum.yaml \
  --gateway http://127.0.0.1:7443 \
  --provider gh
# If instance missing: use --provider github (auto-creates from profile + $GITHUB_TOKEN)
# or: GITHUB_TOKEN=… ./cauteum provider create --name gh --type github --credential GITHUB_TOKEN
```

Composition: **base** = push policy (`git-receive-pack` + API write under `/cauteum/**`)
plus **provider-composed** github endpoints/credential binding from `gh`.

Check:

```bash
./cauteum policy get push --base    # editable base
./cauteum policy get push --full    # effective policy (base + provider-composed)
```

### Alternative: create narrow, then widen

```bash
./cauteum sandbox create --name push --workspace "$PWD" \
  --policy cauteum-cli/policies/default.yaml \
  --gateway http://127.0.0.1:7443 --provider gh

# push will DENY until:
./cauteum policy set push --policy cauteum-cli/policies/github-push-cauteum.yaml
```

## 5. Run the agent *inside* the sandbox

Do **not** push from the host with a placeholder token. Examples:

```bash
# one-shot command
./cauteum sandbox exec push -- git -C /workspace/cauteum-cli status

# interactive shell (then run your agent / git / gh)
./cauteum sandbox exec push -- bash

# or create-with-command (keeps sandbox)
./cauteum sandbox create --name agent --workspace "$PWD" \
  --policy cauteum-cli/policies/github-push-cauteum.yaml \
  --gateway http://127.0.0.1:7443 --provider gh \
  -- bash
```

Inside the guest, env looks like:

```text
GITHUB_TOKEN=cauteum:resolve:env:GITHUB_TOKEN
```

`git` / `gh` / `curl` to `github.com` / `api.github.com` go through the sidecar;
the real PAT is substituted only for credential-bound endpoints.

### First push per module

Repos must exist on GitHub. From **inside** the sandbox:

```bash
cd /workspace/cauteum-cli
git remote -v   # should be https://github.com/cautem/cauteum-cli.git
git push -u origin main
```

Repeat for `cauteum-core`, `cauteum-runtime`, `cauteum-gateway` as needed.

## 6. If push is denied

```bash
./cauteum logs push --source proxy | grep -E 'DENIED|FINDING'
./cauteum policy get push --full | head
# fix base, then:
./cauteum policy set push --policy cauteum-cli/policies/github-push-cauteum.yaml
```

Typical causes:

| Symptom | Fix |
|---------|-----|
| DENY `git-receive-pack` | base missing push rules → `policy set` push YAML |
| `credential_endpoint_mismatch` | provider not attached / wrong key binding |
| GitHub 401 | re-seed: `GITHUB_TOKEN=… cauteum provider update gh --credential GITHUB_TOKEN` |
| Host push with placeholder | always `cauteum sandbox exec …` — host has no MITM rewrite |

## 7. Cleanup

```bash
./cauteum sandbox delete push
# optional: stop gateway job
```

## How policies work

cauteum starts **default deny**. Widen the base policy when the agent needs more.

```bash
./cauteum gateway ensure   # starts cauteum-gateway beside CLI if needed
./cauteum sandbox create --from cursor --workspace "$PWD" -- agent
# -- agent infers --provider cursor; gateway ensure runs automatically
```

| Layer | Builtin `github` provider | After base widen (`github-push-cauteum.yaml`) |
|-------|---------------------------|-----------------------------------------------|
| API | `read-only` | write under `/repos/cauteum/**` + **create repo** |
| Git | `git-upload-pack` (clone/fetch) | + `git-receive-pack` (push) |

Typical loop:

1. Agent tries `gh repo create` / `git push` → proxy **DENIED**
2. Operator reads logs → updates **base policy** (`policy set`)
3. Gateway **composition** keeps provider-composed credentials/endpoints
4. Agent retries inside the sandbox

Create-repo allows in our write base policy:

- `POST /orgs/cauteum/repos` — `gh repo create cautem/cauteum-cli`
- `POST /user/repos` — user-owned `gh repo create cauteum-cli`
- then `git-receive-pack` for push

Token still needs GitHub permission (`repo` / admin on org). Policy only admits the HTTP path; GitHub ACLs still apply.

## Agent create + push (inside sandbox)

```bash
./cauteum sandbox exec push -- bash
# inside:
gh repo create cautem/cauteum-cli --private --source=/workspace/cauteum-cli --remote=origin --push
# or:
gh api -X POST /orgs/cauteum/repos -f name=cauteum-cli -F private=true
cd /workspace/cauteum-cli && git remote add origin https://github.com/cautem/cauteum-cli.git
git push -u origin main
```

If DENIED: `./cauteum logs push --source proxy` → adjust base → `./cauteum policy set push …`.
