resource "glinet_zerotier" "this" {
  enabled     = true
  network_id  = var.zerotier_network_id
  lan_enabled = true
}

variable "zerotier_network_id" {
  type      = string
  sensitive = true
}
