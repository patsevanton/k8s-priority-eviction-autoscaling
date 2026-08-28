locals {
  folder_id  = var.folder_id
  network_id = yandex_vpc_network.priority.id

  subnet_b_id   = yandex_vpc_subnet.priority-b.id
  subnet_d_id   = yandex_vpc_subnet.priority-d.id
  subnet_e_id   = yandex_vpc_subnet.priority-e.id
  subnet_b_zone = yandex_vpc_subnet.priority-b.zone
  subnet_d_zone = yandex_vpc_subnet.priority-d.zone
  subnet_e_zone = yandex_vpc_subnet.priority-e.zone

  # Публичный IP балансировщика Traefik → FQDN Grafana через sslip.io
  ingress_ip   = yandex_vpc_address.ingress.external_ipv4_address[0].address
  grafana_fqdn = "grafana.${local.ingress_ip}.sslip.io"
}
