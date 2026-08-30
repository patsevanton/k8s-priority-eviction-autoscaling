# Правила коммитов

Префикс `feat` используется только при изменениях, связанных с Dockerfile, Go-кодом и образами (image). Все прочие изменения коммитить с другими префиксами (`fix`, `docs`, `chore`, `refactor`, `ci` и т.д.).

Повышение версий image (и соответствующие коммиты с префиксами `feat`, `chore`, `fix` и другими) делается только при изменении кода Go, Dockerfile и т.д. Без изменения исходного кода bump версий image не выполняется.

`values-vmks.yaml` публиковать в README.md не нужно.

# Установка VictoriaMetrics

Для демо приоритетного вытеснения требуется установленный стек VictoriaMetrics
(vmagent + vmsingle + Grafana) в namespace `vmks`:

```bash
helm upgrade --install vmks \
    oci://ghcr.io/victoriametrics/helm-charts/victoria-metrics-k8s-stack \
    --namespace vmks --create-namespace \
    --wait --version 0.90.2 --timeout 15m \
    -f vmks-values.yaml
```
