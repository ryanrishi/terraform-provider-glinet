# The router reboots at 04:00 on Sunday and Wednesday.
resource "glinet_reboot_schedule" "this" {
  enabled = true
  time    = "04:00"
  days    = [0, 3]
}
