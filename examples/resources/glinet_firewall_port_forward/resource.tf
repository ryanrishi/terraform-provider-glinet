resource "glinet_firewall_port_forward" "web" {
  protocol      = "tcp"
  external_zone = "wan"
  external_port = "8080"
  internal_zone = "lan"
  internal_ip   = "192.168.8.201"
  internal_port = "80"
  name          = "web"
}

resource "glinet_firewall_port_forward" "game" {
  protocol      = "tcp udp"
  external_zone = "wan"
  external_port = "27015-27030"
  internal_zone = "lan"
  internal_ip   = "192.168.8.202"
  internal_port = "27015-27030"
  enabled       = false
}
