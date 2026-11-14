data "glinet_firewall_zones" "zones" {}

output "external_zones" {
  value = data.glinet_firewall_zones.zones.external_zones
}
