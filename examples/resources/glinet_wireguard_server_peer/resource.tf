# The key is made up. Keep real keys out of the configuration, for example in a sensitive variable.
resource "glinet_wireguard_server_peer" "phone" {
  name          = "phone"
  preshared_key = "FAKE+PEER+PRESHARED+KEY+DOCS+0000000000000A="
  allowed_ips   = ["0.0.0.0/0", "::/0"]
  dns           = ["192.168.8.1"]
}

# The configuration file of the client device. vpn.example.com is the public name of the router.
output "phone_wireguard_config" {
  sensitive = true
  value     = <<-EOT
    [Interface]
    Address = ${glinet_wireguard_server_peer.phone.client_config.address}
    PrivateKey = ${glinet_wireguard_server_peer.phone.client_config.private_key}
    DNS = ${join(", ", glinet_wireguard_server_peer.phone.client_config.dns)}

    [Peer]
    PublicKey = ${glinet_wireguard_server_peer.phone.client_config.public_key}
    PresharedKey = ${glinet_wireguard_server_peer.phone.client_config.preshared_key}
    AllowedIPs = ${join(", ", glinet_wireguard_server_peer.phone.client_config.allowed_ips)}
    Endpoint = vpn.example.com:51820
  EOT
}
