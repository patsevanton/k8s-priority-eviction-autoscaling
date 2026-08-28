# Демо: приоритетное вытеснение и автоскейлинг в живом кластере

Полный сценарий демо из [статьи](README.md). Предполагается, что кластер уже развёрнут
через Terraform ([INFRASTRUCTURE.md](INFRASTRUCTURE.md)) и kubeconfig получен.

Стенд: одна нода 2 vCPU / 4 ГБ, node group `auto_scale { min = 1, max = 3 }`.

## Шаг 0. Проверяем стенд

```bash
$ kubectl get nodes
NAME                       STATUS   ROLES    AGE   VERSION
cl1v2fmpkgn4srb2b1mm-uxyz   Ready    <none>   3m    v1.33.x

$ kubectl get priorityclasses.scheduling.k8s.io
NAME                      VALUE        GLOBAL-DEFAULT   AGE
system-cluster-critical   2000000000   false            10m
system-node-critical      2000001000   false            10m
```

## Шаг 1. Применяем PriorityClass

```bash
kubectl apply -f priorityclasses.yaml
```

```bash
$ kubectl get priorityclasses.scheduling.k8s.io
NAME                        VALUE        GLOBAL-DEFAULT   AGE
high-priority-default       1000000      true             5s    ← дефолтный
low-priority-specialized    1000         false            5s    ← для специализированных
overprovisioning-placeholder -1000       false            5s    ← для pause-заглушек
system-cluster-critical     2000000000   false            10m
system-node-critical        2000001000   false            10m
```

Проверяем, что приоритет применился к обычному поду (pause-контейнер — минимальный
образ, который ничего не делает):

```bash
$ kubectl run test-pod --image=registry.k8s.io/pause:3.10 --restart=Never
$ kubectl get pod test-pod -o jsonpath='{.spec.priority} {"\n"}'
1000000
```

## Шаг 2. Заполняем ноду низкоприоритетными подами

```bash
kubectl apply -f manifests/specialized-worker.yaml
```

Две реплики с requests 900m CPU / 1.5Gi каждая — нода 2 vCPU / 4 ГБ почти заполнена
(600m CPU и ~1 ГБ уходят на системные поды и резервы kubelet).

```bash
$ kubectl get pods -o wide
NAME                                  READY   STATUS    NODE
specialized-worker-7d9f8b6c4-xk2p9   1/1     Running   cl1...-uxyz
specialized-worker-7d9f8b6c4-m4n8q   1/1     Running   cl1...-uxyz

$ kubectl describe node cl1v2fmpkgn4srb2b1mm-uxyz | grep -A5 "Allocated resources"
Allocated resources:
  (Total limits may be over 100 percent, etc., 2 entries may be displayed.)
  Resource           Requests      Limits
  cpu                1930m (96%)   2100m (105%)
  memory             3648Mi (95%)  4340Mi (113%)
```

## Шаг 3. Запускаем критичное приложение

```bash
kubectl apply -f manifests/critical-app.yaml
```

critical-app (requests 900m / 1.5Gi, приоритет 1000000 через globalDefault)
не помещается на ноду. Планировщик вытесняет specialized-worker:

```bash
$ kubectl get events --sort-by=.lastTimestamp | tail -8
LAST SEEN   TYPE      REASON      OBJECT                            MESSAGE
2m          Normal    Scheduled   pod/critical-app-5c8d7f-9xz4l     Successfully assigned default/critical-app-5c8d7f-9xz4l to cl1...-uxyz
2m          Normal   Preempted   pod/specialized-worker-...xk2p9   By default/critical-app-5c8d7f-9xz4l on node cl1...-uxyz
2m          Normal   Killing     pod/specialized-worker-...xk2p9   Stopping container pause
2m          Normal   Killing     pod/specialized-worker-...xk2p9   Container pause terminated successfully
```

Ключевое событие — `Preempted ... By default/critical-app-...`: планировщик явно
показывает, кто кого вытеснил.

Полезно заглянуть и в статус вытесняющего пода: пока жертвы завершаются,
планировщик «номинирует» для него ноду в поле `nominatedNodeName`:

