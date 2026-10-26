resource "viettelcloud_elastic_ip" "example" {
  description = "Managed by Terraform"
  region      = { name = "vn-central-1" }
  enable_ipv4 = true
  enable_ipv6 = false
}
