# PriorityClass, Cluster Autoscaler и «тёплый» резерв через capacity-overprovisioning поды в Kubernetes

Ситуация, знакомая многим командам: в кластере Kubernetes живут обычные бизнес сервисы, нагрузка на которые днём растёт, а ночью падает. Обычно используют два плохих варианта: держать статический пул нод под дневной пик, которые ночью простаивают и стоят денег, или положиться на cluster autoscaling — и тогда при дневном росте нагрузки бизнес-поды проведут в статусе Pending до создания новой ноды.

Проблема ожидания, пока Cluster Autoscaler развернёт новую ноду, решается паттерном [node overprovisioning](https://kubernetes.io/docs/tasks/administer-cluster/node-overprovisioning/), настраиваемым через PriorityClass и механизмы Cluster Autoscaler: мы **заранее** запускаем ничего не делающие capacity-overprovisioning поды (в документации Kubernetes они называются placeholder-подами), которые занимают небольшую часть ресурсов нод. Когда приходит нагрузка и реплики бизнес-приложений увеличиваются — поды бизнес-приложений немедленно занимают освободившееся место, вытесняя capacity-overprovisioning под за секунды. Вытесненный capacity-overprovisioning под уходит в Pending, Cluster Autoscaler добавляет ноду, и буфер восстанавливается сам.

В этой статье разберём, как это устроено внутри, и развернём полный демо-стенд в Yandex Managed Kubernetes: от Terraform-кластера с автоскейлингом, KEDA-автоскейлинга бизнес-приложения по RPS, который генерирует генератор нагрузки, до живого вытеснения capacity-overprovisioning подов, которое можно наблюдать своими глазами в `kubectl get events`.

## Как это работает: три механизма в связке

Прежде чем идти к практике, важно понять, что приоритетное вытеснение — это не одна фича, а результат работы трёх компонентов Kubernetes, которые существуют независимо друг от друга:

**1. Приоритет пода (PriorityClass).** Каждый под имеет приоритет 0 либо `priorityClassName` или приоритет выставляемый `globalDefault` из какого-либо PriorityClass. Приоритет учитывается в двух местах: планировщиком при выборе жертвы вытеснения и kubelet при eviction под давлением ресурсов.

**2. Вытеснение (Preemption).** Когда высокоприоритетный под не может разместиться, планировщик Kubernetes не просто переводит его в Pending — он ищет ноды, где, удалив один или несколько подов с приоритетом ниже, чем у кандидата, можно освободить достаточно ресурсов — и вытесняет их. Приоритеты не суммируются: планировщик сравнивает приоритет каждого пода-жертвы с приоритетом кандидата по отдельности, а суммирует только освобождаемые ресурсы (`resources.requests`). Важный нюанс: планировщик сравнивает приоритеты только тогда, когда физически не может разместить под из-за нехватки запрошенных ресурсов (`resources.requests`). Без корректно выставленных requests вся эта механика не работает.

**3. Автоскейлинг нод (Cluster Autoscaler).** Вытеснённый capacity-overprovisioning под (или любой другой, который не помещается) переходит в статус Pending. Cluster Autoscaler видит поды, которые не могут разместиться, и разворачивает новую ноду. Буфер восстанавливается после того, как нода будет подготовлена в облаке.

Получается замкнутый цикл: **запас capacity-overprovisioning подов занят на нодах → нагрузка растёт → реплики бизнес-приложений увеличиваются (например, через HPA) → capacity-overprovisioning под вытесняется за секунды → реальный под запущен → capacity-overprovisioning под в Pending → новая нода → буфер восстановлен**. Кластер переживает рост нагрузки без ручного вмешательства и без ожидания, пока в облаке подготовится новая нода.

**Вариант C — замкнутый цикл (return-arrow)**

```
┌─────────────────────────────────────────────────────────────────┐
│                                                                 ▲
┌─────────────────────────────────────────────────────────────┐   │
│ Нода имеет свободные ресурсы:                               │   │
│ capacity-overprovisioning поды в статусе Running            │   │
└─────────────────────────────────────────────────────────────┘   │
                               │                                  │
                               ▼                                  │
┌─────────────────────────────────────────────────────────────┐   │
│ Рост нагрузки: HPA создаёт новые реплики бизнес приложения  │   │
└─────────────────────────────────────────────────────────────┘   │
                               │                                  │
                               ▼                                  │
┌─────────────────────────────────────────────────────────────┐   │
│ Вытеснение: capacity-overprovisioning под → Pending         │   │
└─────────────────────────────────────────────────────────────┘   │
                               │                                  │
                               ▼                                  │
┌─────────────────────────────────────────────────────────────┐   │
│ Scale-up: Cluster Autoscaler добавляет новую ноду           │   │
└─────────────────────────────────────────────────────────────┘   │
                               │                                  │
                               ▼                                  │
┌─────────────────────────────────────────────────────────────┐   │
│ Восстановление: capacity-overprovisioning под снова Running │   │
└─────────────────────────────────────────────────────────────┘   │
                               │                                  │
                               └──────────────────────────────────┘

```
## Как это настроить

Настройка состоит из двух шагов: PriorityClass для capacity-overprovisioning подов и Deployment с контейнером `pause`.

### Шаг 1. Создайте PriorityClass для capacity-overprovisioning подов

Для capacity-overprovisioning подов создайте класс с **отрицательным значением** — благодаря этому они становятся первой жертвой вытеснения:

```yaml
apiVersion: scheduling.k8s.io/v1
kind: PriorityClass
metadata:
  name: capacity-overprovisioning
value: -10
globalDefault: false
description: "Отрицательный приоритет для capacity-overprovisioning подов резервирования мощностей"
```
### Шаг 2. Разверните Deployment с capacity-overprovisioning подами

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
      priorityClassName: capacity-overprovisioning
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
            cpu: 1
            memory: 1Gi
          limits:
            cpu: 1
            memory: 1Gi
```
Ключевые детали:

- **`value: -10`.** Отрицательный приоритет гарантирует, что capacity-overprovisioning под будет первой жертвой вытеснения: поды без `priorityClassName` получают приоритет **0**, то есть уже выше capacity-overprovisioning подов. Этого достаточно, чтобы обычные поды вытесняли capacity-overprovisioning поды — задавать глобальный default PriorityClass для всего кластера не нужно. Значение `-10` выбрано не случайно, а ровно на границе cutoff Cluster Autoscaler (`--expendable-pods-priority-cutoff`, по умолчанию `-10`): чтобы одновременно и быть вытесняемым, и продолжать вызывать scale-up, когда capacity-overprovisioning под уходит в Pending. Подробнее про порог ниже в разделе «Кому можно выставлять PriorityClass ниже -10».
- **`requests` — это и есть размер резерва.** Контейнер `pause` потребляет копейки; capacity-overprovisioning под «занимает» ровно столько, сколько заявлено в `resources.requests`. Общий буфер = `replicas × requests`. Готовый манифест для демо-стенда — [manifests/overprovisioning.yaml](manifests/overprovisioning.yaml).
- **`terminationGracePeriodSeconds: 0`.** Стандартные 30 секунд graceful shutdown — это 30 секунд задержки перед тем, как место освободится. Capacity-overprovisioning поду нечего «корректно завершать», поэтому выключаем ожидание.
- **Pod anti-affinity** (`preferredDuringSchedulingIgnoredDuringExecution` по hostname) — «мягкое» правило, старающееся распределить capacity-overprovisioning поды по разным нодам. Без него весь буфер может осесть на одной ноде — и при её сбое вы теряете весь резерв разом.

### requests запускает масштабирование, priority разрешает вытеснение

Здесь легко запутаться, поэтому разделим роли явно:

- **`resources.requests` capacity-overprovisioning пода — триггер scale-up.** Приоритет сам по себе не заставляет Cluster Autoscaler ничего делать: автоскейлер реагирует только на поды в состоянии `Unschedulable` из-за нехватки ресурсов. Capacity-overprovisioning под с requests 1 CPU / 1Gi, который не помещается, — это и есть сигнал «нужна нода».
- **Отрицательный `priority` — включатель мгновенного вытеснения.** Он гарантирует, что занятый capacity-overprovisioning подом объём будет отдан новому поду немедленно, без ожидания.

### Экономия ночью и утром

Ключевая выгода паттерна — экономия. Ночью и утром, когда нагрузка падает, KEDA снижает число реплик бизнес-приложения, Cluster Autoscaler удаляет недозагруженные ноды, и кластер схлопывается до минимального числа нод. Днём при всплеске capacity-overprovisioning поды отдают место мгновенно — скорость реакции кластера остаётся прежней: секунды вместо минут провижининга новой ноды облаком.

## Демо: вытеснение в живом кластере

Теперь самое интересное — развернём кластер и спровоцируем вытеснение. Предполагается, что k8s кластер c динамическими/autoscale нодами уже развёрнут.

### Шаг 0. Проверяем стенд

Сначала в k8s 1 нода.

```bash
$ kubectl get nodes
NAME                       STATUS   ROLES    AGE   VERSION
cl1v2fmpkgn4srb2b1mm-uxyz   Ready    <none>   3m    v1.33.x
```

```bash
$ kubectl get priorityclasses.scheduling.k8s.io
NAME                      VALUE        GLOBAL-DEFAULT   AGE
system-cluster-critical   2000000000   false            10m
system-node-critical      2000001000   false            10m
```

```
$ helm upgrade --install vmks \
    oci://ghcr.io/victoriametrics/helm-charts/victoria-metrics-k8s-stack \
    --namespace vmks --create-namespace \
    --wait --version 0.90.2 --timeout 15m \
    -f vmks-values.yaml
```


# vmsingle отвечает на запросы — KEDA будет брать метрику RPS отсюда
```
$ kubectl get pods -n vmks
NAME                                          READY   STATUS    RESTARTS   AGE
vmsingle-vmks-victoria-metrics-k8s-stack-6b9755569b-7r22n    1/1     Running   0          5m
vmagent-vmks-victoria-metrics-k8s-stack-5d488f8c89-kjjkn     2/2     Running   0          5m
...
```

### Шаг 1. Применяем PriorityClass
```bash
kubectl apply -f priorityclasses.yaml
```


```bash
$ kubectl get priorityclasses.scheduling.k8s.io
NAME                       VALUE        GLOBAL-DEFAULT   AGE
capacity-overprovisioning  -10          false            5s    ← для capacity-overprovisioning подов
system-cluster-critical    2000000000   false            10m
system-node-critical       2000001000   false            10m
```

Проверяем, что приоритет применился к обычному поду (контейнер `pause` — минимальный образ, который ничего не делает):
```bash
$ kubectl run test-pod --image=registry.k8s.io/pause:3.10 --restart=Never
$ kubectl get pod test-pod -o jsonpath='{.spec.priority} {"\n"}'
0
$ kubectl delete pod test-pod
```
Приоритет 0 выше -10 у capacity-overprovisioning подов — этого достаточно для вытеснения.

### Шаг 2. Запускаем capacity-overprovisioning поды

```bash
kubectl apply -f manifests/overprovisioning.yaml
```
Две реплики с requests 1 CPU / 1Gi каждая не помещаются на стартовую ноду (600m CPU и ~1 ГБ уже уходят на системные поды и резервы kubelet) — Cluster Autoscaler разворачивает под них вторую ноду:

```bash
$ kubectl get pods -o wide
NAME                              READY   STATUS    NODE
overprovisioning-...-a1b2c        1/1     Running   cl1...-uabc   ← «тёплая» нода
overprovisioning-...-d3e4f        0/1     Pending                 ← второй capacity-overprovisioning под ждёт

$ kubectl get nodes
NAME                       STATUS   ROLES    AGE
cl1v2fmpkgn4srb2b1mm-uxyz   Ready    <none>   18m
cl1v2fmpkgn4srb2b1mm-uabc   Ready    <none>   2m    ← новая нода под capacity-overprovisioning под
```

Анти-аффинность в манифесте распределяет capacity-overprovisioning поды по разным нодам, поэтому один занял новую ноду, а второй остался в Pending — он и удерживает Autoscaler от scale-down обратно к одной ноде.

### Шаг 3. Устанавливаем KEDA

KEDA добавляет в кластер горизонтальный автоскейлинг приложений по внешним метрикам — в нашем случае по RPS из Prometheus-совместимого API vmsingle:

```bash
helm repo add kedacore https://kedacore.github.io/charts
helm install keda kedacore/keda --namespace keda --create-namespace --version 2.20.2
```

### Шаг 4. Развёртываем бизнес-приложение, KEDA-триггер и генератор нагрузки

```bash
kubectl apply -f manifests/keda/business-app.yaml
kubectl apply -f manifests/keda/scaledobject.yaml
kubectl apply -f manifests/keda/load-generator.yaml
kubectl apply -f manifests/keda/vmservicescrape.yaml
```

- `business-app` — Go-приложение с HTTP API `/` и метриками Prometheus на `/metrics`. Каждая реплика запрашивает 1 CPU / 1Gi — ровно как capacity-overprovisioning под, поэтому при scale-out новая реплика (приоритет 0) гарантированно вытесняет его (приоритет -10).
- `scaledobject.yaml` — триггер KEDA: Prometheus scaler берёт из vmsingle метрику `sum(rate(business_app_http_requests_total{route="root"}[1m]))` и держит ~25 RPS на реплику (min 1 / max 4).
- `load-generator` — гоняет на business-app лестницу RPS «день/ночь»: ночь 0 RPS (2м) → 5 RPS (3м) → 20 RPS (3м) → 60 RPS (3м) → пик 100 RPS (5м) → спад 20 RPS (3м) → ночь 0 RPS (2м), затем повтор бесконечно.

### Шаг 5. Наблюдаем полный цикл: рост → вытеснение → восстановление буфера

Следим за репликами business-app и HPA, который создал KEDA:

```bash
$ kubectl get deployment business-app -w
$ kubectl get hpa keda-hpa-business-app -w
```

Когда лестница RPS поднимается выше 25 RPS, KEDA добавляет реплику. Новая реплика (requests 1 CPU/1Gi, приоритет 0) не помещается на занятые ноды — планировщик вытесняет capacity-overprovisioning под (приоритет -10). Благодаря `terminationGracePeriodSeconds: 0` место освобождается сразу:

```bash
$ kubectl get events --sort-by=.lastTimestamp | grep -E 'Preempted|Scaled'
LAST SEEN   TYPE      REASON      OBJECT                          MESSAGE
20s         Normal    Preempted   pod/overprovisioning-...-a1b2c  By default/business-app-...-7g8h9 on node cl1...-uabc
35s         Normal    Killing     pod/overprovisioning-...-a1b2c  Stopping container pause
```

Ключевое событие — `Preempted ... By default/business-app-...`: планировщик явно показывает, кто кого вытеснил.

Вытесненный capacity-overprovisioning под уходит в Pending — это сигнал для Cluster Autoscaler развернуть новую ноду (провижининг ноды в облаке — минуты, в рамках `max = 3`):
```bash
$ kubectl get nodes -w
NAME                       STATUS   ROLES    AGE
cl1v2fmpkgn4srb2b1mm-uxyz   Ready    <none>   25m
cl1v2fmpkgn4srb2b1mm-uabc   Ready    <none>   9m
cl1v2fmpkgn4srb2b1mm-wxyz   Ready    <none>   47s   ← новая нода под буфер
```

На новой ноде capacity-overprovisioning поды снова становятся Running — «тёплый» резерв восстановлен:
```bash
$ kubectl get pods -o wide
NAME                              READY   STATUS    NODE
business-app-...-7g8h9            1/1     Running   cl1...-uabc
overprovisioning-...-a1b2c        1/1     Running   cl1...-wxyz   ← буфер снова готов
overprovisioning-...-d3e4f        1/1     Running   cl1...-wxyz
```

Полный цикл замкнулся: **рост RPS → KEDA поднимает реплики → мгновенное вытеснение → реальный под работает → новая нода → буфер восстановлен**.

### Шаг 6. Спад нагрузки и scale-down

Когда лестница RPS опускается к 0 (ночь), KEDA — после `cooldownPeriod: 120` — возвращает реплики business-app к 1. Через несколько минут Cluster Autoscaler поймёт, что лишние ноды недозагружены, и удалит их:

```bash
$ kubectl get nodes -w
# ... спустя ~10 минут после спада нагрузки
NAME                       STATUS                     ROLES    AGE
cl1v2fmpkgn4srb2b1mm-uxyz   Ready                      <none>   35m
cl1v2fmpkgn4srb2b1mm-uabc   Ready,SchedulingDisabled   <none>   17m   ← cordoned
# затем нода удаляется
```

> Cluster Autoscaler удаляет ноду, только если все её поды можно перенести на другие ноды. Не забудьте, что scale-down требует, чтобы суммарные requests помещались на оставшиеся ноды.

## Важные нюансы для корректной работы

**Настройка Cluster Autoscaler.** Убедитесь, что у вас включён автоскейлинг нод. Когда capacity-overprovisioning поды уйдут в Pending, Autoscaler увидит нехватку ресурсов для подов с приоритетом -10 и начнёт создавать новую ноду — восстанавливать буфер. В Yandex Managed K8s это делается через `auto_scale` в node group; в self-hosted — установкой Cluster Autoscaler с явным указанием минимального и максимального размера групп нод.

**Запросы ресурсов (Requests).** Вытеснение сработает только в том случае, если у ваших подов чётко прописаны `resources.requests`. Kubernetes сравнивает приоритеты только тогда, когда физически не может разместить под из-за нехватки запрошенных ресурсов. Под без requests для планировщика «весит ноль» — он не вытеснит никого и сам не станет причиной масштабирования. Это самая частая причина, почему приоритеты «не работают». Полезно запомнить разделение ролей: **requests — триггер масштабирования, priority — право вытеснять**. Приоритет сам по себе не заставляет Cluster Autoscaler создавать ноды — тот реагирует только на поды в состоянии `Unschedulable` из-за нехватки ресурсов.

**Поды без requests и limits (BestEffort).** Если в namespace нет LimitRange, под без requests и limits получает QoS-класс `BestEffort` — и вся приоритетная механика для него переворачивается:

- **Для планировщика он весит ноль.** Он разместится на любую ноду, даже полностью «занятую» по requests; по нехватке ресурсов он никогда не бывает Unschedulable, а значит — никогда не подаст Cluster Autoscaler сигнал «нужна нода».
- **Его нельзя вытеснить ради освобождения места.** Удаление пода с requests = 0 освобождает для планировщика ровно ноль, поэтому scheduler preemption не выбирает его жертвой: реально занятые им гигабайты памяти «непробиваемы» для приоритетного вытеснения.
- **Зато он первый кандидат на node-pressure eviction и OOM-killer.** Kubelet при нехватке памяти ранжирует поды сначала по превышению requests и только потом по приоритету — а BestEffort «превышает» всегда (requests = 0). Плюс kubelet выставляет BestEffort-подам максимальный `oom_score_adj = 1000`, поэтому kernel OOM-killer бьёт по ним первыми — независимо от назначенного PriorityClass.

Парадокс: под без requests одновременно «неуязвим» для вытеснения планировщиком и «самый уязвимый» для eviction kubelet'ом — мониторинг без requests умрёт первым именно в момент инцидента, когда он нужнее всего. Вывод: для схемы «приоритеты + overprovisioning» requests должны стоять у всех участников — и у вытесняющих, и у жертв. Если LimitRange не используется, контролируйте это на уровне манифестов и values (например, проверяйте в CI, что у каждого контейнера указаны requests, — включая компоненты мониторинга вроде vmagent, vmsingle, alertmanager).

**Диапазон значений и имена PriorityClass.** Значение `value` — 32-битное целое от -2147483648 до 1000000000; всё, что выше миллиарда, зарезервировано за встроенными системными классами (`system-cluster-critical` = 2000000000, `system-node-critical` = 2000001000). Имя класса должно быть валидным DNS-именем и не может начинаться с префикса `system-`. Отрицательные значения — не хак, а штатный механизм, официально используемый для capacity-overprovisioning подов.

### Кому можно выставлять PriorityClass ниже -10

Ключевой порог здесь — флаг Cluster Autoscaler `--expendable-pods-priority-cutoff` (по умолчанию `-10`). Cluster Autoscaler рассматривает под в Pending как повод развернуть новую ноду, только если его приоритет **выше или равен** этому порогу. Поды с приоритетом **ниже** cutoff считаются «расходными» (expendable) и scale-up не триггерят — то есть **если у пода PriorityClass меньше `-10`, Cluster Autoscaler не станет создавать под него новую ноду**: под честно ждёт, пока свободное место появится само (после спада нагрузки или после того, как нода добавится по другому поводу).

Поэтому `value` меньше `-10` — не ошибка сама по себе, а осознанный выбор для тех подов, которым **запрещено** тратить деньги кластера на новую ноду:

- **Низкоприоритетные batch/фоновые задачи**, которым не важно, когда они отработают (nightly-джобы, отчёты, обработка очередей). Они должны ждать свободного места, а не поднимать кластер.
- **Внутренние утилиты и «мусорные» поды**, которые не должны влиять на размер кластера вообще.
- **Поды в namespace, где масштабирование нежелательно** — например, staging/dev-нагрузка, которую держат на том, что осталось.

Правило простое: **буфер (capacity-overprovisioning) ставим ровно на cutoff (`-10`), а всё, что не должно провоцировать scale-up, — строго ниже**. Если опустить capacity-overprovisioning поды ниже `-10` (как часто советуют, задавая `-1000`), то при вытеснении ушедший в Pending под не вызовет создание ноды — и «тёплый» резерв не восстановится. Именно поэтому в этом демо используется `-10`, а не `-1000`.

**Защита от циклического перезапуска (Flapping).** Если новая нода создаётся слишком долго, capacity-overprovisioning поды будут находиться в Pending. Как только нода поднимется, они запустятся там и восстановят буфер. Убедитесь, что лимиты автоскейлера позволяют расширять кластер (`max` в auto_scale не должен упираться в квоту облака), и что развертывание подов не блокируется чем-то ещё — например, лимитами namespace (ResourceQuota) или отсутствием доступа к реестру образов.

**Политика вытеснения.** По умолчанию свойство `preemptionPolicy` в PriorityClass имеет значение `PreemptLowerPriority`. Это именно то, что вам нужно (высокий вытесняет низкий, включая capacity-overprovisioning поды с отрицательным приоритетом). Альтернативное значение `Never` означает, что под с таким классом не будет вытеснять других — он честно ждёт в Pending, но для нашего паттерна нужен именно дефолт.

**Graceful shutdown.** Вытеснение — это обычное удаление пода с соблюдением `terminationGracePeriodSeconds` и preStop-хуков. Значение по умолчанию — 30 секунд, и всё это время место на ноде считается занятым. Для capacity-overprovisioning подов graceful shutdown не нужен вовсе — там ставят `terminationGracePeriodSeconds: 0`, чтобы резерв освобождался мгновенно. А вот для подов бизнес-приложений, которые вытесняют capacity-overprovisioning поды, важно, чтобы они корректно обрабатывали SIGTERM — иначе мгновенное вытеснение capacity-overprovisioning пода не гарантирует мгновенное освобождение места: реальная задержка будет ровно по `terminationGracePeriodSeconds` вытесняющего пода.

**PodDisruptionBudget — best effort.** Планировщик старается не нарушать PDB при выборе жертв, но если жертв без нарушения PDB нет — вытеснение всё равно произойдёт. Не рассчитывайте на PDB как на защиту от приоритетного вытеснения: он ограничивает добровольные disruptions (drain, вытеснения при обновлениях), а не scheduler preemption.

**Inter-pod affinity — только к равным или более высоким приоритетам.** Нода рассматривается как кандидат на вытеснение, только если удаление с неё всех низкоприоритетных подов позволило бы разместить новичка.

**Мульти-тенантность: приоритеты как объект атаки.** В кластере, где поды создают не только доверенные команды, пользователь может создать под с максимально высоким приоритетом и начать вытеснять чужие нагрузки, или, наоборот, «похитить» буфер из capacity-overprovisioning подов. Защита — ResourceQuota с ограничением потребления PriorityClass ([scopeSelector по priorityClassName](https://kubernetes.io/docs/concepts/policy/resource-quotas/)): например, квотой, разрешающей использовать `capacity-overprovisioning` только в определённых namespace.

**Не путайте с eviction под давлением.** PriorityClass также влияет на порядок, в котором kubelet выселяет поды при нехватке памяти на ноде (node-pressure eviction) — но это другой механизм с другими причинами. Причём kubelet ранжирует поды иначе: сначала — превышение requests по дефицитному ресурсу, только потом — приоритет. QoS-класс пода в вытеснении планировщиком вообще не участвует. В этой статье речь о вытеснении планировщиком (scheduler preemption), которое происходит из-за нехватки места именно для нового пода.

## Когда этот паттерн уместен

Overprovisioning через capacity-overprovisioning поды — это про покупку скорости за деньги: он окупается при непредсказуемых всплесках с жёсткими SLA (flash sales, продакшен с дорогим простоем), и избыточен при предсказуемой нагрузке, где обычного реактивного автоскейлинга достаточно.

Паттерн особенно хорош для критичных сервисов, которые **должны разворачиваться немедленно**: web-frontend при flash-трафике, API-шлюзы, сервисы с SLO на время ответа, а также batch/ML-задачи, если вы им дали высокий приоритет и хотите, чтобы они стартовали сразу — а не ждали создания ноды.

Обратная сторона: если буфер из capacity-overprovisioning подов занимает ноды, а автоскейлер по какой-то причине не может расширить кластер (квота, лимит max, недоступность зон), вытесненные capacity-overprovisioning поды будут ждать в Pending неограниченно долго, и буфер не восстановится. Приоритеты — не замена мониторингу: алерт на долго живущие Pending-поды и на не расширяющиеся node groups должен быть в любом случае.

## Итоги

Мы развернули в Yandex Managed Kubernetes кластер с автоскейлингом, настроили отрицательный PriorityClass для capacity-overprovisioning подов — и пронаблюдали полный цикл на живом бизнес-приложении: KEDA по RPS поднял реплики business-app, новые реплики мгновенно вытеснили capacity-overprovisioning поды, буфер ушёл в Pending, Cluster Autoscaler добавил ноду, и резерв восстановился. Паттерн node overprovisioning даёт «тёплый» резерв: место под будущий пик держится заранее, а при всплеске освобождается за секунды (вместо минут ожидания, пока облако подготовит новую ноду).

Все конфигурации из статьи:

- [INFRASTRUCTURE.md](INFRASTRUCTURE.md) — Terraform для кластера с `auto_scale` node group, Traefik и VictoriaMetrics K8s Stack
- [priorityclasses.yaml](priorityclasses.yaml) — PriorityClass для capacity-overprovisioning подов с отрицательным значением
- [manifests/overprovisioning.yaml](manifests/overprovisioning.yaml) — capacity-overprovisioning поды для «тёплого» резерва
- [manifests/keda/business-app.yaml](manifests/keda/business-app.yaml) — бизнес-приложение (Deployment + Service)
- [manifests/keda/scaledobject.yaml](manifests/keda/scaledobject.yaml) — KEDA ScaledObject, масштабирование по RPS из VictoriaMetrics
- [manifests/keda/load-generator.yaml](manifests/keda/load-generator.yaml) — генератор нагрузки с профилем RPS «день/ночь»
- [apps/business-app/](apps/business-app/) — исходники бизнес-приложения (Go)
- [apps/load-generator/](apps/load-generator/) — исходники генератора нагрузки (Go)

Главное, что стоит запомнить: приоритеты работают только при корректных `resources.requests`; **requests — триггер масштабирования, priority — право вытеснять**; поды без `priorityClassName` получают приоритет 0 и уже вытесняют capacity-overprovisioning поды с отрицательным значением; `auto_scale` в Yandex Managed K8s — это Cluster Autoscaler из коробки; а если скорость реакции важнее цены — capacity-overprovisioning поды с отрицательным приоритетом дают «тёплый» резерв мощностей.
