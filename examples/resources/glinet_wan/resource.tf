resource "glinet_wan" "this" {
  protocol = "static"
  ipv4 = {
    ip      = "203.0.113.2"
    netmask = "255.255.255.0"
    gateway = "203.0.113.1"
    dns     = ["1.1.1.1", "8.8.8.8"]
  }
  mtu = 1500
}

# Only on a router with a second wired WAN port.
resource "glinet_wan" "second" {
  port     = "secondwan"
  protocol = "dhcp"
}
