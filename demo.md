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
NAME                      VALUE        GLOBAL-DEFAULT   AGE
high-priority-default     1000000      true             5s    ← дефолтный
low-priority-specialized  1000         false            5s    ← для специализированных
system-cluster-critical   2000000000   false            10m
system-node-critical      2000001000   false            10m
```

Проверяем, что приоритет применился к обычному поду:

```bash
$ kubectl run test-pod --image=busybox:1.37 --restart=Never -- sleep 60
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
2m          Normal   Killing     pod/specialized-worker-...xk2p9   Stopping container worker
2m          Normal   Killing     pod/specialized-worker-...xk2p9   Container worker terminated successfully
```

Ключевое событие — `Preempted ... By default/critical-app-...`: планировщик явно
показывает, кто кого вытеснил.

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

## Очистка

```bash
kubectl delete -f manifests/critical-app.yaml
kubectl delete -f manifests/specialized-worker.yaml
kubectl delete -f priorityclasses.yaml
terraform destroy
```
