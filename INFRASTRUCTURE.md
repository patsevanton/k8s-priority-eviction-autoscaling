# Развёртывание инфраструктуры: Terraform

Этот файл описывает разворот инфраструктуры для демо приоритетного вытеснения:
кластер Yandex Managed Kubernetes с node group в режиме автоскейлинга (`auto_scale`),
ноды без публичных IP и NAT-шлюз для исходящего трафика. Сама статья — в [README.md](README.md).

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

## Требования

- [yc CLI](https://yandex.cloud/ru/docs/cli/), настроенный и аутентифицированный (`yc init`)
- [Terraform](https://www.terraform.io/) >= 1.5
- [kubectl](https://kubernetes.io/docs/tasks/tools/)

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

## Стоимость демо

Кластер из одной ноды `standard-v3` (2 vCPU / 4 ГБ) стоит примерно 2 ₽/час +
NAT-шлюз. Полное демо занимает 15–20 минут. Не забудьте `terraform destroy` после.

## Удаление

```bash
terraform destroy
```
