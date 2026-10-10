# Claude / Codex-style agents

Use builtin Anthropic + OpenAI presets **or** a `claude-code` provider instance.
API keys stay on the host / gateway; the sandbox sees `cauteum:resolve:env:…` placeholders.

**Запуск + providers:** [профили провайдеров](https://cautem.github.io/cauteum-haven.github.io/ru/guides/provider-profiles/).

## A. Inference presets in policy (no gateway provider)

```bash
export GOWORK=$PWD/go.work
# keys on host for rewrite when using local proxy path — prefer gateway provider (B)

cauteum sandbox create --name agent-demo \
  --policy cauteum-cli/examples/claude-codex/policy.yaml \
  --workspace .
```

## B. Gateway provider (рекомендуется)

```bash
cauteum-gateway --listen 127.0.0.1:7443 &
cauteum gateway add http://127.0.0.1:7443 --local --name local && cauteum gateway select local

ANTHROPIC_API_KEY=… cauteum provider create --name claude --type claude-code --credential ANTHROPIC_API_KEY

cauteum sandbox create --name agent-demo \
  --policy cauteum-cli/examples/claude-codex/policy.yaml \
  --workspace . \
  --gateway http://127.0.0.1:7443 \
  --provider claude

cauteum sandbox exec agent-demo -- env | grep -E 'ANTHROPIC|OPENAI'
# expect: ANTHROPIC_API_KEY=cauteum:resolve:env:ANTHROPIC_API_KEY
```

Custom host model (vLLM): `inference.profiles` — see [provider profiles](https://cautem.github.io/cauteum-haven.github.io/guides/provider-profiles/).
