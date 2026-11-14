resource "glinet_lan_static_lease" "printer" {
  mac  = "02:00:00:00:00:01"
  ip   = "192.168.8.201"
  name = "printer"
}
