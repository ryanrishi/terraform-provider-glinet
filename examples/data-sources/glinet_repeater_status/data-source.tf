data "glinet_repeater_status" "uplink" {}

output "repeater_uplink" {
  value = data.glinet_repeater_status.uplink.state == "connected" ? data.glinet_repeater_status.uplink.ssid : null
}
