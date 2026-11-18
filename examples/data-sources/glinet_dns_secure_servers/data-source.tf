data "glinet_dns_secure_servers" "all" {}

output "doh_server_names" {
  value = [for s in data.glinet_dns_secure_servers.all.servers : s.name if s.protocol == "DoH" && s.no_log]
}
