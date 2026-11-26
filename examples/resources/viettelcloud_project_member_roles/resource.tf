# Owns every role of the member "alice". Roles assigned to alice outside
# Terraform are removed on the next apply.
resource "viettelcloud_project_member_roles" "example" {
  member = {
    username = "alice"
  }

  roles = [
    # enabled defaults to true.
    { role = { urn = "urn:role:prj-example:deployer" } },
    # Keep the role assigned without granting its permissions.
    { role = { urn = "urn:role:prj-example:auditor" }, enabled = false },
  ]
}

# Remove every role of the member "bob" while keeping bob in the project.
resource "viettelcloud_project_member_roles" "no_roles" {
  member = {
    username = "bob"
  }

  roles = []
}
