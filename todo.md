# TODO

- [ ] Проверить генерацию RPS на живом кластере: RPS должен плавно расти 0→600 по гладкой S-образной кривой smoothstep (без излома, точка перегиба `MIDPOINT` = 0.5), после `CYCLE` держаться на 600, KEDA при этом поднимает реплики business-app. Образ `load-generator` должен быть собран и запушен в GHCR.
- [ ] Пересмотреть `MIDPOINT` (точка перегиба smoothstep, сейчас 0.5) после проверки на живом кластере: нужен ли сдвиг перегиба, или симметричный подъём в середине CYCLE — оптимален.
- [ ] Подобрать эмпирически `threshold` (25 RPS/реплику) и профиль лестницы под ноду 2 vCPU: цель — чтобы каждая новая реплика реально упиралась в CPU.
- [ ] Выбрать ресурсы для бизнес-приложения (`business-app`) и для capacity-overprovisioning подов (`overprovisioning`): сейчас 250m CPU / 250Mi — временные значения.
- [ ] Переписать load-generator (`manifests/keda/load-generator.yaml`, исходники: `apps/load-generator/`) — гоняет на business-app лестницу RPS «день/ночь»: ночь 0 RPS (2м) → 5 RPS (3м) → 20 RPS (3м) → 60 RPS (3м) → пик 100 RPS (5м) → спад 20 RPS (3м) → ночь 0 RPS (2м), затем повтор бесконечно.
## Fallback: currentReplicas

При недоступности VictoriaMetrics — `fallback.behavior: currentReplicas` (в `manifests/keda/scaledobject.yaml`): при сбое метрик держать текущее число реплик.
