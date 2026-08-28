# Мониторинг: Traefik (ingress) + VictoriaMetrics K8s Stack.
#
# VictoriaMetrics ставится через helm_release в namespace `vmks` (victoria-metrics-k8s-stack:
# vmagent + vmsingle + Grafana + node-exporter + kube-state-metrics). vmsingle — источник
# метрик для KEDA ScaledObject (http://vmsingle-vmks.vmks.svc.cluster.local:8428),
# Grafana открывается через Traefik по FQDN из sslip.io.
#
# Values рендерятся из vmks-values.yaml.tftpl (подстановка ingress_public_ip) и
# дополнительно пишутся в vmks-values.yaml — можно обновить релиз вручную через helm.

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
  chart            = "traefik"
  repository       = "https://traefik.github.io/charts"
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

# VictoriaMetrics K8s Stack в namespace vmks (vmagent + vmsingle + Grafana).
# vmalert и alertmanager отключены в values (демо-стенд), scrape-job и recording-правила
# недоступных control-plane компонентов Yandex Managed K8s отключены (см. vmks-values.yaml.tftpl).
resource "helm_release" "vmks" {
  name             = "vmks"
  chart            = "victoria-metrics-k8s-stack"
  repository       = "https://victoriametrics.github.io/helm-charts/"
  namespace        = "vmks"
  create_namespace = true
  version          = "0.90.2"
  wait             = true
  timeout          = 900

  values = [local.vmks_values]

  depends_on = [
    helm_release.traefik,
    local_file.write_vmks_values,
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
