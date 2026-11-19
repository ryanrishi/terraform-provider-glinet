# The allow list can be filled while the router is in block-list mode.
# Add the computer that runs Terraform and each other client that must keep its access,
# then start allow-list mode after the entries exist.
resource "glinet_client_allow" "laptop" {
  mac = "02:00:00:00:00:01"
}

resource "glinet_client_allow" "phone" {
  mac = "02:00:00:00:00:02"
}

resource "glinet_access_control" "this" {
  mode = "allow_list"

  depends_on = [
    glinet_client_allow.laptop,
    glinet_client_allow.phone,
  ]
}
