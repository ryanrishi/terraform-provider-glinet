resource "glinet_dns" "this" {
  mode              = "manual"
  servers           = ["1.1.1.1", "9.9.9.9", "2606:4700:4700::1111"]
  force_dns         = false
  rebind_protection = true
  override_vpn      = true
}
