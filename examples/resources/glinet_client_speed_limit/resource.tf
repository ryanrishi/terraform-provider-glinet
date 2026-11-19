# Download at most 1024 KiB/s, no upload limit.
resource "glinet_client_speed_limit" "tablet" {
  mac      = "02:00:00:00:00:01"
  download = 1024
}
