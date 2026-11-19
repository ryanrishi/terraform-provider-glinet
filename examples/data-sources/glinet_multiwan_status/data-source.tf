data "glinet_multiwan_status" "this" {}

output "multiwan_ipv4_status" {
  value = { for i in data.glinet_multiwan_status.this.interfaces : i.interface => i.ipv4_status }
}
