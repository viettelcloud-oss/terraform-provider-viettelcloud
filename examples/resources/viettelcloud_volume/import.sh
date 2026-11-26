# Import a volume by ID.
terraform import viettelcloud_volume.data 6f1a2c3d-4e5b-6789-abcd-ef0123456789

# The backend can report several origins for one volume, for example a snapshot
# of a volume that itself came from an image. Refreshing a volume reads state
# rather than configuration, so the intended origin has to arrive with the
# import ID: append the create_from.source_type the volume should be recorded
# with (empty, image, custom_image, snapshot, or backup).
terraform import viettelcloud_volume.data 6f1a2c3d-4e5b-6789-abcd-ef0123456789,snapshot

# A volume that has a snapshot or backup policy attached needs snapshot_policy
# or backup_policy in its configuration. Without that attribute, the next plan
# detaches the policy.
