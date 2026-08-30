# Мониторинг: Traefik (ingress).

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
