# PriorityClass, Cluster Autoscaler и «тёплый» резерв через placeholder-поды в Kubernetes

нагружаем RPS для бизнес сервисов постепенно и увеличиваем кол-во через KEDA

Задача, знакомая многим командам: в кластере Kubernetes живут критически важные сервисы, нагрузка на которые днём растёт, а ночью падает. Обычно выбирают между двумя плохими вариантами: держать статический пул нод под дневной пик, которые ночью простаивают и стоят денег, или положиться на cluster autoscaling — и тогда при дневном росте нагрузки бизнес-поды проведут в статусе Pending до 10 минут, пока автоскейлер развернёт новую ноду.

В Kubernetes эта проблема решается паттерном [node overprovisioning](https://kubernetes.io/docs/tasks/administer-cluster/node-overprovisioning/), настраиваемым через PriorityClass и механизмы Cluster Autoscaler: мы **заранее** запускаем ничего не делающие placeholder-поды, которые через `resources.requests` занимают небольшую часть ресурсов нод. Когда приходит нагрузка и реплики бизнес-приложений увеличиваются — поды бизнес-приложений немедленно занимают освободившееся место, вытесняя placeholder за секунды. Вытесненный placeholder уходит в Pending, Cluster Autoscaler добавляет ноду, и буфер восстанавливается сам.

В этой статье разберём, как это устроено внутри, и развернём полный демо-стенд в Yandex Managed Kubernetes: от Terraform-кластера с автоскейлингом до живого вытеснения pause-подов, которое можно наблюдать своими глазами в `kubectl get events`.

## Как это работает: три механизма в связке

Прежде чем идти к практике, важно понять, что приоритетное вытеснение — это не одна фича, а результат работы трёх компонентов Kubernetes, которые существуют независимо друг от друга:

**1. Приоритет пода (PriorityClass).** Каждый под получает числовое значение приоритета — напрямую или через PriorityClass. Это число хранится в `pod.spec.priority` и учитывается в двух местах: планировщиком при выборе жертвы вытеснения и kubelet при eviction под давлением ресурсов.

**2. Вытеснение (Preemption).** Когда высокоприоритетный под не может разместиться, планировщик Kubernetes не просто оставляет его в Pending — он ищет ноды, где сумма приоритетов работающих подов ниже, чем у кандидата. Если удаляя один или несколько низкоприоритетных подов планировщик освобождает достаточно ресурсов — он их вытесняет. Важный нюанс: планировщик сравнивает приоритеты только тогда, когда физически не может разместить под из-за нехватки запрошенных ресурсов (`resources.requests`). Без корректно выставленных requests вся эта механика не работает.

**3. Автоскейлинг нод (Cluster Autoscaler).** Вытеснённый placeholder-под (или любой другой, который не помещается) переходит в статус Pending. Cluster Autoscaler видит поды, которые не могут разместиться, и разворачивает новую ноду. Спустя до 10 минут (типичное время создания ноды в облаке) буфер восстанавливается.

Получается замкнутый цикл: **запас placeholder-подов занят на нодах → нагрузка растёт → реплики бизнес-приложений увеличиваются (например, через HPA) → placeholder вытесняется за секунды → реальный под запущен → placeholder в Pending → новая нода → буфер восстановлен**. Кластер переживает рост нагрузки без ручного вмешательства и без ожиданий до 10 минут.

```
   низкая нагрузка                         рост нагрузки
   ┌────────────────────┐                  ┌────────────────────┐
   │ нода N1            │                  │ нода N1            │
   │ services + pause   │  pause вытеснен  │ services + NEW pod │
   │ placeholder        │ ───────────────► │   (запущен сразу)  │
   │ (держит резерв)    │                  └────────────────────┘
   └────────────────────┘                          │
              ▲                                    ▼
              │                          pause → Pending
              └── новая нода N2 ◄── Cluster Autoscaler
                  (буфер восстановлен)
```

Тот же цикл можно изобразить по-разному — ниже несколько альтернативных схем, от «линейного флоу» до диаграммы последовательности и замкнутого контура.

**Вариант A — линейное флоу по этапам**

```
┌────────────────────┐   ┌────────────────────┐   ┌────────────────────┐   ┌────────────────────┐   ┌────────────────────┐
│ 1. Резерв занят:   │─► │ 2. Рост нагрузки:  │─► │ 3. Вытеснение:     │─► │ 4. Scale-up:       │─► │ 5. Восстановление: │
│ placeholder        │   │ HPA создаёт        │   │ placeholder        │   │ CA создаёт ноду    │   │ placeholder        │
│ держит буфер       │   │ новые реплики      │   │ → Pending          │   │ (Pending виден)    │   │ Running            │
│ на нодах           │   └────────────────────┘   │ (секунды)          │   └────────────────────┘   │ буфер готов        │
└────────────────────┘                            └────────────────────┘                            └────────────────────┘
                                             цикл повторяется
```

**Вариант B — диаграмма последовательности (кто кому что сигналит)**

```
Нагрузка/HPA              Планировщик                       Cluster Autoscaler
   │                         │                                 │
   │ новая реплика           │                                 │
   │─────────────────────►   │                                 │
   │                         │ placeholder имеет               │
   │                         │ приоритет -1000                 │
   │                         │ → можно вытеснить               │
   │                         │                                 │
   │                         │ вытеснение:                     │
   │                         │ placeholder → Pending           │
   │                         │─────────────────────────────►   │
   │                         │                                 │ получает сигнал
   │                         │                                 │
   │                         │                                 │ создаёт ноду
   │                         │                                 │
   │                         │ буфер восстановлен:             │
   │                         │ placeholder Running             │
```

**Вариант C — состояния по вертикали (state machine)**

```
┌───────────────────────────┐
│ Свободные ресурсы:        │
│ placeholder держит буфер  │
└───────────────────────────┘
              │
              ▼
┌───────────────────────────┐
│ Рост нагрузки:            │
│ HPA создаёт новые реплики │
└───────────────────────────┘
              │
              ▼
┌───────────────────────────┐
│ Вытеснение:               │
│ placeholder → Pending     │
└───────────────────────────┘
              │
              ▼
┌───────────────────────────┐
│ Scale-up:                 │
│ CA добавляет ноду         │
└───────────────────────────┘
              │
              ▼
┌───────────────────────────┐
│ Восстановление:           │
│ placeholder снова Running │
└───────────────────────────┘
              (цикл повторяется)
```

**Вариант D — замкнутый цикл (return-arrow)**

```
┌────────────────────────────────────────────────┐
│                                                ▲
┌────────────────────────────────────────────┐   │
│ Дежурный режим:  placeholder держит буфер │   │
└────────────────────────────────────────────┘   │
                       │                         │
                       ▼                         │
┌────────────────────────────────────────────┐   │
│ Рост нагрузки: HPA создаёт новые реплики   │   │
└────────────────────────────────────────────┘   │
                       │                         │
                       ▼                         │
┌────────────────────────────────────────────┐   │
│ Вытеснение: placeholder → Pending          │   │
└────────────────────────────────────────────┘   │
                       │                         │
                       ▼                         │
┌────────────────────────────────────────────┐   │
│ Scale-up: CA добавляет новую ноду          │   │
└────────────────────────────────────────────┘   │
                       │                         │
                       ▼                         │
┌────────────────────────────────────────────┐   │
│ Восстановление: placeholder снова Running  │   │
└────────────────────────────────────────────┘   │
                       │                         │
                       └─────────────────────────┘
```

## Как это настроить

Настройка состоит из двух шагов: PriorityClass для placeholder-подов и Deployment с pause-контейнерами.

### Шаг 1. Создайте PriorityClass для placeholder-подов

Для placeholder-подов создайте класс с **отрицательным значением** — благодаря этому они становятся первой жертвой вытеснения:

```yaml
apiVersion: scheduling.k8s.io/v1
kind: PriorityClass
metadata:
  name: overprovisioning-placeholder
value: -1000
globalDefault: false
description: "Отрицательный приоритет для pause-подов резервирования мощностей"
```

### Шаг 2. Разверните placeholder Deployment

В манифесте Deployment явно укажите созданный отрицательный приоритет, минимальный образ `pause` и `terminationGracePeriodSeconds: 0`:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: overprovisioning
spec:
  replicas: 2 # Сколько буфера держим: replicas × requests
  selector:
    matchLabels:
      app: overprovisioning
  template:
    metadata:
      labels:
        app: overprovisioning
    spec:
      priorityClassName: overprovisioning-placeholder
      terminationGracePeriodSeconds: 0 # Резерв должен освобождаться мгновенно
      affinity:
        podAntiAffinity: # Раскидываем буфер по разным нодам
          preferredDuringSchedulingIgnoredDuringExecution:
          - weight: 100
            podAffinityTerm:
              labelSelector:
                matchLabels:
                  app: overprovisioning
              topologyKey: topology.kubernetes.io/hostname
      containers:
      - name: pause
        image: registry.k8s.io/pause:3.10 # Контейнер, который просто «спит»
        resources:
          requests:
            cpu: 900m # Размер резерва, который держит один placeholder
            memory: 1Gi
```

Ключевые детали:

- **`value: -1000`.** Отрицательный приоритет гарантирует, что placeholder будет первой жертвой вытеснения: поды без `priorityClassName` получают приоритет **0**, то есть уже выше placeholder'ов. Этого достаточно, чтобы обычные поды вытесняли placeholder'ы — дефолтный PriorityClass не нужен.
- **`requests` — это и есть размер резерва.** Pause-контейнер потребляет копейки; placeholder «занимает» ровно столько, сколько заявлено в `resources.requests`. Общий буфер = `replicas × requests`. Готовый манифест для демо-стенда — [manifests/overprovisioning.yaml](manifests/overprovisioning.yaml).
- **`terminationGracePeriodSeconds: 0`.** Стандартные 30 секунд graceful shutdown — это 30 секунд задержки перед тем, как место освободится. Placeholder нечего «корректно завершать», поэтому выключаем ожидание.
- **Pod anti-affinity** (`preferredDuringSchedulingIgnoredDuringExecution` по hostname) — «мягкое» правило, старающееся распределить placeholder-поды по разным нодам. Без него весь буфер может осесть на одной ноде — и при её сбое вы теряете весь резерв разом.

### requests запускает масштабирование, priority разрешает вытеснение

Здесь легко запутаться, поэтому разделим роли явно:

- **`resources.requests` placeholder-пода — триггер scale-up.** Приоритет сам по себе не заставляет Cluster Autoscaler ничего делать: автоскейлер реагирует только на поды в состоянии `Unschedulable` из-за нехватки ресурсов. Placeholder с requests 900m CPU / 1Gi, который не помещается, — это и есть сигнал «нужна нода».
- **Отрицательный `priority` — включатель мгновенного вытеснения.** Он гарантирует, что занятый placeholder'ом объём будет отдан новому поду немедленно, без ожидания.

### Trade-off: вы платите за скорость

Placeholder-поды не бесплатны: буфер — это простаивающие мощности, за которые идёт обычная оплата. Взамен вы покупаете скорость реакции кластера: секунды вместо минут. Паттерн окупается при непредсказуемых всплесках с жёсткими требованиями к времени развёртывания; при плавной предсказуемой нагрузке дешевле полагаться на обычный реактивный автоскейлинг.

> Нюанс для других облаков: некоторые автоскейлеры трактуют `preferred` anti-affinity как жёсткое ограничение при масштабировании — число реплик placeholder-Deployment задаёт тем самым минимальное число нод. Классический Cluster Autoscaler (и Yandex Managed K8s) так не делает: для него anti-affinity — только предпочтение при размещении.

## Демо: вытеснение в живом кластере

Теперь самое интересное — развернём кластер и спровоцируем вытеснение. Предполагается, что кластер уже развёрнут через Terraform ([INFRASTRUCTURE.md](INFRASTRUCTURE.md)) и kubeconfig получен.

Стенд: одна нода 2 vCPU / 4 ГБ, node group `auto_scale { min = 1, max = 3 }` — это и есть включённый Cluster Autoscaler. В Yandex Managed K8s отдельная установка Cluster Autoscaler не нужна: `auto_scale` вместо `fixed_scale` — и платформа запускает управляемый автоскейлер.

### Шаг 0. Проверяем стенд

```bash
$ kubectl get nodes
NAME                       STATUS   ROLES    AGE   VERSION
cl1v2fmpkgn4srb2b1mm-uxyz   Ready    <none>   3m    v1.33.x

$ kubectl get priorityclasses.scheduling.k8s.io
NAME                      VALUE        GLOBAL-DEFAULT   AGE
system-cluster-critical   2000000000   false            10m
system-node-critical      2000001000   false            10m
```

### Шаг 1. Применяем PriorityClass

```bash
kubectl apply -f priorityclasses.yaml
```

```bash
$ kubectl get priorityclasses.scheduling.k8s.io
NAME                        VALUE        GLOBAL-DEFAULT   AGE
overprovisioning-placeholder -1000       false            5s    ← для pause-заглушек
system-cluster-critical     2000000000   false            10m
system-node-critical        2000001000   false            10m
```

Проверяем, что приоритет применился к обычному поду (pause-контейнер — минимальный образ, который ничего не делает):

```bash
$ kubectl run test-pod --image=registry.k8s.io/pause:3.10 --restart=Never
$ kubectl get pod test-pod -o jsonpath='{.spec.priority} {"\n"}'
0
$ kubectl delete pod test-pod
```

Приоритет 0 выше -1000 у placeholder'ов — этого достаточно для вытеснения.

### Шаг 2. Запускаем placeholder-поды

```bash
kubectl apply -f manifests/overprovisioning.yaml
```

Две реплики с requests 900m CPU / 1Gi каждая не помещаются на стартовую ноду (600m CPU и ~1 ГБ уже уходят на системные поды и резервы kubelet) — Cluster Autoscaler разворачивает под них вторую ноду:

```bash
$ kubectl get pods -o wide
NAME                              READY   STATUS    NODE
overprovisioning-...-a1b2c        1/1     Running   cl1...-uabc   ← «тёплая» нода
overprovisioning-...-d3e4f        0/1     Pending                 ← вторая заглушка ждёт

$ kubectl get nodes
NAME                       STATUS   ROLES    AGE
cl1v2fmpkgn4srb2b1mm-uxyz   Ready    <none>   18m
cl1v2fmpkgn4srb2b1mm-uabc   Ready    <none>   2m    ← новая нода под placeholder
```

Анти-аффинность в манифесте распределяет заглушки по разным нодам, поэтому одна заняла новую ноду, а вторая осталась в Pending — она и удерживает Autoscaler от scale-down обратно к одной ноде.

### Шаг 3. Запускаем критичное приложение — имитация роста нагрузки

В реальной жизни этот момент выглядит так: нагрузка на бизнес-сервис растёт, HPA поднимает `replicas`, и новые поды должны стартовать немедленно. В демо мы имитируем этот рост одним `kubectl apply` — critical-app играет роль «реплики, которую HPA только что создал».

```bash
kubectl apply -f manifests/critical-app.yaml
```

critical-app (requests 900m / 1Gi, приоритет 0) находит ноду с placeholder'ом (приоритет -1000) и вытесняет его. Благодаря `terminationGracePeriodSeconds: 0` место освобождается сразу — критичный под стартует за секунды, не дожидаясь создания новой ноды:

```bash
$ kubectl get events --sort-by=.lastTimestamp | tail -4
LAST SEEN   TYPE      REASON      OBJECT                          MESSAGE
20s         Normal    Scheduled   pod/critical-app-...-7g8h9      Successfully assigned default/critical-app-...-7g8h9 to cl1...-uabc
35s         Normal    Preempted   pod/overprovisioning-...-a1b2c  By default/critical-app-...-7g8h9 on node cl1...-uabc
35s         Normal    Killing     pod/overprovisioning-...-a1b2c  Stopping container pause
```

Ключевое событие — `Preempted ... By default/critical-app-...`: планировщик явно показывает, кто кого вытеснил.

Состояние подов: critical-app работает на «тёплой» ноде, вытесненная заглушка — в Pending:

```bash
$ kubectl get pods -o wide
NAME                              READY   STATUS    NODE
critical-app-...-7g8h9            1/1     Running   cl1...-uabc
overprovisioning-...-a1b2c        0/1     Pending                 ← вытеснена
overprovisioning-...-d3e4f        0/1     Pending
```

Полезно заглянуть и в статус вытесняющего пода: пока жертва завершается, планировщик «номинирует» для него ноду в поле `nominatedNodeName`:

```bash
$ kubectl get pod critical-app-...-7g8h9 -o jsonpath='{.status.nominatedNodeName} {"\n"}'
cl1v2fmpkgn4srb2b1mm-uabc
```

Номинация — не гарантия: если за время graceful shutdown жертвы освободится другая нода (или придёт ещё более приоритетный под и займёт место), фактическая нода размещения может отличаться, а `nominatedNodeName` будет очищен. Для placeholder-подов с `terminationGracePeriodSeconds: 0` задержка нулевая.

### Шаг 4. Cluster Autoscaler восстанавливает буфер

Теперь в кластере два Pending placeholder-пода — они не помещаются на оставшиеся ноды. Через до 10 минут Cluster Autoscaler разворачивает третью ноду (в рамках лимита `max = 3`):

```bash
$ kubectl get nodes -w
NAME                       STATUS   ROLES    AGE
cl1v2fmpkgn4srb2b1mm-uxyz   Ready    <none>   25m
cl1v2fmpkgn4srb2b1mm-uabc   Ready    <none>   9m
cl1v2fmpkgn4srb2b1mm-wxyz   Ready    <none>   47s   ← новая нода под буфер
```

Placeholder-поды запускаются на новой ноде, и «тёплый» резерв восстановлен:

```bash
$ kubectl get pods -o wide
NAME                              READY   STATUS    NODE
critical-app-...-7g8h9            1/1     Running   cl1...-uabc
overprovisioning-...-a1b2c        1/1     Running   cl1...-wxyz   ← буфер снова готов
overprovisioning-...-d3e4f        1/1     Running   cl1...-wxyz
```

Полный цикл замкнулся: **рост нагрузки → мгновенное вытеснение → реальный под работает → новая нода → буфер восстановлен**.

### Шаг 5. Проверяем scale-down

Удаляем critical-app и placeholder-поды — через несколько минут Cluster Autoscaler поймёт, что лишние ноды недозагружены, и удалит их:

```bash
kubectl delete -f manifests/critical-app.yaml
kubectl delete -f manifests/overprovisioning.yaml

$ kubectl get nodes -w
# ... спустя ~10 минут
NAME                       STATUS                     ROLES    AGE
cl1v2fmpkgn4srb2b1mm-uxyz   Ready                      <none>   35m
cl1v2fmpkgn4srb2b1mm-uabc   Ready,SchedulingDisabled   <none>   17m   ← cordoned
# затем нода удаляется
```

> Cluster Autoscaler удаляет ноду, только если все её поды можно перенести на другие ноды. Не забудьте, что scale-down требует, чтобы суммарные requests помещались на оставшиеся ноды.

### Что пошло не так? (Траблшутинг)

**Вытеснение не происходит, критичный под в Pending.** Проверьте requests у Deployment: без `resources.requests` планировщик считает под «бесплатным» и не вытесняет никого.

```bash
kubectl describe pod <pod> | grep -A3 "Requests"
```

**Placeholder-поды не создают новую ноду.** Проверьте, что node group действительно с auto_scale, и посмотрите события:

```bash
yc managed-kubernetes node-group list
yc managed-kubernetes node-group get <node-group-id> --show-log
```

**Под в Pending навсегда.** Cluster Autoscaler не может расширить группу сверх `max` (в нашем демо — 3) или упёрся в квоту каталога. Проверьте лимиты:

```bash
yc resource-manager folder list-limits --name <folder>
```

**Критичный под запустился не мгновенно.** Убедитесь, что у placeholder-подов стоит `terminationGracePeriodSeconds: 0` — иначе место освобождается только после 30-секундного graceful shutdown. Также проверьте, что у критичного пода нет `preStop`-хуков, замедляющих старт.

### Очистка

```bash
kubectl delete -f manifests/critical-app.yaml
kubectl delete -f manifests/overprovisioning.yaml
kubectl delete -f priorityclasses.yaml
terraform destroy
```

## Важные нюансы для корректной работы

**Настройка Cluster Autoscaler.** Убедитесь, что у вас включён автоскейлинг нод. Когда placeholder-поды уйдут в Pending, Autoscaler увидит нехватку ресурсов для подов с приоритетом -1000 и начнёт создавать новую ноду — восстанавливать буфер. В Yandex Managed K8s это делается через `auto_scale` в node group; в self-hosted — установкой Cluster Autoscaler с явным указанием минимального и максимального размера групп нод.

**Запросы ресурсов (Requests).** Вытеснение сработает только в том случае, если у ваших подов чётко прописаны `resources.requests`. Kubernetes сравнивает приоритеты только тогда, когда физически не может разместить под из-за нехватки запрошенных ресурсов. Под без requests для планировщика «весит ноль» — он не вытеснит никого и сам не станет причиной масштабирования. Это самая частая причина, почему приоритеты «не работают». Полезно запомнить разделение ролей: **requests — триггер масштабирования, priority — право вытеснять**. Приоритет сам по себе не заставляет Cluster Autoscaler создавать ноды — тот реагирует только на поды в состоянии `Unschedulable` из-за нехватки ресурсов.

**Поды без requests и limits (BestEffort).** Если в namespace нет LimitRange, под без requests и limits получает QoS-класс `BestEffort` — и вся приоритетная механика для него переворачивается:

- **Для планировщика он весит ноль.** Он разместится на любую ноду, даже полностью «занятую» по requests; по нехватке ресурсов он никогда не бывает Unschedulable, а значит — никогда не подаст Cluster Autoscaler сигнал «нужна нода».
- **Его нельзя вытеснить ради освобождения места.** Удаление пода с requests = 0 освобождает для планировщика ровно ноль, поэтому scheduler preemption не выбирает его жертвой: реально занятые им гигабайты памяти «непробиваемы» для приоритетного вытеснения.
- **Зато он первый кандидат на node-pressure eviction и OOM-killer.** Kubelet при нехватке памяти ранжирует поды сначала по превышению requests и только потом по приоритету — а BestEffort «превышает» всегда (requests = 0). Плюс kubelet выставляет BestEffort-подам максимальный `oom_score_adj = 1000`, поэтому kernel OOM-killer бьёт по ним первыми — независимо от назначенного PriorityClass.

Парадокс: под без requests одновременно «неуязвим» для вытеснения планировщиком и «самый уязвимый» для eviction kubelet'ом — мониторинг без requests умрёт первым именно в момент инцидента, когда он нужнее всего. Вывод: для схемы «приоритеты + overprovisioning» requests должны стоять у всех участников — и у вытесняющих, и у жертв. Если LimitRange не используется, контролируйте это на уровне манифестов и values (например, проверяйте в CI, что у каждого контейнера указаны requests, — включая компоненты мониторинга вроде vmagent, vmsingle, alertmanager).

**Диапазон значений и имена PriorityClass.** Значение `value` — 32-битное целое от -2147483648 до 1000000000; всё, что выше миллиарда, зарезервировано за встроенными системными классами (`system-cluster-critical` = 2000000000, `system-node-critical` = 2000001000). Имя класса должно быть валидным DNS-именем и не может начинаться с префикса `system-`. Отрицательные значения — не хак, а штатный механизм, официально используемый для placeholder-подов.

**Защита от циклического перезапуска (Flapping).** Если новая нода создаётся слишком долго, placeholder-поды будут находиться в Pending. Как только нода поднимется, они запустятся там и восстановят буфер. Убедитесь, что лимиты автоскейлера позволяют расширять кластер (`max` в auto_scale не должен упираться в квоту облака), и что развертывание подов не блокируется чем-то ещё — например, лимитами namespace (ResourceQuota) или отсутствием доступа к реестру образов.

**Политика вытеснения.** По умолчанию свойство `preemptionPolicy` в PriorityClass имеет значение `PreemptLowerPriority`. Это именно то, что вам нужно (высокий вытесняет низкий, включая отрицательные placeholder-поды). Альтернативное значение `Never` означает, что под с таким классом не будет вытеснять других — он честно ждёт в Pending, но для нашего паттерна нужен именно дефолт.

**Graceful shutdown.** Вытеснение — это обычное удаление пода с соблюдением `terminationGracePeriodSeconds` и preStop-хуков. Значение по умолчанию — 30 секунд, и всё это время место на ноде считается занятым. Для placeholder-подов graceful shutdown не нужен вовсе — там ставят `terminationGracePeriodSeconds: 0`, чтобы резерв освобождался мгновенно. А вот для критичных подов, которые вытесняют placeholder-поды, важно, чтобы они корректно обрабатывали SIGTERM — иначе мгновенное вытеснение placeholder'а не гарантирует мгновенное освобождение места: реальная задержка будет ровно по `terminationGracePeriodSeconds` критичного пода.

**PodDisruptionBudget — best effort.** Планировщик старается не нарушать PDB при выборе жертв, но если жертв без нарушения PDB нет — вытеснение всё равно произойдёт. Не рассчитывайте на PDB как на защиту от приоритетного вытеснения: он ограничивает добровольные disruptions (drain, вытеснения при обновлениях), а не scheduler preemption.

**Inter-pod affinity — только к равным или более высоким приоритетам.** Нода рассматривается как кандидат на вытеснение, только если удаление с неё всех низкоприоритетных подов позволило бы разместить новичка.

**Мульти-тенантность: приоритеты как объект атаки.** В кластере, где поды создают не только доверенные команды, пользователь может создать под с максимально высоким приоритетом и начать вытеснять чужие нагрузки, или, наоборот, «похитить» placeholder-буфер. Защита — ResourceQuota с ограничением потребления PriorityClass ([scopeSelector по priorityClassName](https://kubernetes.io/docs/concepts/policy/resource-quotas/)): например, квотой, разрешающей использовать `overprovisioning-placeholder` только в определённых namespace.

**Не путайте с eviction под давлением.** PriorityClass также влияет на порядок, в котором kubelet выселяет поды при нехватке памяти на ноде (node-pressure eviction) — но это другой механизм с другими причинами. Причём kubelet ранжирует поды иначе: сначала — превышение requests по дефицитному ресурсу, только потом — приоритет. QoS-класс пода в вытеснении планировщиком вообще не участвует. В этой статье речь о вытеснении планировщиком (scheduler preemption), которое происходит из-за нехватки места именно для нового пода.

## Когда этот паттерн уместен

Overprovisioning через placeholder-поды — это про покупку скорости за деньги: он окупается при непредсказуемых всплесках с жёсткими SLA (flash sales, продакшен с дорогим простоем), и избыточен при предсказуемой нагрузке, где обычного реактивного автоскейлинга достаточно.

Паттерн особенно хорош для критичных сервисов, которые **должны разворачиваться немедленно**: web-frontend при flash-трафике, API-шлюзы, сервисы с SLO на время ответа, а также batch/ML-задачи, если вы им дали высокий приоритет и хотите, чтобы они стартовали сразу — а не ждали создания ноды.

Обратная сторона: если placeholder-буфер занимает ноды, а автоскейлер по какой-то причине не может расширить кластер (квота, лимит max, недоступность зон), вытесненные placeholder-поды будут ждать в Pending неограниченно долго, и буфер не восстановится. Приоритеты — не замена мониторингу: алерт на долго живущие Pending-поды и на не расширяющиеся node groups должен быть в любом случае.

## Итоги

Мы развернули в Yandex Managed Kubernetes кластер с автоскейлингом, настроили отрицательный PriorityClass для placeholder-подов — и пронаблюдали полный цикл: критичное приложение мгновенно вытеснило placeholder-под, буфер ушёл в Pending, Cluster Autoscaler добавил ноду, и резерв восстановился. Паттерн node overprovisioning даёт «тёплый» резерв: место под будущий пик держится заранее, а при всплеске освобождается за секунды.

Все конфигурации из статьи:

- [INFRASTRUCTURE.md](INFRASTRUCTURE.md) — Terraform для кластера с `auto_scale` node group
- [priorityclasses.yaml](priorityclasses.yaml) — PriorityClass для placeholder-подов с отрицательным значением
- [manifests/critical-app.yaml](manifests/critical-app.yaml) — критичное приложение
- [manifests/overprovisioning.yaml](manifests/overprovisioning.yaml) — placeholder-поды для «тёплого» резерва

Главное, что стоит запомнить: приоритеты работают только при корректных `resources.requests`; **requests — триггер масштабирования, priority — право вытеснять**; поды без `priorityClassName` получают приоритет 0 и уже вытесняют placeholder'ы с отрицательным значением; `auto_scale` в Yandex Managed K8s — это Cluster Autoscaler из коробки; а если скорость реакции важнее цены — placeholder-поды с отрицательным приоритетом дают «тёплый» резерв мощностей.
