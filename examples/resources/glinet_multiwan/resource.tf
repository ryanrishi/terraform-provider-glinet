# Failover: the router uses the wired WAN, and the repeater when the wired WAN is offline.
# The interfaces that are not in the list come after the listed ones.
resource "glinet_multiwan" "this" {
  mode     = "failover"
  priority = ["wan", "wwan"]
}
