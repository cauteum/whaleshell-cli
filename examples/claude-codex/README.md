# Claude / Codex-style agents

Use builtin Anthropic + OpenAI presets **or** a `claude-code` provider instance.
API keys stay on the host / gateway; the sandbox sees `cautem:resolve:env:…` placeholders.

**Запуск + providers:** [профили провайдеров](https://cautem.github.io/sandbox.dev/ru/guides/provider-profiles/).

## A. Inference presets in policy (no gateway provider)

```bash
export GOWORK=$PWD/go.work
# keys on host for rewrite when using local proxy path — prefer gateway provider (B)

cautem sandbox create --name agent-demo \
  --policy cautem-cli/examples/claude-codex/policy.yaml \
  --workspace .
```

## B. Gateway provider (рекомендуется)

```bash
cautem-gateway --listen 127.0.0.1:7443 &
cautem gateway add http://127.0.0.1:7443 --local --name local && cautem gateway select local

ANTHROPIC_API_KEY=… cautem provider create --name claude --type claude-code --credential ANTHROPIC_API_KEY

cautem sandbox create --name agent-demo \
  --policy cautem-cli/examples/claude-codex/policy.yaml \
  --workspace . \
  --gateway http://127.0.0.1:7443 \
  --provider claude

cautem sandbox exec agent-demo -- env | grep -E 'ANTHROPIC|OPENAI'
# expect: ANTHROPIC_API_KEY=cautem:resolve:env:ANTHROPIC_API_KEY
```

Custom host model (vLLM): `inference.profiles` — see [provider profiles](https://cautem.github.io/sandbox.dev/guides/provider-profiles/).
