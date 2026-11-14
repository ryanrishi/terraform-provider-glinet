# The import ID is <external_zone>/<protocol>/<external_port>. Write the protocol "tcp udp" as tcp+udp.
terraform import glinet_firewall_port_forward.web wan/tcp/8080
terraform import glinet_firewall_port_forward.game wan/tcp+udp/27015-27030
