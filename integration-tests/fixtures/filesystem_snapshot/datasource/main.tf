# Creates one snapshot of an existing filesystem and resolves it through the
# direct ID path, the name and filesystem_id criteria, and every criterion in a
# non-canonical spelling.
terraform {
  required_version = ">= 1.16"
  required_providers {
    viettelcloud = { source = "viettelcloud-oss/viettelcloud" }
  }
}

variable "name" { type = string }
variable "filesystem_id" { type = string }

resource "viettelcloud_fs_filesystem_snapshot" "subject" {
  name        = var.name
  description = "Filesystem snapshot data-source integration subject"
  filesystem  = { id = var.filesystem_id }
}

data "viettelcloud_fs_filesystem_snapshot" "by_id" {
  id = viettelcloud_fs_filesystem_snapshot.subject.id
}

data "viettelcloud_fs_filesystem_snapshot" "by_name_and_filesystem" {
  name          = viettelcloud_fs_filesystem_snapshot.subject.name
  filesystem_id = viettelcloud_fs_filesystem_snapshot.subject.filesystem.id
}

data "viettelcloud_fs_filesystem_snapshot" "by_all_filters" {
  id            = upper(viettelcloud_fs_filesystem_snapshot.subject.id)
  name          = " ${viettelcloud_fs_filesystem_snapshot.subject.name} "
  filesystem_id = upper(viettelcloud_fs_filesystem_snapshot.subject.filesystem.id)
}

output "subject" { value = viettelcloud_fs_filesystem_snapshot.subject }
output "by_id" { value = data.viettelcloud_fs_filesystem_snapshot.by_id }
output "by_name_and_filesystem" { value = data.viettelcloud_fs_filesystem_snapshot.by_name_and_filesystem }
output "by_all_filters" { value = data.viettelcloud_fs_filesystem_snapshot.by_all_filters }
