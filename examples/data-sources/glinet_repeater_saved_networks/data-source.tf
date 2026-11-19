data "glinet_repeater_saved_networks" "saved" {}

output "saved_ssids" {
  value = data.glinet_repeater_saved_networks.saved.networks[*].ssid
}
