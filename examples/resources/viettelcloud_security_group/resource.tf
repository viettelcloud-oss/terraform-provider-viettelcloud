# Basic security group
resource "viettelcloud_security_group" "example" {
  name        = "my-security-group"
  region      = { name = "vn-central-1" }
  description = "Example security group"
}
