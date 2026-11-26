# Look up a server group member by ID
data "viettelcloud_lb_member" "by_id" {
  server_group_id = "11111111-1111-1111-1111-111111111111"
  id              = "22222222-2222-2222-2222-222222222222"
}

# Look up a server group member by name
data "viettelcloud_lb_member" "by_name" {
  server_group_id = "11111111-1111-1111-1111-111111111111"
  name            = "web-server-1"
}
