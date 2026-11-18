data "glinet_wan_status" "wan" {}

output "uplink_addresses" {
  value = { for i in data.glinet_wan_status.wan.interfaces : i.interface => i.ip }
}
