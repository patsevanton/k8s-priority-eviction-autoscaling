provider "yandex" {
  # Все ресурсы без явного folder_id создаются в этом folder.
  # Без явного указания провайдер берёт folder из YC_FOLDER_ID,
  # который может указывать на несуществующий folder.
  folder_id = var.folder_id
}

# Провайдер helm устанавливает Traefik и VictoriaMetrics K8s Stack в кластер.
# Аутентификация через yc CLI (токен для kube-apiserver).
provider "helm" {
  kubernetes = {
    host                   = yandex_kubernetes_cluster.priority.master[0].external_v4_endpoint
    cluster_ca_certificate = yandex_kubernetes_cluster.priority.master[0].cluster_ca_certificate
    exec = {
      api_version = "client.authentication.k8s.io/v1beta1"
      args        = ["k8s", "create-token"]
      command     = "yc"
    }
  }
}
