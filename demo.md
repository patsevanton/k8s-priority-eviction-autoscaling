# Демо: «тёплый» резерв через placeholder-поды и автоскейлинг в живом кластере

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
overprovisioning-placeholder -1000       false            5s    ← для pause-заглушек
system-cluster-critical     2000000000   false            10m
system-node-critical        2000001000   false            10m
```

Проверяем, что приоритет применился к обычному поду (pause-контейнер — минимальный
образ, который ничего не делает):

```bash
$ kubectl run test-pod --image=registry.k8s.io/pause:3.10 --restart=Never
$ kubectl get pod test-pod -o jsonpath='{.spec.priority} {"\n"}'
0
$ kubectl delete pod test-pod
```

Приоритет 0 выше -1000 у placeholder'ов — этого достаточно для вытеснения.

## Шаг 2. Запускаем placeholder-поды

```bash
kubectl apply -f manifests/overprovisioning.yaml
```

Две реплики с requests 900m CPU / 1Gi каждая не помещаются на стартовую ноду
(600m CPU и ~1 ГБ уже уходят на системные поды и резервы kubelet) — Cluster
Autoscaler разворачивает под них вторую ноду:

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

Анти-аффинность в манифесте распределяет заглушки по разным нодам, поэтому одна
заняла новую ноду, а вторая осталась в Pending — она и удерживает Autoscaler от
scale-down обратно к одной ноде.

## Шаг 3. Запускаем критичное приложение — имитация пика нагрузки

В реальной жизни этот момент выглядит так: нагрузка на бизнес-сервис растёт, HPA
поднимает `replicas`, и новые поды должны стартовать немедленно. В демо мы
имитируем пик одним `kubectl apply` — critical-app играет роль «реплики, которую
HPA только что создал».

```bash
kubectl apply -f manifests/critical-app.yaml
```

critical-app (requests 900m / 1Gi, приоритет 0)
находит ноду с placeholder'ом (приоритет -1000) и вытесняет его. Благодаря
`terminationGracePeriodSeconds: 0` место освобождается сразу — критичный под
стартует за секунды, не дожидаясь создания новой ноды:

```bash
$ kubectl get events --sort-by=.lastTimestamp | tail -4
LAST SEEN   TYPE      REASON      OBJECT                          MESSAGE
20s         Normal    Scheduled   pod/critical-app-...-7g8h9      Successfully assigned default/critical-app-...-7g8h9 to cl1...-uabc
35s         Normal    Preempted   pod/overprovisioning-...-a1b2c  By default/critical-app-...-7g8h9 on node cl1...-uabc
35s         Normal    Killing     pod/overprovisioning-...-a1b2c  Stopping container pause
```

Ключевое событие — `Preempted ... By default/critical-app-...`: планировщик явно
показывает, кто кого вытеснил.

Состояние подов: critical-app работает на «тёплой» ноде, вытесненная заглушка —
в Pending:

```bash
$ kubectl get pods -o wide
NAME                              READY   STATUS    NODE
critical-app-...-7g8h9            1/1     Running   cl1...-uabc
overprovisioning-...-a1b2c        0/1     Pending                 ← вытеснена
overprovisioning-...-d3e4f        0/1     Pending
```

Полезно заглянуть и в статус вытесняющего пода: пока жертва завершается,
планировщик «номинирует» для него ноду в поле `nominatedNodeName`:

```bash
$ kubectl get pod critical-app-...-7g8h9 -o jsonpath='{.status.nominatedNodeName} {"\n"}'
cl1v2fmpkgn4srb2b1mm-uabc
```

Номинация — не гарантия: если за время graceful shutdown жертвы освободится
другая нода (или придёт ещё более приоритетный под и займёт место), фактическая
нода размещения может отличаться, а `nominatedNodeName` будет очищен. Для
placeholder-подов с `terminationGracePeriodSeconds: 0` задержка нулевая.

## Шаг 4. Cluster Autoscaler восстанавливает буфер

Теперь в кластере два Pending placeholder-пода — они не помещаются на
оставшиеся ноды. Через до 10 минут Cluster Autoscaler разворачивает третью ноду
(в рамках лимита `max = 3`):

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

Полный цикл замкнулся: **пик → мгновенное вытеснение → реальный под работает →
новая нода → буфер восстановлен**.

## Шаг 5. Проверяем scale-down

Удаляем critical-app и placeholder-поды — через несколько минут Cluster
Autoscaler поймёт, что лишние ноды недозагружены, и удалит их:

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

> Cluster Autoscaler удаляет ноду, только если все её поды можно перенести
> на другие ноды. Не забудьте, что scale-down требует, чтобы суммарные requests
> помещались на оставшиеся ноды.

## Что пошло не так? (Траблшутинг)

**Вытеснение не происходит, критичный под в Pending.** Проверьте requests у
Deployment: без `resources.requests` планировщик считает под «бесплатным» и не
вытесняет никого.

```bash
kubectl describe pod <pod> | grep -A3 "Requests"
```

**Placeholder-поды не создают новую ноду.** Проверьте, что node group действительно с auto_scale,
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

**Критичный под запустился не мгновенно.** Убедитесь, что у placeholder-подов
стоит `terminationGracePeriodSeconds: 0` — иначе место освобождается только
после 30-секундного graceful shutdown. Также проверьте, что у критичного пода
нет `preStop`-хуков, замедляющих старт.

## Очистка

```bash
kubectl delete -f manifests/critical-app.yaml
kubectl delete -f manifests/overprovisioning.yaml
kubectl delete -f priorityclasses.yaml
terraform destroy
```