```bash
$ kubectl get pod critical-app-5c8d7f-9xz4l -o jsonpath='{.status.nominatedNodeName} {"\n"}'
cl1v2fmpkgn4srb2b1mm-uxyz
```

Номинация — не гарантия: если за время graceful shutdown жертвы освободится
другая нода (или придёт ещё более приоритетный под и займёт место), фактическая
нода размещения может отличаться, а `nominatedNodeName` будет очищен.

Состояние подов: critical-app работает на ноде, вытесненный воркер — в Pending:

```bash
$ kubectl get pods
NAME                                  READY   STATUS    RESTARTS   AGE
critical-app-5c8d7f-9xz4l             1/1     Running   0          2m
specialized-worker-7d9f8b6c4-m4n8q    1/1     Running   0          8m
specialized-worker-7d9f8b6c4-xk2p9    0/1     Pending   0          8m

$ kubectl describe pod specialized-worker-7d9f8b6c4-xk2p9 | tail -4
Events:
  Type     Reason            Age                 From            Message
  ----     ------            ----                ----            -------
  Warning  FailedScheduling  20s (x2 over 90s)   default-scheduler  0/1 nodes are available: 1 Insufficient cpu. preemption: 0/1 nodes are available: 1 No preemption victims found for incoming pod.
```

`No preemption victims found` — вытеснять больше некого (critical-app и второй
воркер вместе занимают ноду, а приоритет второго воркера не ниже — он такой же).

## Шаг 4. Cluster Autoscaler добавляет ноду

Через 2–3 минуты после появления Pending-пода Cluster Autoscaler разворачивает
новую ноду:

```bash
$ kubectl get nodes -w
NAME                       STATUS   ROLES    AGE
cl1v2fmpkgn4srb2b1mm-uxyz   Ready    <none>   18m
cl1v2fmpkgn4srb2b1mm-uabc   Ready    <none>   47s    ← новая нода
```

Вытесненный воркер запускается на новой ноде:

```bash
$ kubectl get pods -o wide
NAME                                  READY   STATUS    NODE
critical-app-5c8d7f-9xz4l             1/1     Running   cl1...-uxyz
specialized-worker-7d9f8b6c4-m4n8q    1/1     Running   cl1...-uxyz
specialized-worker-7d9f8b6c4-xk2p9    1/1     Running   cl1...-uabc   ← перезапущен
```

## Шаг 5. Проверяем восстановление равновесия

Удаляем critical-app — через несколько минут Cluster Autoscaler поймёт, что
вторая нода недозагружена, и удалит её (scale-down):

```bash
kubectl delete -f manifests/critical-app.yaml

$ kubectl get nodes -w
# ... спустя ~10 минут
NAME                       STATUS                     ROLES    AGE
cl1v2fmpkgn4srb2b1mm-uxyz   Ready                      <none>   35m
cl1v2fmpkgn4srb2b1mm-uabc   Ready,SchedulingDisabled   <none>   17m   ← cordoned
# затем нода удаляется, воркер переезжает на первую ноду
```

> Cluster Autoscaler удаляет ноду, только если все её поды можно перенести
> на другие ноды. Не забудьте, что scale-down требует, чтобы суммарные requests
> помещались на оставшиеся ноды — поэтому после удаления critical-app
> оба воркера вернутся на первую ноду.

## Шаг 6 (опционально). «Тёплый» резерв через placeholder-поды

> Шаг 6 — расширение сценария: сначала прогоните шаги 0–5, затем вернитесь к шагу 2
> и выполните шаги 2–4 ещё раз (нода снова должна быть заполнена двумя воркерами
> и critical-app).

