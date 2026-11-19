resource "glinet_wan_access" "this" {
  https_enabled      = true
  ssh_enabled        = false
  ping_enabled       = true
  allow_list_enabled = true

  # IP address = description
  allow_list = {
    "203.0.113.10" = "office"
    "2001:db8::10" = ""
  }
}
