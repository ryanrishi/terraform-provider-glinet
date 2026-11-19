data "glinet_tethering_status" "usb" {}

output "tethering_connected" {
  value = data.glinet_tethering_status.usb.status == "connected"
}
