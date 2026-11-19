data "glinet_clients" "all" {}

output "online_clients" {
  value = { for c in data.glinet_clients.all.clients : c.mac => coalesce(c.alias, c.name, c.mac) if c.online }
}

output "client_download_bytes" {
  value = { for c in data.glinet_clients.all.clients : c.mac => c.total_rx }
}
