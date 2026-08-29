# Развёртывание инфраструктуры: Terraform

Этот файл описывает разворот инфраструктуры для демо приоритетного вытеснения:
кластер Yandex Managed Kubernetes с node group в режиме автоскейлинга (`auto_scale`),
ноды без публичных IP и NAT-шлюз для исходящего трафика, ingress-контроллер Traefik
и стек мониторинга VictoriaMetrics (vmagent + vmsingle + Grafana). Сама статья — в
[README.md](README.md).

## Почему auto_scale — это и есть Cluster Autoscaler

В Yandex Managed K8s не нужно устанавливать Cluster Autoscaler отдельно: достаточно
указать `scale_policy.auto_scale` вместо `fixed_scale` в node group — и платформа
запускает управляемый Cluster Autoscaler, который масштабирует группу от `min` до `max`.

Для демо выбрана дешёвая конфигурация:

- **Нода**: 2 vCPU / 4 ГБ RAM (`standard-v3`) — маленькая нода, чтобы вытеснение
  срабатывало быстро и наглядно
- **min = 1, max = 3** — Cluster Autoscaler начнёт с одной ноды и при нехватке
  ресурсов добавит вторую
- **Ноды без публичных IP** — исходящий трафик через NAT-шлюз (`net.tf`)
- **Одна зона (`ru-central1-b`) для node group** — ограничение Yandex Managed K8s:
  группы нод с `auto_scale` могут иметь только одну location. Мастер при этом
  остаётся региональным (3 зоны отказоустойчивости)
- **Traefik** — ingress-контроллер; балансировщик получает статический публичный IP
  (`ip-dns.tf`), из которого через sslip.io формируется FQDN Grafana
- **VictoriaMetrics K8s Stack** — устанавливается Terraform'ом через `helm_release`
  в namespace `vmks` (`monitoring.tf`)

## VictoriaMetrics K8s Stack

`terraform apply` устанавливает две вещи через провайдер Helm (`monitoring.tf`):

1. **Traefik** (`helm_release.traefik`) — ingress-контроллер в namespace `traefik`.
   Service типа LoadBalancer получает статический публичный IP из `yandex_vpc_address.ingress`.

2. **victoria-metrics-k8s-stack** (`helm_release.vmks`, чарт 0.90.2) в namespace `vmks` —
   минимальный стек мониторинга: vmagent + vmsingle + Grafana + node-exporter +
   kube-state-metrics. vmsingle — источник метрик RPS для KEDA ScaledObject
   (`http://vmsingle-vmks-victoria-metrics-k8s-stack.vmks.svc.cluster.local:8428`, см. `manifests/keda/scaledobject.yaml`).

Values рендерятся Terraform'ом из шаблона [vmks-values.yaml.tftpl](vmks-values.yaml.tftpl)
в `vmks-values.yaml` (файл в `.gitignore`). Что в них задано:

- **Grafana за Traefik** по адресу `http://grafana.<IP>.sslip.io` (IP — публичный адрес
  балансировщика Traefik; sslip.io — бесплатный wildcard-DNS, резолвится без настройки).
- **Отключены vmalert и alertmanager** — демо-стенду на нодах 2 vCPU / 4 ГБ нужен
  минимальный стек.
- **Отключены scrape-job и recording-правила для control-plane компонентов Yandex
  Managed K8s** (`kubeControllerManager`, `kubeScheduler`, `kubeEtcd`, группы правил
  `etcd`, `kubernetes-system-scheduler`, `kubernetes-system-controller-manager`,
  `kube-scheduler.rules`): master управляемый и вне кластера, эти компоненты недоступны
  для скрейпинга — иначе vmagent плодит `ScrapePoolHasNoTargets`, а vmalert —
  `RecordingRulesNoData`.

Если нужно обновить релиз вручную (Terraform уже сгенерировал `vmks-values.yaml`):

```bash
helm upgrade --install vmks victoria-metrics-k8s-stack/victoria-metrics-k8s-stack \
  --namespace vmks --create-namespace \
  --version 0.90.2 \
  --wait --values vmks-values.yaml
```

## Требования

- [yc CLI](https://yandex.cloud/ru/docs/cli/), настроенный и аутентифицированный (`yc init`)
- [Terraform](https://www.terraform.io/) >= 1.3
- [kubectl](https://kubernetes.io/docs/tasks/tools/)
- [Helm](https://helm.sh/) >= 3 (для ручных операций с релизами)

## Запуск

```bash
# Клонируем репозиторий
git clone https://github.com/patsevanton/k8s-priority-eviction-autoscaling
cd k8s-priority-eviction-autoscaling

# Указываем ID каталога Yandex Cloud
export YC_FOLDER_ID=<ваш-folder-id>

terraform init
terraform apply
```

После успешного `terraform apply` получаем доступ к кластеру:

```bash
yc managed-kubernetes cluster get-credentials --id $(terraform output -raw k8s_cluster_id) --external --force
kubectl get nodes
```

Должна быть видна одна нода с автоскейлингом:

```
NAME                       STATUS   ROLES    AGE   VERSION
cl1v2fmpkgn4srb2b1mm-uxyz   Ready    <none>   2m    v1.33.x
```

Terraform также устанавливает Traefik и VictoriaMetrics K8s Stack (см. раздел выше).
Проверяем поды мониторинга и доступ к Grafana:

```bash
$ kubectl get pods -n vmks
NAME                                                        READY   STATUS    RESTARTS   AGE
vmks-grafana-...                                            1/1     Running   0          2m
vmks-kube-state-metrics-...                                 1/1     Running   0          2m
vmks-prometheus-node-exporter-...                           1/1     Running   0          2m
vmks-victoria-metrics-operator-...                          1/1     Running   0          2m
vmsingle-vmks-victoria-metrics-k8s-stack-...                1/1     Running   0          2m
vmagent-vmks-victoria-metrics-k8s-stack-...                 1/1     Running   0          2m

$ terraform output -raw grafana_url
http://grafana.84.201.172.10.sslip.io

$ kubectl -n vmks get secret vmks-grafana -o jsonpath='{.data.admin-password}' | base64 --decode; echo
<пароль admin>
```

Grafana открывается по URL из output `grafana_url` (логин `admin`), datasource
VictoriaMetrics уже настроен как default.

## Стоимость демо

Кластер из одной ноды `standard-v3` (2 vCPU / 4 ГБ) стоит примерно 2 ₽/час +
NAT-шлюз + публичный IP балансировщика Traefik. Полное демо занимает 15–20 минут.
Не забудьте `terraform destroy` после.

## Удаление

```bash
terraform destroy
```
