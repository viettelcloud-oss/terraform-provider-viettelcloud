# Lookup vDC by ID
data "viettelcloud_vdc" "by_id" {
  id = "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"
}

# Lookup vDC by Name and Zone
data "viettelcloud_vdc" "by_name_and_zone" {
  name = "example-vdc"
  zone = "vn-central-1a"
}

# Lookup vDC by Name and Region
data "viettelcloud_vdc" "by_name_and_region" {
  name   = "example-vdc"
  region = "vn-central-1"
}
