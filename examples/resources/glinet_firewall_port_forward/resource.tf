resource "glinet_firewall_port_forward" "web" {
  name      = "web"
  src       = "wan"
  proto     = "tcp"
  src_dport = "8080"
  dest      = "lan"
  dest_ip   = "192.168.8.201"
  dest_port = "80"
}

resource "glinet_firewall_port_forward" "game" {
  src       = "wan"
  proto     = "tcp udp"
  src_dport = "27015-27030"
  dest      = "lan"
  dest_ip   = "192.168.8.202"
  dest_port = "27015-27030"
  enabled   = false
}
