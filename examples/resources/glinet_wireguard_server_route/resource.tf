# The network 192.168.10.0/24 is behind the peer with the tunnel address 10.0.0.2.
resource "glinet_wireguard_server_route" "office" {
  destination = "192.168.10.0/24"
  gateway     = "10.0.0.2"
  metric      = 10
}
