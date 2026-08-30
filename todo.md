# TODO

- [ ] Проверить генерацию RPS на живом кластере: RPS должен плавно расти 0→600 по гладкой S-образной кривой smoothstep (без излома, точка перегиба `MIDPOINT` = 0.5), после `CYCLE` держаться на 600, KEDA при этом поднимает реплики business-app. Образ `load-generator` должен быть собран и запушен в GHCR.
- [ ] Пересмотреть `MIDPOINT` (точка перегиба smoothstep, сейчас 0.5) после проверки на живом кластере: нужен ли сдвиг перегиба, или симметричный подъём в середине CYCLE — оптимален.
- [ ] Подобрать эмпирически `threshold` (25 RPS/реплику) и профиль лестницы под ноду 2 vCPU: цель — чтобы каждая новая реплика реально упиралась в CPU.
- [ ] Выбрать ресурсы для бизнес-приложения (`business-app`) и для capacity-overprovisioning подов (`overprovisioning`): сейчас 250m CPU / 250Mi — временные значения.
- [ ] Переписать load-generator (`manifests/keda/load-generator.yaml`, исходники: `apps/load-generator/`) — гоняет на business-app лестницу RPS «день/ночь»: ночь 0 RPS (2м) → 5 RPS (3м) → 20 RPS (3м) → 60 RPS (3м) → пик 100 RPS (5м) → спад 20 RPS (3м) → ночь 0 RPS (2м), затем повтор бесконечно.
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
