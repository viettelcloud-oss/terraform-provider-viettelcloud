resource "viettelcloud_vpc" "app" {
  name   = "app-vpc"
  cidr   = "10.10.0.0/16"
  region = { name = "vn-central-1" }
}

resource "viettelcloud_vpc" "db" {
  name   = "db-vpc"
  cidr   = "10.20.0.0/16"
  region = { name = "vn-central-1" }
}

resource "viettelcloud_vpc_peering" "example" {
  requester_vpc = { id = viettelcloud_vpc.app.id }
  accepter_vpc  = { id = viettelcloud_vpc.db.id }
  description   = "App VPC to database VPC"
}
