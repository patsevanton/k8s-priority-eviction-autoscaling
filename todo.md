# TODO

Инструкция по запуску KEDA-демо перенесена в README.md. Ниже — только незакрытые пункты.

## Открытые вопросы

- [ ] Подобрать эмпирически `threshold` (25 RPS/реплику) и профиль лестницы под ноду 2 vCPU: цель — чтобы каждая новая реплика реально упиралась в CPU.
- [ ] Выбрать ресурсы для бизнес-приложения (`business-app`) и для capacity-overprovisioning подов (`overprovisioning`): сейчас 250m CPU / 250Mi — временные значения.
## Fallback: currentReplicas vs второй ScaledObject

Два варианта на случай недоступности VictoriaMetrics:

1. `fallback.behavior: currentReplicas` (в `manifests/keda/scaledobject.yaml`): при сбое метрик держать текущее число реплик.
2. Второй триггер `cpu` в том же ScaledObject (не отдельный ScaledObject): CPU-метрика остаётся страховкой, пока prometheus-метрика в fallback.

- [ ] Сравнить оба варианта и выбрать один.
- [ ] НЕ использовать два отдельных ScaledObject на один Deployment: KEDA создаст два HPA на один target — они конфликтуют (каждый перезаписывает `desiredReplicas`, «последний победит»). Комбинацию «CPU + prometheus» делать в одном ScaledObject через второй триггер `cpu`.
