# A change of http_port, https_port or redirect_https moves the router API.
# After the apply, change the host of the provider to the new address.
resource "glinet_local_access" "this" {
  session_timeout = 1800
  ssh_enabled     = true
  ssh_port        = 2222
}
