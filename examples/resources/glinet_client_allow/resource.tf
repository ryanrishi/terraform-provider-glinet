# The router must be in allow-list mode, for example with glinet_access_control and mode = "allow_list".
resource "glinet_client_allow" "laptop" {
  mac = "02:00:00:00:00:01"
}
