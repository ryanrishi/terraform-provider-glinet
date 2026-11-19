resource "glinet_multiwan_interface" "wan" {
  interface      = "wan"
  check_enabled  = true
  track_mode     = "normal"
  track_protocol = "ipv4"
  track_ipv4     = ["192.0.2.1", "198.51.100.1"]
}
