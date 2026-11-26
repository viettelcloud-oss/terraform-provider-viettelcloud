# Back up every volume that uses the policy every 2 days, from the first
# execution on.
resource "viettelcloud_volume_backup_policy" "periodic" {
  name        = "example-periodic"
  description = "Backup every 2 days"
  region      = { name = "vn-central-1" }
  status      = "enabled"
  retention   = 7

  schedule = {
    execution_strategy = "periodic"
    frequency          = 2
    frequency_unit     = "day"
    first_execution    = "2030-01-01T02:00:00Z"
  }
}

# Back up on the 1st and the 15th of each month at 02:00 UTC.
resource "viettelcloud_volume_backup_policy" "monthly" {
  name   = "example-monthly"
  region = { name = "vn-central-1" }
  status = "enabled"

  schedule = {
    execution_strategy = "days_of_month"
    days_of_month      = [1, 15]
    time               = "02:00"
  }

  # Deleting a policy that volumes use removes it from those volumes.
  lifecycle {
    prevent_destroy = true
  }
}
