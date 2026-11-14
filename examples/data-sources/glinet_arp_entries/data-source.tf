data "glinet_arp_entries" "arp" {}

output "lan_neighbor_ips" {
  value = [for e in data.glinet_arp_entries.arp.entries : e.ip if e.device == "br-lan"]
}
