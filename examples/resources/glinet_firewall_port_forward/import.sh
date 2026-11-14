# The import ID is <src>/<proto>/<src_dport>. Write the protocol "tcp udp" as tcp+udp.
terraform import glinet_firewall_port_forward.web wan/tcp/8080
terraform import glinet_firewall_port_forward.game wan/tcp+udp/27015-27030