Вариация того же механизма — [overprovisioning](https://kubernetes.io/docs/tasks/administer-cluster/node-overprovisioning/):
в кластер запускаются pause-поды с отрицательным приоритетом, которые ничего
не делают, а только «держат» место. Смысл смотреть на живом кластере:
нода под будущий пик разворачивается **заранее**, ещё до прихода нагрузки.

```bash
kubectl apply -f manifests/overprovisioning.yaml
```

Placeholder-поды (2 реплики по 900m CPU / 1.5Gi) не помещаются на заполненную
ноду — Cluster Autoscaler разворачивает под них ещё одну (max = 3 в node group,
так что для демо этого хватает):

```bash
$ kubectl get pods -o wide
NAME                              READY   STATUS    NODE
specialized-worker-...-xk2p9      1/1     Running   cl1...-uxyz
specialized-worker-...-m4n8q      1/1     Running   cl1...-uxyz
critical-app-5c8d7f-9xz4l         1/1     Running   cl1...-uxyz
overprovisioning-...-a1b2c        1/1     Running   cl1...-uabc   ← «тёплая» нода
overprovisioning-...-d3e4f        0/1     Pending                   ← вторая заглушка ждёт
```

Анти-аффинность в манифесте распределяет заглушки по разным нодам, поэтому
одна заняла новую ноду, а вторая осталась в Pending — она и удерживает
Autoscaler от scale-down обратно к одной ноде.

Теперь проверяем суть паттерна: создаём ещё один критичный под — он вытесняет
placeholder мгновенно (у того и `terminationGracePeriodSeconds: 0`), не дожидаясь
создания ноды:

```bash
$ kubectl scale deployment critical-app --replicas=2

$ kubectl get events --sort-by=.lastTimestamp | tail -4
LAST SEEN   TYPE      REASON      OBJECT                          MESSAGE
20s         Normal    Scheduled   pod/critical-app-...-7g8h9      Successfully assigned default/critical-app-...-7g8h9 to cl1...-uabc
35s         Normal    Preempted   pod/overprovisioning-...-a1b2c  By default/critical-app-...-7g8h9 on node cl1...-uabc
35s         Normal    Killing     pod/overprovisioning-...-a1b2c  Stopping container pause
```

Критичный под запустился на «тёплой» ноде за секунды — вместо 2–3 минут
ожидания новой ноды. Вытесненная заглушка ушла в Pending, и кластер начал
восстанавливать буфер: Autoscaler увидит её и при необходимости добавит ноду.

Когда резерв не нужен — убираем:

```bash
kubectl delete -f manifests/overprovisioning.yaml
```

После удаления placeholder-подов Cluster Autoscaler через несколько минут
замечает недозагрузку и удаляет «тёплую» ноду (scale-down, как на шаге 5).

## Что пошло не так? (Траблшутинг)

**Вытеснение не происходит, все поды Pending.** Проверьте requests у обоих
Deployment: без `resources.requests` планировщик считает под «бесплатным» и не
вытесняет никого.

```bash
kubectl describe pod <pod> | grep -A3 "Requests"
```

**Нода не создаётся.** Проверьте, что node group действительно с auto_scale,
и посмотрите события:

```bash
yc managed-kubernetes node-group list
yc managed-kubernetes node-group get <node-group-id> --show-log
```

**Под в Pending навсегда.** Cluster Autoscaler не может расширить группу сверх
`max` (в нашем демо — 3) или упёрся в квоту каталога. Проверьте лимиты:

```bash
yc resource-manager folder list-limits --name <folder>
```

**Жертвы вытеснены, но вытесняющий под так и не запустился.** Жертвы получают
graceful termination period (30 секунд по умолчанию) — всё это время место
считается занятым, и только после ухода жертв preemptor может быть запланирован.
Кроме того, пока жертвы завершаются, может прийти под с ещё более высоким
приоритетом и занять место — это ожидаемое поведение Kubernetes, а не баг.
Проверить, что место действительно зарезервировано за подом, можно полем
`status.nominatedNodeName`:

```bash
kubectl get pod <pod> -o jsonpath='{.status.nominatedNodeName} {"\n"}'
```

## Очистка

```bash
kubectl delete -f manifests/critical-app.yaml
kubectl delete -f manifests/specialized-worker.yaml
kubectl delete -f manifests/overprovisioning.yaml
kubectl delete -f priorityclasses.yaml
terraform destroy
```
