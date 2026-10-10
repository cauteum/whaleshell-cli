# NVIDIA provider profile example

Builtin profile: `cauteum-providers/profiles/nvidia.yaml` (also via `cauteum provider profile show nvidia`).

**Запуск + create:** [профили провайдеров](https://cautem.github.io/cauteum-haven.github.io/ru/guides/provider-profiles/).

```bash
export GOWORK=$PWD/go.work
go build -C cauteum-cli -o cauteum ./cmd/cauteum && ./cauteum install
go build -C cauteum-gateway -o cauteum-gateway ./cmd/cauteum-gateway
./cauteum-gateway --listen 127.0.0.1:7443 &
cauteum gateway add http://127.0.0.1:7443 --local --name local && cauteum gateway select local

NVIDIA_API_KEY=… cauteum provider create --name nv --type nvidia --credential NVIDIA_API_KEY

cauteum sandbox create --name nim \
  --policy cauteum-cli/policies/default.yaml \
  --workspace . \
  --gateway http://127.0.0.1:7443 \
  --provider nv

cauteum provider effective nim
cauteum sandbox exec nim -- env | grep NVIDIA || true
# expect: NVIDIA_API_KEY=cauteum:resolve:env:NVIDIA_API_KEY
```

См. [профили провайдеров](https://cautem.github.io/cauteum-haven.github.io/ru/guides/provider-profiles/).
