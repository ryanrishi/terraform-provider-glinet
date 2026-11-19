resource "glinet_firewall_dmz" "this" {
  enabled         = true
  internal_ip     = "192.168.8.200"
  higher_priority = true
}
