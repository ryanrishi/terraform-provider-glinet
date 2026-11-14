data "glinet_system_status" "router" {}

output "online_interfaces" {
  value = [for n in data.glinet_system_status.router.networks : n.interface if n.online]
}
