resource "viettelcloud_vpc" "example" {
  name        = "my-vpc"
  cidr        = "10.0.0.0/16"
  region      = { name = "vn-central-1" }
  description = "Example VPC"
}
