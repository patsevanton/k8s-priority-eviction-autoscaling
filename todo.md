# TODO

## Исследование: почему KEDA предупреждает о PollingInterval и CooldownPeriod

Warning выдаёт webhook KEDA `verifyScaledObjects` (исходники `apis/keda/v1alpha1/scaledobject_webhook.go`, KEDA v2.20.2). Условия:

```go
// PollingInterval: предупреждение, если minReplicaCount > 0 И (idleReplicaCount не задан ИЛИ != 0) И !useCachedMetrics
if minReplicas > 0 && idleReplicaNotZero && !usesCachedMetrics { warn PollingInterval }
// CooldownPeriod: предупреждение, если minReplicaCount > 0 И (idleReplicaCount не задан ИЛИ != 0)
if minReplicas > 0 && idleReplicaNotZero { warn CooldownPeriod }
```

Почему при `minReplicaCount: 1` оба нерелевантны:

- **CooldownPeriod** — это пауза перед scale-to-zero («после падения нагрузки держим под тёплым, прежде чем уйти в 0»). При `minReplicaCount: 1` скейлинг в 0 запрещён, KEDA никогда не опускает реплики ниже 1 — уменьшение реплик выполняется напрямую через HPA по целевой метрике, cooldown к этому отношения не имеет (`defaultCooldownPeriod = 5 * 60` в `pkg/scaling/executor/scale_executor.go` применяется только в сценарии scale-to-zero).
- **PollingInterval** — период, с которым `startScaleLoop` (`pkg/scaling/scale_handler.go`) пересчитывает активность триггеров через `checkScalers`. Но при `minReplicaCount > 0` фактическим скейлингом управляет HPA: он сам опрашивает KEDA metrics API с периодом `hpa-sync-period` (по умолчанию 15с), а не `pollingInterval`. Собственный цикл KEDA в этом режиме нужен только для (а) определения неактивности → scale-to-zero и (б) наполнения кеша метрик при `useCachedMetrics`. Раз ни того, ни другого нет — значение игнорируется.

Вывод: при `minReplicaCount: 1` без `useCachedMetrics` оба поля можно спокойно удалить из `manifests/keda/scaledobject.yaml` — поведение не изменится. Оставить можно (это warning, не ошибка), но они вводят в заблуждение.

## Fallback: currentReplicas

При недоступности VictoriaMetrics — `fallback.behavior: currentReplicas` (в `manifests/keda/scaledobject.yaml`): при сбое метрик держать текущее число реплик.
