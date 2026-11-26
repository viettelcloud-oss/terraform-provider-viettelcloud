resource "viettelcloud_fs_filesystem_snapshot_policy" "nightly" {
  name         = "example-nightly"
  description  = "Weekday snapshots at 02:00 UTC"
  region       = { name = "vn-central-1" }
  days_of_week = ["mon", "tue", "wed", "thu", "fri"]
  time         = "02:00"
  retention    = 7
  status       = "enabled"
}
