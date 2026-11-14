data "glinet_system_info" "router" {}

output "firmware_version" {
  value = data.glinet_system_info.router.firmware_version
}
