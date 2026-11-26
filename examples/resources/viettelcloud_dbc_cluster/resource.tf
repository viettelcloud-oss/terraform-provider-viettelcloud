variable "db_root_password" {
  type      = string
  sensitive = true
}

resource "viettelcloud_vpc" "example" {
  name   = "db-example-vpc"
  cidr   = "10.0.0.0/16"
  region = { name = "vn-central-1" }
}

resource "viettelcloud_subnet" "example" {
  name = "db-example-subnet"
  cidr = "10.0.1.0/24"
  vpc  = { id = viettelcloud_vpc.example.id }
}

# A MongoDB replica set. The node pool is named mongo for mongo_rs and shard
# for mongo_sharding, and a sharded cluster also sets partition_count to the
# number of shards.
resource "viettelcloud_dbc_cluster" "mongodb" {
  name           = "example-mongodb"
  description    = "Orders database"
  engine         = "mongodb"
  engine_version = { version = "7.0" }
  region         = { name = "vn-central-1" }
  subnet         = { id = viettelcloud_subnet.example.id }
  topology       = "mongo_rs"

  # root_password is write-only: Terraform sends it when it creates the cluster
  # and never stores it. Changing it later has no effect.
  root_password = var.db_root_password

  node_pools = [{
    name            = "mongo"
    replicas        = 3
    resource_config = { name = "2c4g" }
    storage_class   = { name = "ssd" }
    disk_size       = 100
  }]

  auto_backup_config = {
    enable_auto_backup = true
    backup_interval    = "weekly"
    day_of_week        = "sunday"
    start_hour         = 2
    max_auto_backup    = 7
  }

  # Only description and keep_backup change in place. Changing anything else,
  # such as the node pools or the backup schedule, replaces the cluster and
  # deletes its data, so keep this on a cluster that holds real data.
  lifecycle {
    prevent_destroy = true
  }
}

# An OpenSearch cluster. It takes no topology, and its node pools are named by
# you. The cluster needs at least one data node and three cluster manager
# nodes in total.
resource "viettelcloud_dbc_cluster" "opensearch" {
  name           = "example-opensearch"
  engine         = "opensearch"
  engine_version = { version = "2.11" }
  region         = { name = "vn-central-1" }
  subnet         = { id = viettelcloud_subnet.example.id }

  root_password = var.db_root_password

  node_pools = [
    {
      name            = "manager"
      replicas        = 3
      resource_config = { name = "2c4g" }
      storage_class   = { name = "ssd" }
      disk_size       = 50
      engine_roles    = ["cluster_manager"]
    },
    {
      name            = "data"
      replicas        = 2
      resource_config = { name = "4c8g" }
      storage_class   = { name = "ssd" }
      disk_size       = 200
      engine_roles    = ["data", "ingest"]
      pool_config     = { attribute = "hot" }
    },
  ]

  plugins = [{ name = "analysis-icu" }]
}

# A Cassandra cluster. It takes no topology and one node pool named cassandra.
# The backend always disables SSL for Cassandra.
resource "viettelcloud_dbc_cluster" "cassandra" {
  name           = "example-cassandra"
  engine         = "cassandra"
  engine_version = { version = "4.1" }
  region         = { name = "vn-central-1" }
  subnet         = { id = viettelcloud_subnet.example.id }

  root_password = var.db_root_password

  node_pools = [{
    name            = "cassandra"
    replicas        = 3
    resource_config = { name = "4c8g" }
    storage_class   = { name = "ssd" }
    disk_size       = 100
  }]
}

# A Memcached cluster. Its nodes keep data in memory, so its node pools take
# no storage_class or disk_size, and it has no backups. The mcrouter mode also
# needs a node pool named mcrouter.
resource "viettelcloud_dbc_cluster" "memcached" {
  name           = "example-memcached"
  engine         = "memcached"
  engine_version = { version = "1.6" }
  region         = { name = "vn-central-1" }
  subnet         = { id = viettelcloud_subnet.example.id }

  root_password = var.db_root_password

  engine_config = {
    mode         = "direct"
    auth_enabled = true
  }

  node_pools = [{
    name            = "memcached"
    replicas        = 2
    resource_config = { name = "2c4g" }
  }]
}

# A StarRocks cluster. It takes two node pools named fe and be. The fe pool
# has 1 or 3 replicas, and the be pool an odd number up to 9. The backend
# always disables SSL for StarRocks, and the engine has no backups.
resource "viettelcloud_dbc_cluster" "starrocks" {
  name           = "example-starrocks"
  engine         = "starrocks"
  engine_version = { version = "3.2" }
  region         = { name = "vn-central-1" }
  subnet         = { id = viettelcloud_subnet.example.id }

  root_password = var.db_root_password

  node_pools = [
    {
      name            = "fe"
      replicas        = 1
      resource_config = { name = "2c4g" }
      storage_class   = { name = "ssd" }
      disk_size       = 50
    },
    {
      name            = "be"
      replicas        = 3
      resource_config = { name = "4c8g" }
      storage_class   = { name = "ssd" }
      disk_size       = 200
    },
  ]
}

# A TimescaleDB cluster. It takes one node pool with a name of your choice.
# It keeps backups for retention_days, and point-in-time recovery comes on
# together with the backup schedule.
resource "viettelcloud_dbc_cluster" "timescaledb" {
  name           = "example-timescaledb"
  engine         = "timescaledb"
  engine_version = { version = "17" }
  region         = { name = "vn-central-1" }
  subnet         = { id = viettelcloud_subnet.example.id }

  root_password = var.db_root_password

  node_pools = [{
    name            = "db"
    replicas        = 1
    resource_config = { name = "2c4g" }
    storage_class   = { name = "ssd" }
    disk_size       = 100
  }]

  enable_pitr_backup = true
  auto_backup_config = {
    enable_auto_backup = true
    backup_interval    = "daily"
    start_hour         = 1
    retention_days     = 14
  }
}
