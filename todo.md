# TODO

- [ ] Проверить генерацию RPS по закону Симпсона на живом кластере: RPS должен плавно расти 0→100 по S-образной кривой (излом в точке моды 60), после `CYCLE` держаться на 100, KEDA при этом поднимает реплики business-app. Образ `load-generator:1.1.0` должен быть собран и запушен в GHCR.
- [ ] Подобрать эмпирически `threshold` (25 RPS/реплику) и профиль лестницы под ноду 2 vCPU: цель — чтобы каждая новая реплика реально упиралась в CPU.
- [ ] Выбрать ресурсы для бизнес-приложения (`business-app`) и для capacity-overprovisioning подов (`overprovisioning`): сейчас 250m CPU / 250Mi — временные значения.
## Fallback: currentReplicas

При недоступности VictoriaMetrics — `fallback.behavior: currentReplicas` (в `manifests/keda/scaledobject.yaml`): при сбое метрик держать текущее число реплик.
