resource "glinet_firewall_rule" "ssh_from_office" {
  name             = "ssh from office"
  source_zone      = "wan"
  source_ip        = "203.0.113.0/24"
  protocol         = "tcp"
  destination_zone = "lan"
  destination_ip   = "192.168.8.201"
  destination_port = "22"
  action           = "ACCEPT"
}

# The firewall applies the first matching rule, so this rule must come after the rule above.
resource "glinet_firewall_rule" "block_ssh" {
  name             = "block ssh"
  source_zone      = "wan"
  protocol         = "tcp"
  destination_zone = "lan"
  destination_ip   = "192.168.8.201"
  destination_port = "22"
  action           = "DROP"

  depends_on = [glinet_firewall_rule.ssh_from_office]
}
