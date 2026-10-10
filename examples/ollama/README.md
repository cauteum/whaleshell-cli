# Ollama in cauteum

Run Ollama on the host, reach it from the sandbox via `host.cauteum.internal:11434`

Policy: `filesystem_policy` / `network_policies` + `inference.providers: [local]` — see `policy.yaml`.

**Запуск CLI:** [профили провайдеров](https://cautem.github.io/cauteum-haven.github.io/ru/guides/provider-profiles/).

```bash
export GOWORK=$PWD/go.work
go build -C cauteum-cli -o ../cauteum ./cmd/cauteum && ./cauteum install
task runtime:image:cli

# host
ollama serve   # listens on 11434

cauteum sandbox create --name ollama-demo \
  --policy cauteum-cli/examples/ollama/policy.yaml \
  --workspace .

cauteum sandbox exec ollama-demo -- curl -s http://host.cauteum.internal:11434/api/tags
```

No managed URL rewrite — use the native Ollama HTTP API. See [provider profiles](https://cautem.github.io/cauteum-haven.github.io/guides/provider-profiles/).
