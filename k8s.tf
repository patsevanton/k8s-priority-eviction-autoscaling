# Создание сервисного аккаунта для управления Kubernetes
resource "yandex_iam_service_account" "sa_k8s_editor" {
  folder_id = local.folder_id
  name      = "priority-sa-k8s-editor" # Имя сервисного аккаунта
}

# Назначение роли "editor" сервисному аккаунту на уровне папки
resource "yandex_resourcemanager_folder_iam_member" "sa_k8s_editor_permissions" {
  folder_id = local.folder_id
  role      = "editor"                                                        # Роль, дающая полные права на ресурсы папки
  member    = "serviceAccount:${yandex_iam_service_account.sa_k8s_editor.id}" # Назначаемый участник
}

# Пауза, чтобы изменения IAM успели примениться до создания кластера
resource "time_sleep" "wait_sa" {
  create_duration = "20s"
  depends_on = [
    yandex_iam_service_account.sa_k8s_editor,
    yandex_resourcemanager_folder_iam_member.sa_k8s_editor_permissions,
  ]
}

# Создание Kubernetes-кластера в Yandex Cloud
resource "yandex_kubernetes_cluster" "priority" {
  name       = "priority" # Имя кластера
  folder_id  = local.folder_id
  network_id = local.network_id # Сеть, к которой подключается кластер

  master {
    version = "1.33" # Версия Kubernetes мастера
    regional {
      region = "ru-central1" # Регион размещения мастера (3 зоны отказоустойчивости)

      location {
        zone      = local.subnet_b_zone # Зона размещения мастера
        subnet_id = local.subnet_b_id   # Подсеть для мастера
      }

      location {
        zone      = local.subnet_d_zone # Зона размещения мастера
        subnet_id = local.subnet_d_id   # Подсеть для мастера
      }

      location {
        zone      = local.subnet_e_zone # Зона размещения мастера
        subnet_id = local.subnet_e_id   # Подсеть для мастера
      }
    }

    public_ip = true # Включение публичного IP для доступа к мастеру
  }

  # Сервисный аккаунт для управления кластером и нодами
  service_account_id      = yandex_iam_service_account.sa_k8s_editor.id
  node_service_account_id = yandex_iam_service_account.sa_k8s_editor.id

  release_channel = "STABLE" # Канал обновлений

  depends_on = [
    time_sleep.wait_sa,
  ]
}

# Группа узлов с автоскейлингом: auto_scale включает Cluster Autoscaler
# в Yandex Managed K8s — отдельная установка не нужна.
resource "yandex_kubernetes_node_group" "k8s_node_group" {
  description = "Autoscaled node group for the priority eviction demo"
  name        = "priority-node-group"
  cluster_id  = yandex_kubernetes_cluster.priority.id
  version     = "1.33" # Версия Kubernetes на нодах

  # Автоскейлинг: Cluster Autoscaler будет менять количество нод от 1 до 3
  scale_policy {
    auto_scale {
      min     = 1 # Минимальное количество нод
      max     = 5 # Максимальное количество нод
      initial = 1 # Стартовое количество нод
    }
  }

  # Ограничение Yandex Managed K8s: node group с auto_scale может располагаться
  # только в одной зоне — Cluster Autoscaler масштабирует группу в пределах одной location.
  allocation_policy {
    location { zone = local.subnet_b_zone }
  }

  instance_template {
    platform_id = "standard-v3"

    network_interface {
      nat        = false # Публичные IP на нодах выключены; исходящий трафик через NAT-шлюз (см. net.tf)
      subnet_ids = [local.subnet_b_id]
    }

    resources {
      cores  = 2 # vCPU
      memory = 4 # ГБ
    }

    boot_disk {
      type = "network-ssd" # Тип диска
      size = 33            # Размер диска
    }
  }
}

# Вывод команды для получения kubeconfig
output "k8s_cluster_credentials_command" {
  value = "yc managed-kubernetes cluster get-credentials --id ${yandex_kubernetes_cluster.priority.id} --external --force"
}

output "k8s_cluster_id" {
  description = "ID кластера"
  value       = yandex_kubernetes_cluster.priority.id
}
