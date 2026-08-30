# Мониторинг: Traefik (ingress).

locals {
  vmks_values = templatefile("${path.module}/vmks-values.yaml.tftpl", {
    ingress_public_ip = local.ingress_ip
  })
}

resource "local_file" "write_vmks_values" {
  content         = local.vmks_values
  filename        = "${path.module}/vmks-values.yaml"
  file_permission = "0644"
}

# Ingress-контроллер Traefik: балансировщик LoadBalancer получает публичный IP
# (yandex_vpc_address.ingress), из которого через sslip.io формируется FQDN Grafana.
resource "helm_release" "traefik" {
  name             = "traefik"
  chart            = "oci://ghcr.io/traefik/helm/traefik"
  namespace        = "traefik"
  create_namespace = true
  version          = "41.3.0"

  values = [
    yamlencode({
      image = {
        registry   = "ghcr.io"
        repository = "traefik/traefik"
      }
      service = {
        spec = {
          type           = "LoadBalancer"
          loadBalancerIP = local.ingress_ip
        }
      }
    })
  ]

  depends_on = [
    yandex_kubernetes_cluster.priority,
    yandex_kubernetes_node_group.k8s_node_group,
  ]
}

output "grafana_url" {
  description = "URL Grafana (FQDN из публичного IP Traefik через sslip.io)"
  value       = "http://${local.grafana_fqdn}"
}

output "grafana_admin_password_command" {
  description = "Команда для получения пароля admin Grafana"
  value       = "kubectl -n vmks get secret vmks-grafana -o jsonpath='{.data.admin-password}' | base64 --decode; echo"
}
