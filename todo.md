# TODO

Инструкция по запуску KEDA-демо перенесена в README.md. Ниже — только незакрытые пункты.

## Открытые вопросы

- [ ] Подобрать эмпирически `threshold` (25 RPS/реплику) и профиль лестницы под ноду 2 vCPU: цель — чтобы каждая новая реплика реально упиралась в CPU.
- [ ] Выбрать ресурсы для бизнес-приложения (`business-app`) и для capacity-overprovisioning подов (`overprovisioning`): сейчас 250m CPU / 250Mi — временные значения.
## Fallback: currentReplicas

При недоступности VictoriaMetrics — `fallback.behavior: currentReplicas` (в `manifests/keda/scaledobject.yaml`): при сбое метрик держать текущее число реплик.
