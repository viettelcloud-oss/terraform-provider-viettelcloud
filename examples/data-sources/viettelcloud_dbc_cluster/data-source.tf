# Lookup a DB cluster by ID
data "viettelcloud_dbc_cluster" "by_id" {
  id = "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"
}

# Lookup a DB cluster by name and engine. A name is unique per project and
# engine, so the pair selects one cluster.
data "viettelcloud_dbc_cluster" "by_name" {
  name   = "example-mongodb"
  engine = "mongodb"
}

# Narrow the lookup to one region
data "viettelcloud_dbc_cluster" "by_name_and_region" {
  name   = "example-opensearch"
  engine = "opensearch"
  region = "vn-central-1"
}
