# Публичный IP-адрес для балансировщика Traefik (ingress в кластер).
# Из IP через sslip.io формируется FQDN Grafana (см. locals.tf).
resource "yandex_vpc_address" "ingress" {
  name = "priority-ingress-pip"
  external_ipv4_address {
    zone_id = yandex_vpc_subnet.priority-b.zone
  }
}
