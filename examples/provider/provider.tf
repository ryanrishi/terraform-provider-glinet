terraform {
  required_providers {
    glinet = {
      source = "ryanrishi/glinet"
    }
  }
}

# The arguments can also come from GLINET_HOST, GLINET_USERNAME and GLINET_PASSWORD.
# With all three set, the block can be empty: provider "glinet" {}
provider "glinet" {
  host     = "http://192.168.8.1"
  username = "root"
  password = var.glinet_password
}

variable "glinet_password" {
  type      = string
  sensitive = true
}
