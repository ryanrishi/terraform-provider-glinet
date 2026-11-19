# Initializes the WireGuard server when it has no keys, and keeps the private key of the router.
# The server must be stopped for a change of address_v4, address_v6, listen_port or private_key.
resource "glinet_wireguard_server" "this" {
  address_v4       = "10.0.0.1/24"
  address_v6       = "fd00:db8::1/64"
  listen_port      = 51820
  client_to_client = false
  local_access     = true
  masquerade       = true
  mtu              = 1420
}
