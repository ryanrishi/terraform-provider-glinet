variable "guest_wifi_key" {
  type      = string
  sensitive = true
}

resource "glinet_wifi_interface" "guest" {
  name       = "guest5g"
  ssid       = "guest"
  encryption = "sae-mixed"
  key        = var.guest_wifi_key
  enabled    = true
}
