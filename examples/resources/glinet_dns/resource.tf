resource "glinet_dns" "this" {
  mode              = "auto"
  force_dns         = false
  rebind_protection = true
  override_vpn      = true
}
