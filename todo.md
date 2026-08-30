# TODO

## Исследование: почему KEDA предупреждает о PollingInterval

Warning выдаёт webhook KEDA `verifyScaledObjects` (исходники `apis/keda/v1alpha1/scaledobject_webhook.go`, KEDA v2.20.2). Условия:

```go
// PollingInterval: предупреждение, если minReplicaCount > 0 И (idleReplicaCount не задан ИЛИ != 0) И !useCachedMetrics
if minReplicas > 0 && idleReplicaNotZero && !usesCachedMetrics { warn PollingInterval }
```

Почему при `minReplicaCount: 1` нерелевантен:

- **PollingInterval** — период, с которым `startScaleLoop` (`pkg/scaling/scale_handler.go`) пересчитывает активность триггеров через `checkScalers`. Но при `minReplicaCount > 0` фактическим скейлингом управляет HPA: он сам опрашивает KEDA metrics API с периодом `hpa-sync-period` (по умолчанию 15с), а не `pollingInterval`. Собственный цикл KEDA в этом режиме нужен только для (а) определения неактивности → scale-to-zero и (б) наполнения кеша метрик при `useCachedMetrics`. Раз ни того, ни другого нет — значение игнорируется.

Вывод: при `minReplicaCount: 1` без `useCachedMetrics` поле можно спокойно удалить из `manifests/keda/scaledobject.yaml` — поведение не изменится. Оставить можно (это warning, не ошибка), но оно вводит в заблуждение.
