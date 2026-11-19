resource "glinet_wan_access" "this" {
  https             = true
  ssh               = false
  ping              = true
  whitelist_enabled = true

  # IP address = description
  whitelist = {
    "203.0.113.10" = "office"
    "2001:db8::10" = ""
  }
}
