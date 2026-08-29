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
- [ ] Сделать версионирование образов `business-app` и `load-generator`: вместо `latest` закрепить в манифестах (`manifests/keda/business-app.yaml`, `manifests/keda/load-generator.yaml`) конкретный semver-тег, который пушит GitHub Actions (`${{ steps.semver.outputs.version }}`). Требует первого релиза — сейчас образов с конкретным тегом в GHCR нет, а CI ещё не отрабатывал.
- [ ] Проверить значение PriorityClass для capacity-overprovisioning подов: `-1000` против cutoff Cluster Autoscaler (`--expendable-pods-priority-cutoff`, по умолчанию `-10`). Поды с приоритетом ниже cutoff не триггерят scale-up — ушедший в Pending capacity-overprovisioning под с `-1000` может не вызвать создание ноды. Официальный FAQ Cluster Autoscaler использует `value: -10`.
- [ ] Перепроверить противоречие в README: «приоритет сам по себе не заставляет Cluster Autoscaler ничего делать» vs «Autoscaler увидит нехватку ресурсов для подов с приоритетом -1000». Связано с cutoff `-10` (пункт выше).
- [ ] Перепроверить тег образа `registry.k8s.io/pause:3.10` (README и `manifests/overprovisioning.yaml`). Такого тега может не существовать.
- [ ] Перепроверить реальные имена подов vmsingle/vmagent в `kubectl get pods -n vmks`. По рендеру чарта 0.90.2 CR/StatefulSet называются `vmks-victoria-metrics-k8s-stack`, а не `vmsingle-vmks-victoria-metrics-k8s-stack-0` / `vmagent-vmks-...-0`.
- [ ] Перепроверить `loadBalancerIP` в `monitoring.tf` — поддерживается ли явный IP балансировщика в Yandex Managed K8s (обычно используется аннотация, а не `spec.loadBalancerIP`).
- [ ] Добавить descheduler и проверить, как он будет отрабатывать при снижении нагрузки и уменьшении количества подов.
