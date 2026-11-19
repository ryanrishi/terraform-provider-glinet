data "glinet_fan_status" "fan" {}

output "fan_speed" {
  value = data.glinet_fan_status.fan.speed
}
