# TODO

Инструкция по запуску KEDA-демо перенесена в README.md. Ниже — только незакрытые пункты.

## Сборка образов вручную

Образы собираются автоматически через GitHub Actions (`.github/workflows/docker.yml`) при пуше в `main`: semver-тег + `latest` в GHCR. Для ручной сборки:

```bash
docker build -t ghcr.io/patsevanton/k8s-priority-eviction-autoscaling/business-app:latest apps/business-app
docker build -t ghcr.io/patsevanton/k8s-priority-eviction-autoscaling/load-generator:latest apps/load-generator
docker push ghcr.io/patsevanton/k8s-priority-eviction-autoscaling/business-app:latest
docker push ghcr.io/patsevanton/k8s-priority-eviction-autoscaling/load-generator:latest
```

## Открытые вопросы

- [ ] Проверить на живом кластере, что vmagent действительно скрейпит `/metrics` business-app (при необходимости добавить в `manifests/keda/business-app.yaml` аннотации `prometheus.io/scrape: "true"`, `prometheus.io/port: "8080"`, `prometheus.io/path: "/metrics"` или явный scrape-config в values VictoriaMetrics).
- [ ] Подобрать эмпирически `threshold` (25 RPS/реплику) и профиль лестницы под ноду 2 vCPU: цель — чтобы каждая новая реплика реально упиралась в CPU.
- [ ] Проверить имя HPA, который создаёт KEDA (обычно `keda-hpa-<scaledobject-name>`).
- [ ] Решить, публиковать ли `values-vmks.yaml` в репозитории как полноценный файл вместо инструкции в README.
- [ ] Возможно, зафиксировать тег образов в манифестах вместо `latest`.
