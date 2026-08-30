# Развёртывание инфраструктуры: Terraform

Этот файл описывает разворот инфраструктуры для демо приоритетного вытеснения:
кластер Yandex Managed Kubernetes с node group в режиме автоскейлинга (`auto_scale`),
ноды без публичных IP и NAT-шлюз для исходящего трафика, ingress-контроллер Traefik
и стек мониторинга VictoriaMetrics (vmagent + vmsingle + Grafana). Сама статья — в
[README.md](README.md).

## Traefik

`terraform apply` через провайдер Helm устанавливает только ingress-контроллер
(`monitoring.tf`):

1. **Traefik** (`helm_release.traefik`) — ingress-контроллер в namespace `traefik`.
   Service типа LoadBalancer получает статический публичный IP из `yandex_vpc_address.ingress`.

## VictoriaMetrics K8s Stack

Terraform генерирует values-файл `vmks-values.yaml` из шаблона
[vmks-values.yaml.tftpl](vmks-values.yaml.tftpl) (файл `vmks-values.yaml` — в
`.gitignore`). Что в них задано:

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

После `terraform apply` (Terraform уже сгенерировал `vmks-values.yaml`) стек ставится
вручную — команда установки приведена в [README.md](README.md).

## Требования

- [yc CLI](https://yandex.cloud/ru/docs/cli/), настроенный и аутентифицированный (`yc init`)
- [Terraform](https://www.terraform.io/) >= 1.3
- [kubectl](https://kubernetes.io/docs/tasks/tools/)
- [Helm](https://helm.sh/) >= 3 (для установки VictoriaMetrics K8s Stack)

## Запуск

```bash
terraform init
terraform apply
```

После успешного `terraform apply` получаем доступ к кластеру:

```bash
yc managed-kubernetes cluster get-credentials --id $(terraform output -raw k8s_cluster_id) --external --force
kubectl get nodes
```

Должна быть видна одна нода с автоскейлингом (вывод `kubectl get nodes` — в
[README.md](README.md)).

Terraform устанавливает Traefik.
