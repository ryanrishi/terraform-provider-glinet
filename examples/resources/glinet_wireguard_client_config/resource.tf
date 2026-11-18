resource "glinet_wireguard_client_group" "home" {
  name = "home"
}

# The keys are made up. Keep real keys out of the configuration, for example in a sensitive variable.
resource "glinet_wireguard_client_config" "home" {
  group_id      = glinet_wireguard_client_group.home.id
  name          = "home server"
  address_v4    = "10.0.0.2/32"
  private_key   = "FAKE+PRIVATE+KEY+FOR+DOCS+ONLY+00000000000A="
  public_key    = "FAKE+SERVER+PUBLIC+KEY+FOR+DOCS+0000000000A="
  preshared_key = "FAKE+PRESHARED+KEY+FOR+DOCS+00000000000000A="
  endpoint      = "vpn.example.com:51820"
  allowed_ips   = ["10.0.0.0/24", "192.168.1.0/24"]
  dns           = ["10.0.0.1"]

  persistent_keepalive = 25
}
