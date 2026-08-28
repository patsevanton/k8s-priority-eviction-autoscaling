# TODO

Инструкции, которые нужно перенести в README.md после проверки демо на живом кластере.

## Бизнес-приложения и KEDA: что добавлено в репозиторий

- `apps/business-app/` — бизнес-приложение на Go (HTTP API `/`, метрики Prometheus `/metrics`, пробы `/healthz`, `/readyz`; имитация полезной работы через `REQUEST_LATENCY_MS`).
- `apps/load-generator/` — генератор нагрузки на Go: гоняет лестницу RPS «день/ночь» на `TARGET_URL` по профилю `LOAD_PROFILE` (формат `rps/длительность;...`), `REPEAT` — число итераций (0 — бесконечно).
- `apps/.gitignore` — игнор локальных бинарей сборки.
- `.github/workflows/docker.yml` — CI: semver-релиз и сборка обоих образов в GHCR (`ghcr.io/patsevanton/k8s-priority-eviction-autoscaling/business-app`, `.../load-generator`).
- `manifests/keda/business-app.yaml` — Deployment + Service бизнес-приложения (requests 900m/1Gi — как у pause-пода, чтобы реплика гарантированно вытесняла его).
- `manifests/keda/scaledobject.yaml` — KEDA ScaledObject: Prometheus scaler, запрос `sum(rate(business_app_http_requests_total{route="root"}[1m]))`, threshold 25 RPS на реплику, min 1 / max 4.
- `manifests/keda/load-generator.yaml` — Deployment генератора нагрузки (профиль: 0 → 5 → 20 → 60 → 100 → 20 → 0 RPS, бесконечный повтор).

## Инструкция по запуску демо (перенести в README после проверки)

### 1. Установка KEDA

```bash
helm repo add kedacore https://kedacore.github.io/charts
helm install keda kedacore/keda --namespace keda --create-namespace
```

### 2. Установка VictoriaMetrics (для Prometheus scaler)

Установить `victoria-metrics-k8s-stack` в namespace `vmks` по правилам проекта (отключенные scrape-job и recording-правила для недоступных control-plane компонентов Yandex Managed K8s):

```bash
helm repo add vm https://victoriametrics.github.io/helm-charts/
helm install vmks vm/victoria-metrics-k8s-stack -n vmks --create-namespace -f values-vmks.yaml
```

Минимальные значения `values-vmks.yaml` (полный набор — по правилам инфраструктуры):

```yaml
defaultRules:
  groups:
    etcd:
      enabled: false
    kubernetes-system-scheduler:
      enabled: false
    kubernetes-system-controller-manager:
      enabled: false
    kube-scheduler.rules:
      enabled: false
kubeControllerManager:
  enabled: false
kubeScheduler:
  enabled: false
kubeEtcd:
  enabled: false
```

После установки убедиться, что vmagent скрейпит поды бизнес-приложения (аннотации не нужны: vmsingle/vmagent подхватит `business-app` через kubernetes-pods job при наличии prometheus.io/scrape аннотаций либо добавить explicit scrape-конфиг).

### 3. Сборка и публикация образов

Образы собираются автоматически через GitHub Actions (`.github/workflows/docker.yml`) при пуше в `main`: semver-тег + `latest` в GHCR. Для ручной сборки:

```bash
docker build -t ghcr.io/patsevanton/k8s-priority-eviction-autoscaling/business-app:latest apps/business-app
docker build -t ghcr.io/patsevanton/k8s-priority-eviction-autoscaling/load-generator:latest apps/load-generator
docker push ghcr.io/patsevanton/k8s-priority-eviction-autoscaling/business-app:latest
docker push ghcr.io/patsevanton/k8s-priority-eviction-autoscaling/load-generator:latest
```

### 4. Развёртывание демо

```bash
kubectl apply -f priorityclasses.yaml
kubectl apply -f manifests/overprovisioning.yaml
kubectl apply -f manifests/keda/business-app.yaml
kubectl apply -f manifests/keda/scaledobject.yaml
kubectl apply -f manifests/keda/load-generator.yaml
```

### 5. Наблюдение за масштабированием

```bash
# Реплики business-app растут по RPS
kubectl get deployment business-app -w

# HPA, созданный KEDA
kubectl get hpa keda-hpa-business-app -w

# Вытеснение pause-подов
kubectl get events --sort-by=.lastTimestamp | grep -E 'Preempted|Scaled'

# Прометеус-запрос для проверки метрики RPS
kubectl run -it --rm curl --image=curlimages/curl --restart=Never -- \
  curl -s 'http://vmsingle-vmks.vmks.svc.cluster.local:8428/select/0/prometheus/api/v1/query?query=sum(rate(business_app_http_requests_total[1m]))'
```

Ожидаемый цикл: рост RPS по лестнице → KEDA поднимает реплики (каждая с requests 900m/1Gi) → новые поды вытесняют pause-поды (приоритет -1000) → вытесненные pause-поды в Pending → Cluster Autoscaler добавляет ноду → буфер восстанавливается. При спаде RPS до 0 (ночь) KEDA возвращёт реплики к 1, CA удаляет лишние ноды.

## Открытые вопросы

- [ ] Проверить на живом кластере, что vmagent действительно скрейпит `/metrics` business-app (при необходимости добавить в `manifests/keda/business-app.yaml` аннотации `prometheus.io/scrape: "true"`, `prometheus.io/port: "8080"`, `prometheus.io/path: "/metrics"` или явный scrape-config в values VictoriaMetrics).
- [ ] Подобрать эмпирически `threshold` (25 RPS/реплику) и профиль лестницы под ноду 2 vCPU: цель — чтобы каждая новая реплика реально упиралась в CPU.
- [ ] Проверить имя HPA, который создаёт KEDA (обычно `keda-hpa-<scaledobject-name>`).
- [ ] Решить, публиковать ли `values-vmks.yaml` в репозитории как полноценный файл вместо инструкции в README.
- [ ] Возможно, зафиксировать тег образов в манифестах вместо `latest`.
- [ ] Перепроверить: в README пошаговое демо (шаги 0–5) идёт через `manifests/critical-app.yaml` и ручной `kubectl apply`, а KEDA-сценарий с load-generator'ом (лестница RPS) описан только здесь. Решить, добавить ли KEDA-демо в README или оставить оба сценария.
