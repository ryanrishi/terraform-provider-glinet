resource "glinet_firewall_port_forward_order" "this" {
  port_forwards = [
    {
      external_zone = glinet_firewall_port_forward.game.external_zone
      protocol      = glinet_firewall_port_forward.game.protocol
      external_port = glinet_firewall_port_forward.game.external_port
    },
    {
      external_zone = glinet_firewall_port_forward.web.external_zone
      protocol      = glinet_firewall_port_forward.web.protocol
      external_port = glinet_firewall_port_forward.web.external_port
    },
  ]
}
