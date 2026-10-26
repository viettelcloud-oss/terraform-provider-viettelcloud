variable "region" {
  type        = string
  description = "Project region used by the Placement Group."
  default     = "vn-central"
}

variable "replacement_region" {
  type        = string
  description = "Project region used to demonstrate replacement when the region attribute changes."
  default     = "vn-central"
}

resource "viettelcloud_placement_group" "example" {
  name        = "my-placement-group"
  description = "Example placement group"
  policy      = "affinity"
  region = {
    name = var.region
  }
}

# Changing region forces a new Placement Group.
resource "viettelcloud_placement_group" "replacement" {
  name        = "my-placement-group-replacement"
  description = "Replacement placement group after region change"
  policy      = "affinity"
  region = {
    name = var.replacement_region
  }
}
