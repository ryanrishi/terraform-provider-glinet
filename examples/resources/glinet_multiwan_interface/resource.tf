# The router keeps its own track hosts. If you set track_ipv4 or track_ipv6,
# use hosts that answer: the router thinks that an interface whose hosts do not
# answer is offline, and can stop using it.
resource "glinet_multiwan_interface" "wan" {
  interface      = "wan"
  check_enabled  = true
  track_mode     = "normal"
  track_protocol = "ipv4"
}
