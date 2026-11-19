# Clone the MAC address of the device that the upstream network knows.
resource "glinet_wan_port" "this" {
  port     = "wan"
  mode     = "wan"
  mac_mode = "clone"
  mac      = "02:00:5E:10:00:01"
}

# A random MAC address that the router changes each week.
# Only on a router with a second wired WAN port.
resource "glinet_wan_port" "second" {
  port             = "secondwan"
  mode             = "wan"
  mac_mode         = "random"
  mac_update       = "time"
  mac_period_hours = 168
}
