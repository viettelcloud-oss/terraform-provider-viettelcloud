# A role that lets project members operate servers. The backend derives
# name = "doi-van-hanh" from the display name.
resource "viettelcloud_project_custom_role" "example" {
  display_name = "Đội vận hành"
  description  = "Operates the project's servers."

  actions = [
    { urn = "urn:action:server:server:list" },
    { urn = "urn:action:server:server:get" },
  ]
}

# A disabled role that grants no action yet.
resource "viettelcloud_project_custom_role" "placeholder" {
  display_name = "Auditors"
  enabled      = false
  actions      = []
}
