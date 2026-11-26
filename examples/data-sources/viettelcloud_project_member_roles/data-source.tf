# Look up the roles of a project member by exact username.
data "viettelcloud_project_member_roles" "example" {
  username = "alice"
}

output "alice_role_urns" {
  value = [for assignment in data.viettelcloud_project_member_roles.example.assignments : assignment.urn]
}
