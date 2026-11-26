resource "viettelcloud_vpc" "example" {
  name        = "my-lb-vpc"
  cidr        = "10.20.0.0/16"
  region      = { name = "vn-central-1" }
  description = "VPC for a Load Balancer"
}

resource "viettelcloud_lb_loadbalancer" "example" {
  name        = "my-load-balancer"
  description = "Managed by Terraform"
  package     = "SMALL"
  topology    = "SINGLE"
  region      = { name = "vn-central-1" }
  vpc         = { id = viettelcloud_vpc.example.id }

  # Assign a public IPv4 address. Omit it to keep public IP off.
  public_ip_mode = "ipv4-only"
}
