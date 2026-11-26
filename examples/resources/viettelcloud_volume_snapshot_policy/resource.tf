resource "viettelcloud_volume_snapshot_policy" "nightly" {
  name        = "example-nightly"
  description = "Weekday snapshots at 02:00 UTC"
  region      = { name = "vn-central-1" }
  status      = "enabled"
  retention   = 7

  schedule = {
    days_of_week = ["mon", "tue", "wed", "thu", "fri"]
    time         = "02:00"
  }

  # Deleting a policy removes it from the volumes that use it.
  lifecycle {
    prevent_destroy = true
  }
}
