# Import a DB cluster by ID. The configuration must declare every required
# argument, including root_password, which is write-only and so is never read
# back from the cluster.
terraform import viettelcloud_dbc_cluster.mongodb 6f1a2c3d-4e5b-6789-abcd-ef0123456789

# An OpenSearch cluster reports its node pools sorted by name. List the pools
# in that order in the configuration to import it without a state-only update.
# Import also records the volume size in use for each pool. A disk_size that is
# not larger than that size never replaces the cluster.
