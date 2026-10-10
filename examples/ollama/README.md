# Ollama in cautem

Run Ollama on the host, reach it from the sandbox via `host.cautem.internal:11434`

Policy: `filesystem_policy` / `network_policies` + `inference.providers: [local]` — see `policy.yaml`.

**Запуск CLI:** [профили провайдеров](https://cautem.github.io/sandbox.dev/ru/guides/provider-profiles/).

```bash
export GOWORK=$PWD/go.work
go build -C cautem-cli -o ../cautem ./cmd/cautem && ./cautem install
task runtime:image:cli

# host
ollama serve   # listens on 11434

cautem sandbox create --name ollama-demo \
  --policy cautem-cli/examples/ollama/policy.yaml \
  --workspace .

cautem sandbox exec ollama-demo -- curl -s http://host.cautem.internal:11434/api/tags
```

No managed URL rewrite — use the native Ollama HTTP API. See [provider profiles](https://cautem.github.io/sandbox.dev/guides/provider-profiles/).
