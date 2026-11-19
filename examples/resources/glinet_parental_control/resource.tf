# Clients that are in no group keep their access, also the computer that runs Terraform.
resource "glinet_parental_control" "this" {
  enabled        = true
  drop_anonymous = false
}
