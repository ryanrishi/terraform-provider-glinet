# The import ID is the name of the rule on the router. The name must be unique on the router.
# A rule with no name cannot be imported: give it a name on the router first.
terraform import glinet_firewall_rule.ssh_from_office "ssh from office"
