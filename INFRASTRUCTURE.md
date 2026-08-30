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

После `terraform apply` стек ставится вручную — команда установки приведена
в [README.md](README.md).

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
