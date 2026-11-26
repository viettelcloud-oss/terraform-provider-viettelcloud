# Look up a custom role by ID.
data "viettelcloud_project_custom_role" "by_id" {
  id = "00000000-0000-0000-0000-000000000000"
}

# Look up a custom role by its exact name.
data "viettelcloud_project_custom_role" "by_name" {
  name = "doi-van-hanh"
}
