resource "viettelcloud_vpc" "example" {
  name        = "my-vpc"
  cidr        = "10.0.0.0/16"
  region      = { name = "vn-central-1" }
  description = "Example VPC"
}

resource "viettelcloud_subnet" "example" {
  name        = "my-subnet"
  cidr        = "10.0.1.0/24"
  vpc         = { id = viettelcloud_vpc.example.id }
  description = "Example subnet"
}

resource "viettelcloud_private_ip" "example" {
  subnet      = { id = viettelcloud_subnet.example.id }
  description = "Example private IP"
}
