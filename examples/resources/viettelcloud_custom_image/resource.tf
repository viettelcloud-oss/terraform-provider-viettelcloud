variable "source_volume" {
  type        = string
  default     = null
  description = "Name of an available bootable volume in the provider project."
}
variable "source_volume_id" {
  type        = string
  default     = null
  description = "UUID of an available bootable volume in the provider project. Set exactly one source variable."
}
resource "viettelcloud_custom_image" "example" {
  name = "my-custom-image"
  volume = {
    name = var.source_volume
    id   = var.source_volume_id
  }
  disk_format = "qcow2"
  description = "Reusable boot image"
  os          = "Linux"
  version     = "1"
  state       = "enabled"
  metadata    = jsonencode({ managed_by = "terraform" })
}
