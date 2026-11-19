resource "glinet_lan" "guest" {
  name                  = "guest"
  ip                    = "192.168.9.1"
  netmask               = "255.255.255.0"
  ap_isolation_enabled  = true
  wan_isolation_enabled = false
  dhcp_enabled          = true
  dhcp_start            = "192.168.9.100"
  dhcp_end              = "192.168.9.249"
  dhcp_lease_time       = "12h"
}
