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
# A replaced rule goes to the end of the list, so this rule is replaced when the rule above is replaced.
resource "glinet_firewall_rule" "block_ssh" {
  name             = "block ssh"
  source_zone      = "wan"
  protocol         = "tcp"
  destination_zone = "lan"
  destination_ip   = "192.168.8.201"
  destination_port = "22"
  action           = "DROP"

  lifecycle {
    replace_triggered_by = [glinet_firewall_rule.ssh_from_office]
  }
}

# IPv6 addresses and CIDR blocks work in source_ip and destination_ip.
# Set destination_port: on firmware 4.8.3, Terraform cannot update or destroy a rule with no destination port.
resource "glinet_firewall_rule" "https_ipv6" {
  name             = "https over ipv6"
  source_zone      = "wan"
  source_ip        = "2001:db8:1::/48"
  protocol         = "tcp"
  destination_zone = "lan"
  destination_ip   = "2001:db8::20"
  destination_port = "443"
  action           = "ACCEPT"
}
