data "glinet_system_timezone_config" "config" {}

output "router_timezone" {
  value = data.glinet_system_timezone_config.config.zonename
}
