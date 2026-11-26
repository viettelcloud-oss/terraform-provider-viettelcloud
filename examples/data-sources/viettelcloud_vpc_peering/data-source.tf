# Look up a VPC peering by ID
data "viettelcloud_vpc_peering" "by_id" {
  id = "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"
}

# Look up the pending or active peering from one VPC to another
data "viettelcloud_vpc_peering" "by_vpcs" {
  requester_vpc_id = "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"
  accepter_vpc_id  = "yyyyyyyy-yyyy-yyyy-yyyy-yyyyyyyyyyyy"
}

# Look up a VPC peering by name, in any status
data "viettelcloud_vpc_peering" "by_name" {
  name = "peer-xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"
}
