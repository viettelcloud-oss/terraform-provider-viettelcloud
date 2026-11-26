variable "subnet_id" {
  type = string
}

variable "root_password" {
  type      = string
  sensitive = true
}

resource "viettelcloud_db_instance" "example" {
  name          = "example-db"
  description   = "Orders database"
  database_type = "postgresql"
  db_version    = { version = "16" }
  region        = { name = "hn-1" }
  flavor        = { name = "db.small" }
  volume_type   = { name = "ssd" }
  disk_size     = 20
  solution      = "basic"
  subnet        = { id = var.subnet_id }
  root_password = var.root_password

  backup_policy = {
    enabled         = true
    backup_interval = "weekly"
    day_of_week     = "sunday"
    start_hour      = 2
    max_auto_backup = 7
  }

  # Replacing a DB instance destroys its data.
  lifecycle {
    prevent_destroy = true
  }
}
