data "glinet_wifi_status" "wifi" {}

output "radio_channels" {
  value = { for r in data.glinet_wifi_status.wifi.radios : r.name => r.channel }
}
