data "glinet_system_timezone" "router" {}

output "router_timezone" {
  value = data.glinet_system_timezone.router.zonename
}
