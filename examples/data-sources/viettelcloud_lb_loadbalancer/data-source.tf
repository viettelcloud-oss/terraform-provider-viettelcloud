# Lookup Load Balancer by ID
data "viettelcloud_lb_loadbalancer" "by_id" {
  id = "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"
}

# Lookup Load Balancer by name
data "viettelcloud_lb_loadbalancer" "by_name" {
  name = "primary"
}

# Lookup Load Balancer by name within a region
data "viettelcloud_lb_loadbalancer" "by_name_and_region" {
  name   = "primary"
  region = "vn-central-1"
}

# Lookup by ID with an address filter
data "viettelcloud_lb_loadbalancer" "by_address" {
  id          = "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"
  vip_address = "10.0.0.10"
}

# Lookup with several filters
data "viettelcloud_lb_loadbalancer" "by_filters" {
  name               = "primary"
  region             = "vn-central-1"
  package            = "SMALL"
  topology           = "SINGLE"
  type               = "native"
  access_log_enabled = false
}
