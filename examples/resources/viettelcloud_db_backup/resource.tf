variable "db_instance_id" {
  type = string
}

resource "viettelcloud_db_backup" "example" {
  name        = "example-db-backup"
  description = "Before the schema migration"
  db_instance = { id = var.db_instance_id }

  # Keep the old backup until its replacement completes. Terraform applies
  # create_before_destroy to the referenced DB instance as well, so give that
  # instance a new name whenever it is replaced.
  lifecycle {
    create_before_destroy = true
  }
}
