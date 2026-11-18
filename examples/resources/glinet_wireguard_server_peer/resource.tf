# The key is made up. Keep real keys out of the configuration, for example in a sensitive variable.
resource "glinet_wireguard_server_peer" "phone" {
  name          = "phone"
  preshared_key = "FAKE+PEER+PRESHARED+KEY+DOCS+0000000000000A="
  allowed_ips   = ["0.0.0.0/0", "::/0"]
  dns           = ["192.168.8.1"]
}
