data "viettelcloud_db_backup" "example" {
  name           = "example-db-backup"
  db_instance_id = "00000000-0000-0000-0000-000000000001"
  backup_type    = "manual"
}
