# Terraform integration tests

These tests create real resources on Viettel Cloud using `terraform test`.
Each resource and data source has separate test files and fixtures so they can
be reviewed, run, and updated independently.

## Structure

```text
integration-tests/
├── tests/
│   ├── vpc_resource.tftest.hcl
│   ├── vpc_data_source.tftest.hcl
│   ├── <entity>_resource.tftest.hcl
│   └── <entity>_data_source.tftest.hcl
├── fixtures/
│   ├── vpc/
│   │   ├── resource/main.tf
│   │   └── datasource/main.tf
│   └── <entity>/
│       ├── resource/main.tf
│       └── datasource/main.tf
├── provider.tf
└── terraform.tfvars
```

- `tests/<entity>_<target>.tftest.hcl` contains the lifecycle steps, queries, and
  assertions. `provider.tf` configures the provider from the shared connection
  input variables.
- `fixtures/<entity>/<target>` is an independent Terraform module loaded through
  `module.source` by the test file.
- `fixtures/<entity>/source` creates prerequisite resources in a separate test
  state. Terraform removes these resources after their dependent resources.
- `fixtures/<entity>/import` is a separate module holding a config-driven
  `import` block, used by the test files that cover an import path. It is
  separate because `terraform test` has no import command and an `import` block
  cannot be switched off by a variable.
- `provider.tf` declares the provider requirement and shared connection input
  variables. A variable that only one test file needs is declared inside that
  `.tftest.hcl` file instead, so the remaining files stay runnable without it.
- `terraform.tfvars` contains connection details and must not be committed.
  `terraform test` auto-loads it for the variables declared in `provider.tf` and
  for those declared inside a test file.
- Runs that load the same fixture within a test file share state. Terraform
  uses this state to update or replace the resource created by an earlier run.
- A run can opt out of that shared state with `state_key`, and runs in different
  state keys can execute at the same time with `parallel = true`. A failing run
  only skips the remaining runs of its own state key.
- Terraform destroys resources in the test state after the test file finishes.

## Test coverage

| Target | Coverage |
| --- | --- |
| Custom Image Resource | Create by volume name and ID, refresh, in-place updates, omit and clear, stable plans after omission, normalization, import, and teardown. Replacement covers disk format, volume name, and volume ID. Container-format replacement is N/A because the SDK supports only `bare`. Non-empty sharing requires an additional project UUID. |
| Custom Image Data Source | Independent volume and image setup, direct ID, name, volume name and ID, region, OS, version, state, status, combined filters, full state mapping, and configured-value preservation |
| DB Cluster Resource | Create and refresh of MongoDB replica set, OpenSearch, Cassandra, Memcached, StarRocks, and TimescaleDB clusters, the write-only password, weekly and daily backup schedules, backup retention by count and by days, PITR on MongoDB and TimescaleDB, configured pool order against the backend order, the values the backend forces on OpenSearch, Cassandra, and StarRocks, and Memcached engine settings and pools without storage. In-place update of `description` and `keep_backup`. Omit and clear of `description`, and omission of the attributes that replace only when configured. Replacement of `name`, `engine`, `subnet`, `allowed_cidrs`, `enable_public_access`, `enable_ssl`, `enable_auto_increase_volume_size`, `enable_pitr_backup`, `maintenance_config`, `metadata`, `auto_backup_config`, `engine_config`, and the node pool `name`, pool count, `disk_size`, `replicas`, `resource_config`, `engine_roles`, and `pool_config`. Import and refresh, and teardown. Replacement of `engine_version`, `zone`, `region`, and `storage_class` is N/A until the test project offers a second engine version, zone, region, or storage class. `topology` and `partition_count` replacement are N/A because a `mongo_sharding` cluster needs more quota. `plugins` is N/A until the catalog offers an active OpenSearch plugin. `enable_ssl` replacement on OpenSearch, Cassandra, and StarRocks is N/A because the backend forces the value. |
| DB Cluster Data Source | One isolated MongoDB cluster looked up by ID, by name with engine, by name with engine and region, and by name with engine and status; full state mapping and configured-value preservation |
| Elastic IP Resource | Create and refresh, update/omit/clear description, replace address family, and teardown |
| Elastic IP Data Source | Lookup by ID, IPv4/IPv6 address, status, region, availability, and combined filters; full state mapping and configured-value preservation |
| Key Pair Resource | Create and refresh, generated private-key state, rename and normalization, public-key replacement, whitespace-only public-key update in place, and omission preservation, plan-only import, and teardown |
| Key Pair Data Source | Lookup by ID, name, fingerprint, and combined filters; full state mapping and configured-value preservation |
| Load Balancer Server Group Resource | Create and refresh, sparse updates, omitted-value preservation with stable plans after omission, explicit description/H2/persistence clearing, H2 enabled on an HTTPS group with `enable_h2` omitted, nested normalization, monitor removal, protocol and parent replacement, plan-only import, and teardown. Removal from state after a group is deleted outside Terraform is N/A because a run cannot delete a group that another state owns; unit tests cover it. Disabling H2 on an HTTPS group is N/A because the provider rejects `enable_h2 = false` for HTTPS at plan time; unit tests cover it. |
| Load Balancer Server Group Data Source | Lookup by ID, name, and combined ID/name; full state mapping, configured-value preservation, and teardown |
| Load Balancer Server Group Member Resource | Create with provider defaults and refresh, in-place name/weight/backup update with a stable ID, name normalization without drift, omitted weight and backup restoring the defaults, address/port/server group replacement, the same address and port in two server groups, plan-only import, and teardown. The backend rejects weight zero, so unit tests cover that diagnostic. |
| Load Balancer Server Group Member Data Source | Lookup by direct ID, name, and ID plus name with normalized criteria; full state mapping and configured-value preservation |
| Private IP Resource | Allocated and configured IP/MAC values, allowed VIP lifecycle with stable plans after omission, description lifecycle, replacement, normalization, and teardown with an attachment |
| Private IP Data Source | Direct ID and representative subnet/VPC name, ID, CIDR, and region selector combinations; full state mapping and configured-value preservation |
| Placement Group Resource | Three parallel chains covering create and refresh, mutable and optional attributes, name and region normalization, policy replacement, plan-only import, and teardown. Region replacement is N/A until the test project provides a second region. |
| Security Group Resource | Create and refresh, description lifecycle, rename in place, omitted-description no-drift, plan-only import, and teardown. Region replacement is N/A until the test project provides a second region. |
| Security Group Data Source | Lookup by ID, name, region, default status, and combined filters; full state mapping and configured-value preservation |
| Security Group Rule Resource | A shared test-owned security group and five parallel rule chains covering create and refresh, update/omit/clear description, backend-assigned optional values, direction/protocol/port-range/remote-prefix replacement, enum normalization, plan-only import, and teardown. `security_group` replacement is N/A because moving a rule requires a second security group. |
| Server Resource | Create and refresh, mutable fields, network attachments, power, rebuild, resize, replacement, catalog selectors, import, and teardown that deletes server-owned volumes, private IPs, and elastic IPs. Data volumes cover new and existing attachments, including several existing volumes during creation. They cover reordering, addition, removal, switching, cleanup flags, omission, clearing during a server rename, and resize. Repeated plans preserve volume identity and backend IOPS. A data volume retype is N/A because the backend rejects a retype of an attached volume. Explicit IOPS updates lack live coverage because the volume type that the data-volume runs select fixes IOPS at one value, and an update needs a type that accepts an IOPS range. |
| Subnet Resource | Create and refresh, update/omit/clear description, normalization, CIDR/VPC replacement, and teardown |
| Subnet Data Source | Lookup by ID, name, CIDR, VPC ID/name, region, and combined filters; full state mapping and configured-value preservation |
| VPC Resource | Create and refresh, update/omit/clear description, CIDR replacement, and teardown |
| VPC Data Source | Lookup by ID, name, CIDR, region, and combined filters; full state mapping and configured-value preservation |
| VPC Peering Resource | Create and refresh of a same-project peering that is active at once, a stable replan after create, update of `description` in place, omit and clear of `description`, a stable replan after the clear, the empty default at create, `requester_vpc` and `accepter_vpc` replacement, plan-only import, and teardown. Create of a peering whose accepter VPC belongs to a second project (it stays `pending`) and a stable replan, in `tests/vpc_peering_cross_project_resource.tftest.hcl`. Removal from state after a peering is deleted outside Terraform is N/A because a run cannot delete it outside Terraform; unit tests cover it. Backend rejections and a `description` with outer whitespace (trimmed in the request, kept in state) are covered by unit tests only. Deletion of routes through the peering is N/A until `viettelcloud_route` exists. |
| VPC Peering Data Source | Lookup by ID, by requester and accepter VPC ID, by that pair with `status`, and by name; full state mapping and configured-value preservation. Skipping `rejected` and `error` peerings of the same pair is covered by unit tests only, because a same-project peering cannot reach those states. |
| Volume Resource | Concurrent create and scaling, configured-zone creation, zone omission preservation, snapshot-source zone omission, mutable attributes, normalization, external sources, resize, retype, image and volume type selectors, identity preservation when criteria change, plan-only import with name selectors, and stable plans, attach, switch, and detach of a snapshot policy in place, a teardown that deletes a volume with a policy, and plan-only import of an attached policy. For a backup policy: attaching, changing, and detaching it, removal of a policy that the configuration does not declare, attaching it again after that drift, a refresh that shows a different policy attached outside Terraform and an apply that restores the configured one, destroying a volume that has a policy while the policies remain, and plan-only import of a volume that has a policy. For both policies, rejection at create and replace and a policy from another region are N/A. Drift of a snapshot policy outside Terraform and a destroy that snapshots block are N/A too. Unit tests cover them, because they are provider diagnostics, API refusals, or a state that a run cannot set up. |
| Volume Data Source | Lookup by ID, name, size, status, bootable, zone, and combined filters; full state mapping and configured-value preservation |
| Volume Backup Resource | Create and refresh, rename, description omit and clear, name normalization, no-drift plan, source-volume replacement, plan-only import, and teardown. Restore into an existing detached volume, restore into another volume, a new spelling of the same restore volume without drift, and removal of `restore_volume`. Restore into a volume attached to a shut-down server. A failed restore and a restore on a new backup are covered by unit tests only, because the first needs a backend failure and the second is a plan-time diagnostic. A change of `timeouts` alone updates in place without drift. The `timeouts` durations and a restore that outlasts them are covered by unit tests only, because a run would need a restore longer than the timeout and `terraform test` cannot expect a provider error. |
| Volume Backup Data Source | Lookup by ID, name, source volume, status, size, region, and combined filters. Full state mapping and configured-value preservation. |
| Volume Backup Policy Resource | Create and refresh of a periodic policy with the default retention, stable plans, in-place update of name, status, retention, frequency, and schedule type, create with a days_of_month schedule, omit and clear of description, omission of retention, name, region, and time normalization, `region` replacement, plan-only import, and destroy. Removal from state after a deletion outside Terraform is N/A because a unit test covers it. |
| Volume Backup Policy Data Source | Create an isolated subject; look up by ID, by name and region, by name, region, and status, and by every criterion in a non-canonical spelling; verify full state mapping and configured-value preservation. Lookup errors are N/A because they are provider diagnostics; unit tests cover them. |
| Volume Snapshot Resource | Create and refresh, update/omit/clear description, rename and name normalization, source-volume replacement, stable plan, plan-only import, and teardown |
| Volume Snapshot Data Source | Lookup by ID, name, volume ID, zone, status, and combined filters; full state mapping and configured-value preservation |
| Volume Snapshot Policy Resource | Create and refresh from the required inputs, a stable plan, create with `description` and `retention`, omit and clear of both optional values, a `description` with spaces around it that keeps its configured spelling and a stable plan, in-place update of `name`, `status`, `retention`, and both `schedule` attributes that keeps the ID, `region` replacement, plan-only import, and destroy. Removal from state after a deletion outside Terraform is N/A because a unit test of `Read` not-found covers it. Schedule diagnostics and unsupported schedule types are N/A because they are provider diagnostics; unit tests cover them. |
| Volume Snapshot Policy Data Source | Create an isolated subject; look up by ID, by `name` and `region`, by `name`, `region`, and `status`, and by every criterion in a non-canonical spelling; verify full state mapping and configured-value preservation. Lookup errors are N/A because they are provider diagnostics; unit tests cover them. |
| DB Instance Resource | Create and refresh with required values, optional create values (description and backup policy), stable plan, in-place update of description, keep_backup, backup policy, and auto-increase, weekly-to-daily interval without day_of_week, omit and clear, name, disk_size, flavor, subnet, and enable_public_access replacement, plan-only import, and teardown. Region, DB version, and volume type replacement are N/A until the test project provides a second region and a second DB version and volume type in the catalog. database_type replacement is N/A, because `db_version` names a version of one database type and the suite has no catalog values for a second type. solution replacement is N/A until Test confirms that the test project accepts a `business` instance. Unit tests run the plan modifiers of every configurable attribute. Backend-driven disk growth (`current_disk_size` above `disk_size`), plan-time validation, and backend rejections are covered by unit tests only. |
| DB Instance Data Source | Independent network and DB instance setup, direct ID and exact name, full state mapping, and configured-value preservation |
| DB Backup Resource | Two shared source DB instances, create and refresh, stable plan, description update, omit and clear, name and db_instance replacement with create_before_destroy, plan-only import, a backup that remains after its instance is destroyed with keep_backup = true, and teardown. Backend rejections, failed creates and deletes, automatic-backup import, and plan-time validation are covered by unit tests only. |
| DB Backup Data Source | Independent network, DB instance, and backup setup; direct ID, the unique name, db_instance_id, and backup_type combination, status, database_type, and region filters; full state mapping and configured-value preservation |
| Load Balancer Resource | Create and refresh, in-place update of name, description, package, and topology, omit and clear description, backend default and omission of `type`, `public_ip_mode` `ipv4-only` at create and in place, a padded `public_ip_mode` that keeps its spelling at create and sends no request in place, omission of `public_ip_mode` turning public IP off in place, name and UUID normalization, VPC replacement, Private IP omission and reconfiguration, plan-only import with the derived `public_ip_mode`, and teardown. `public_ip_mode` `dual-stack` is N/A until the test infrastructure supports dual-stack public IP; unit tests cover it. Region replacement is N/A until the test project provides a second region. Private IP replacement is N/A until the test project has quota for a second Private IP. QoS policy assignment and replacement are N/A until the test project provides a QoS policy. `type` replacement is N/A until the test project provides a network for `native`. |
| Load Balancer Data Source | Create an isolated subject; look up by ID, name, region, private VIP address, package, topology, access log status, and combined filters; verify full state equality, configured-value preservation, and a stable repeated plan. The type filter runs only when the backend reports a type, and the run asserts which case applied. Public IPv4 and IPv6 filters have unit tests only because public IP configuration is outside the baseline contract. Mutation and import are N/A for a read-only data source. |
| L7 Policy Resource | Create and refresh of every redirect variant with the default code 302, in-place change of every ordered pair of actions with old targets cleared, position, description, and code changes, omission of description, position, and code with their values kept, explicit empty description, normalization of surrounding whitespace in name, description, redirect URL, and redirect prefix and of upper-case listener and server group IDs, stable plan, `listener` replacement, plan-only import, removal from state and re-creation after a second state deletes the policy, and deletion of every policy. |
| L7 Policy Data Source | Lookup by ID, name, untrimmed name, and the combinations of ID, name, and listener ID; full state mapping and configured-value preservation |
| L7 Rule Resource | Create and refresh of every rule type and compare type, in-place update of compare type, value, invert, key, and type, invert default after omission, key cleared when the type changes to one without key, stable plan, `l7_policy` replacement, plan-only import, and teardown. Clearing `key` on HEADER or COOKIE is N/A because those types require a key. External deletion is N/A because `terraform test` cannot delete a rule outside Terraform between runs. |
| L7 Rule Data Source | Direct lookup by L7 policy ID and rule ID, full state mapping, and configured-value preservation. Lookup by type, value, or key is N/A because the data source supports only the ID criterion. |
| Placement Group Data Source | Create an isolated subject; look up by ID, name, policy, region, and combined filters; verify full state equality and configured-value preservation |
| vDC Data Source | Lookup of an existing vDC by ID, by name with zone, by name with region, and by every criterion in a non-canonical spelling; full state mapping and configured-value preservation. Mutation, import, and cleanup are N/A because only an operator creates a vDC and the suite creates no infrastructure. Lookup errors, a null description, and vDC names that contain each other or repeat in another zone are N/A because they are provider diagnostics or need more vDCs in the test project; unit tests cover them. |
| Load Balancer Listener Resource | Create and refresh, create-time timeout and TERMINATED_HTTPS `enable_h2` defaults, mutable fields including `enable_h2` on HTTP, description and CIDR omit and clear, timeout/certificate/Boolean omission, explicit false values, TLS certificate, server group attachment and detachment, name normalization, stable plans, protocol/port replacement, plan-only import, and teardown. TERMINATED_HTTPS create without SNI certificates and SNI certificate clearing are N/A because the backend rejects an omitted or empty `sni_tls_cert_ids` on that protocol with HTTP 400 `invalid_input`. Detaching `default_tls_cert` is N/A: the backend rejects it on TERMINATED_HTTPS with HTTP 400, and the suite has no listener of another protocol with a certificate. `enable_h2 = false` on HTTPS and TERMINATED_HTTPS is N/A: the backend always stores `true` for these protocols, and the provider rejects `false` at plan time. Parent replacement is N/A until the test project provides a second load balancer. In-place change of `server_group` and `default_tls_cert` to another object is N/A until the test project provides a second server group and a second TLS certificate. |
| Load Balancer Listener Data Source | Create an isolated subject, then look up by ID, name, and combined filters. Full state mapping and configured-value preservation. |
| Member Roles Resource | Create and refresh with default and disabled assignments, stable plan, in-place addition and disabling that keeps assignment IDs, restore after an out-of-band change, omitted `enabled` defaulting to true, an empty role set, member replacement that clears the previous member, replacement of roles assigned before Terraform, a disabled role, a project admin, destroy that keeps the member and its admin flag, plan-only import, and teardown. Removal from state after a member leaves the project is N/A because Terraform cannot remove a project member. Error diagnostics are N/A because they are provider diagnostics; unit tests cover them. |
| Member Roles Data Source | Lookup by username, full state equality with a managed role set, and configured-value preservation. Lookup errors are N/A because they are provider diagnostics; unit tests cover them. |
| Custom Role Resource | Create from display name and actions with backend defaults and the derived name, stable plan, create with every optional attribute, in-place update of display name, description, enabled flag, and actions that keeps ID, name, and URN, omitted description kept and omitted `enabled` defaulting to true, cleared description and actions, a name derived from a Vietnamese display name, configured `display_name` and `description` with outer whitespace kept in state with a stable plan while the backend stores them trimmed, plan-only import, and teardown. Replacement is N/A because no attribute requires replacement. Removal from state after a role is deleted outside Terraform is N/A because a run cannot delete a role that another state owns; a unit test covers it. Error diagnostics, including deleting a role that a member holds, are N/A because they are provider diagnostics; unit tests cover them. |
| Custom Role Data Source | Lookup by ID, by exact name next to a role whose name contains it, and by ID and name together; full state equality and actions read from the role detail; a configured name with outer whitespace and an uppercase ID kept in state. Lookup errors are N/A because they are provider diagnostics; unit tests cover them. |
| Filesystem Resource | Create and refresh with minimal config, in-place update of name and description, description omit and clear, resize in-place, configured `name`, `description`, and `protocol` with outer whitespace and an upper-case `subnet.id` kept in state in place with a stable plan, protocol replacement, filesystem_type replacement, subnet replacement, plan-only import, and teardown. Creation from a snapshot with only `name` and `snapshot` configured, where `size`, `protocol`, `filesystem_type`, and `subnet` come from the source filesystem, resize in place after the size is added, and `snapshot.id` replacement, with a source filesystem and two snapshots that the suite creates; the provider and the backend must report the source snapshot. Source conflicts are N/A because they are provider diagnostics; unit tests cover them. Removal from state and re-creation after a second state imports and deletes the filesystem outside the creating state. |
| Filesystem Data Source | Direct ID lookup, name + zone lookup, and name + subnet_id lookup; full state mapping. Lookup errors are N/A because they are provider diagnostics; unit tests cover them. |
| Filesystem Snapshot Resource | Four parallel chains covering create and refresh, stable plan, in-place rename, description update, omit and clear, `filesystem` replacement, plan-only import, removal from state and re-creation after a deletion outside the creating state, and destroy. Name normalization is covered with an untrimmed name. |
| Filesystem Snapshot Data Source | Create an isolated subject; look up by ID, by name and `filesystem_id`, and by every criterion in a non-canonical spelling; verify full state mapping and configured-value preservation. Lookup errors are N/A because they are provider diagnostics; unit tests cover them. |
| Filesystem Snapshot Policy Resource | Five parallel chains covering create and refresh with the retention default of 1 that the provider sends, stable plans, in-place update of name, days of week, time, status, and retention, omit and clear of description, omission of retention, name, region, days-of-week, and time normalization, plan-only import, removal from state and re-creation after a deletion outside the creating state, and destroy. Region replacement is N/A until the test project provides a second region; a unit test covers the replacement plan. |
| Filesystem Snapshot Policy Data Source | Create an isolated subject; look up by ID, by name and region, and by every criterion in a non-canonical spelling; verify full state mapping and configured-value preservation. Lookup errors are N/A because they are provider diagnostics; unit tests cover them. |

The index and assertions in each `.tftest.hcl` file are the source of truth for
its exact chains, scenarios, inputs, and expected results.

## Prerequisites

The tests require Terraform 1.16 or newer to support `import` blocks in test
fixture modules. The tests also use `parallel` and `state_key` for independent
lifecycle chains. Use credentials for a dedicated test project and a
development override that points to the provider built from this repository.

Each test file declares its environment-specific catalog and resource inputs.
Before running it, ensure that the target project provides the referenced
regions, zones, images, volume types, and flavors, and has no resources that
would collide with the test fixtures. The selected `.tftest.hcl` file is the
source of truth for concrete values and compatibility constraints.

Every test file that creates a volume selects its volume type by name, encryption key size, and multiple-attachment support.
Make sure that each volume type selector matches exactly one enabled, up volume type in the test zone.
For server and volume selector tests, make sure that each selector matches exactly one catalog entry.
The selected images must fit the requested volume size.
The selector suites require distinct images and volume types for their replacement and rebuild cases.
The image name used for equivalent criteria must identify the image selected by those criteria.

The following test files require additional environment preparation:

- `tests/custom_image_resource.tftest.hcl` and `tests/custom_image_data_source.tftest.hcl`:
  - Quota for four source volumes and two custom images in the resource suite.
  - Quota for one source volume and one custom image in the data source suite.
  - Permission to create bootable volumes from catalog images and custom images from those volumes.
  - An additional project UUID and sharing permission when testing non-empty sharing.
- `tests/db_cluster_resource.tftest.hcl` and `tests/db_cluster_data_source.tftest.hcl`:
  - A test project whose catalog offers the engine versions, resource packages, and storage class that the files name in their variables.
  - Quota for seven DB clusters at once in the resource suite, and for one in the data source suite.
  - A `db_root_password` that satisfies the backend password policy.
- `tests/volume_resource.tftest.hcl`:
  - existing snapshot and backup objects referenced by `create_from.snapshot`
    and `create_from.backup`;
  - quota for up to nine volumes totaling at least 270 GiB, four volume
    snapshot policies, and two backup policies; and
  - the region that holds the test zone, in `snapshot_policy_region` and
    `backup_policy_region`.
- `tests/volume_snapshot_policy_resource.tftest.hcl`:
  - a second region of the project in `second_region`, for the `region`
    replacement chain.
- `tests/server_resource.tftest.hcl`:
  - quota for one VPC, up to 17 servers, and their dependent storage and
    networking resources, including four volumes for the attachment lifecycle.
- `tests/vpc_peering_resource.tftest.hcl` and `tests/vpc_peering_data_source.tftest.hcl`:
  - quota for five VPCs and two VPC peerings in the resource suite, and for
    two VPCs and one VPC peering in the data source suite; and
  - the network service active in the test region.
- `tests/vpc_peering_cross_project_resource.tftest.hcl`:
  - a second project with the network service active in the test region, and
    quota for one VPC there. Set `accepter_project_id` to its UUID, for example
    as `TF_VAR_accepter_project_id`. The token must be allowed to create and
    delete VPCs in that project; and
  - quota for one VPC and one VPC peering in the test project.
- `tests/volume_backup_resource.tftest.hcl`:
  - quota for five 30 GiB volumes and three backups; and
  - an existing unencrypted volume of at least 30 GiB in the test region,
    attached only to servers that are shut down. Set
    `restore_attached_volume_id` to its UUID. The suite restores a backup into
    it, which overwrites its data.
- `tests/volume_backup_data_source.tftest.hcl`:
  - quota for one 30 GiB volume and one backup.
- `tests/volume_backup_policy_resource.tftest.hcl`:
  - a second region of the project for the `region` replacement chain, and a `first_execution` variable that holds an RFC 3339 timestamp in UTC, on the hour, in the future when the run creates a policy.
- `tests/volume_snapshot_resource.tftest.hcl` and `tests/volume_snapshot_data_source.tftest.hcl`:
  - quota for five 30 GiB source volumes and three snapshots when both suites run at once.
- `tests/filesystem_snapshot_resource.tftest.hcl` and `tests/filesystem_snapshot_data_source.tftest.hcl`:
  - five existing filesystems in the test project, set through `filesystem_id`,
    `description_filesystem_id`, `drift_filesystem_id`, `import_filesystem_id`,
    and `replacement_filesystem_id`; and
  - quota for four snapshots at once in the resource suite and one in the data
    source suite.
- `tests/member_roles_resource.tftest.hcl` and `tests/member_roles_data_source.tftest.hcl`:
  - Terraform cannot add members to a project, so invite them beforehand. The
    test project needs IAM enabled and three human members other than the
    token's identity: two who are not project admins (`member_username` and
    `second_member_username`) and one project admin (`legacy_admin_username`).
    The data source suite needs only `member_username`;
  - two enabled predefined project roles (`predefined_role_urn` and
    `second_predefined_role_urn`) and a custom role of the test project whose
    `enabled` flag is false (`disabled_custom_role_urn`); and
  - members whose roles the tests may replace. The suites replace the roles of
    these members and leave them with no role, in the project, with their admin
    flag unchanged.
- `tests/custom_role_resource.tftest.hcl` and `tests/custom_role_data_source.tftest.hcl`:
  - two IAM actions that are allowed on projects (`action_urn` and
    `second_action_urn`).
- `tests/db_instance_resource.tftest.hcl`, `tests/db_instance_data_source.tftest.hcl`,
  `tests/db_backup_resource.tftest.hcl`, and
  `tests/db_backup_data_source.tftest.hcl`:
  - DBaaS enabled for the project in the test region, with the DB version,
    flavors, and volume type that each file names in its `variables` block;
  - a root password for the test DB instances in `db_root_password`, for
    example as `TF_VAR_db_root_password`;
  - quota for one VPC and subnet per file, a second subnet in
    `tests/db_instance_resource.tftest.hcl`, and up to two DB instances at a
    time in the resource suites;
  - a public endpoint for one DB instance in
    `tests/db_instance_resource.tftest.hcl`; and
  - a backend that reports DB instance and backup status through the v2 API
    event watchers.
- `tests/filesystem_resource.tftest.hcl` and `tests/filesystem_data_source.tftest.hcl`:
  - File Storage enabled for the project in the test zone.
  - Two existing subnets in the test zone. Set `subnet_id` and `subnet_id_b`,
    for example as `TF_VAR_subnet_id` and `TF_VAR_subnet_id_b`. The suites
    create no VPC or subnet.
  - At least two enabled NFS filesystem types in the test zone. Set
    `filesystem_type_name_b` (for the type replacement chain) as
    `TF_VAR_filesystem_type_name_b`. Set the first type as
    `filesystem_type_name`, for example as `TF_VAR_filesystem_type_name`.
  - An enabled SMB filesystem type in the test zone. Set
    `smb_filesystem_type_name` as `TF_VAR_smb_filesystem_type_name` for the
    protocol replacement chain.
  - Quota for up to seven filesystems and two filesystem snapshots at once (one
    filesystem per chain, plus the source filesystem and its two snapshots of
    the `from_snapshot` chain).
- `tests/load_balancer_resource.tftest.hcl`:
  - two existing VPCs in the test region, each with a subnet. Set `vpc_id`,
    `subnet_id` (a subnet of `vpc_id`), and `replacement_vpc_id`, for example
    as `TF_VAR_vpc_id`, `TF_VAR_subnet_id`, and `TF_VAR_replacement_vpc_id`.
    The suite creates no VPC or subnet; and
  - quota for up to three Load Balancers and one Private IP. A public IP has
    no separate quota: it comes with its Load Balancer, at most one each.
- `tests/load_balancer_data_source.tftest.hcl`:
  - one existing VPC in the test region. Set `vpc_id` to its ID, for example as `TF_VAR_vpc_id`.
    The suite creates no VPC or subnet; and
  - quota for one Load Balancer, and for four when this suite and `tests/load_balancer_resource.tftest.hcl` run at once.
- `tests/lb_server_group_resource.tftest.hcl`:
  - two existing load balancers that support the configured protocols and HTTP/2; and
  - quota for five server groups across the referenced load balancers.
- `tests/lb_server_group_data_source.tftest.hcl`:
  - an existing load balancer that supports the configured protocol; and
  - quota for one server group.
- `tests/lb_listener_resource.tftest.hcl` and `tests/lb_listener_data_source.tftest.hcl`:
  - existing load balancers with free listener ports and listener quota.
  - The resource suite also requires a server group and one TLS certificate in the test project.
- `tests/lb_member_resource.tftest.hcl` and `tests/lb_member_data_source.tftest.hcl`:
  - two existing, ACTIVE server groups, set in `lb_server_group_ids` in
    `terraform.tfvars`. The data source suite uses only the first group;
  - backend IP addresses that the server groups accept as members, set in
    `lb_member_addresses` and `lb_datasource_member_address` when the
    defaults do not fit the load balancer network; and
  - a member limit of at least three on the Load Balancer, or of at least
    two on each Load Balancer when the server groups belong to different
    Load Balancers. The package of a Load Balancer sets the limit for the
    members of all its server groups.
- `tests/l7_policy_resource.tftest.hcl` and `tests/l7_policy_data_source.tftest.hcl`:
  - an existing HTTP or TERMINATED_HTTPS listener of an ACTIVE Load Balancer
    whose type is not NATIVE. Set `listener_id`, for example as
    `TF_VAR_listener_id`. The suites create no Load Balancer or listener;
  - for the resource suite, a second such listener, other than `listener_id`,
    set as `replacement_listener_id`, and a server group of the Load Balancer
    that owns `listener_id`, set as `server_group_id`;
  - for the resource suite, a package limit that allows at least four L7
    policies on the Load Balancer that owns `listener_id`. The suite holds
    four policies there at one time. A create above the limit fails with
    `L7 Policy requested: <n>, limit: <limit>`; and
  - no other policy changes on `listener_id` while the resource suite runs.
    Do not run the two suites at the same time with the same `listener_id`,
    because a policy that the data-source suite adds can shift the positions
    that the resource suite asserts.
- `tests/l7_rule_resource.tftest.hcl` and `tests/l7_rule_data_source.tftest.hcl`:
  - an existing L7 policy on an HTTP listener of an ACTIVE Load Balancer. Set
    `l7_policy_id`, for example as `TF_VAR_l7_policy_id`. The suites create no
    Load Balancer, listener, or L7 policy; and
  - for the resource suite, a second existing L7 policy, other than
    `l7_policy_id`, set as `replacement_l7_policy_id`.
- `tests/vdc_data_source.tftest.hcl`:
  - an existing vDC that an operator assigned to the test project. Set
    `vdc_id` to its ID, for example as `TF_VAR_vdc_id`. Its name must be
    unique in its zone and in its region. The suite creates no infrastructure.

## Set up

Build the provider:

```bash
make build
```

Configure the Terraform CLI using the absolute path to this repository:

```hcl
provider_installation {
  dev_overrides {
    "registry.terraform.io/viettelcloud-oss/viettelcloud" = "/absolute/path/to/terraform-provider-viettelcloud"
  }

  direct {}
}
```

Create `integration-tests/terraform.tfvars`:

```hcl
token        = "..."
project_id   = "integration-test-project"
api_endpoint = "<api-endpoint>"
```

Add any test-specific variables declared by the selected `.tftest.hcl` file to
`terraform.tfvars`, or pass them on the command line with `-var`. Terraform
reports `Required variable not set` when a required input is missing.

`integration-tests/terraform.tfvars` is ignored by Git. Do not commit it or
expose its contents.

Set the provider address before running the tests:

```bash
export VIETTELCLOUD_PROVIDER_ADDRESS="registry.terraform.io/viettelcloud-oss/viettelcloud"
```

## Run the tests

Run the following commands from the repository root.

Initialize the integration-test configuration:

```bash
terraform -chdir=integration-tests init -backend=false
```

Run a single test file, for example the VPC resource test:

```bash
terraform -chdir=integration-tests test \
  -filter=tests/vpc_resource.tftest.hcl \
  -verbose
```

Run all integration tests:

```bash
terraform -chdir=integration-tests test -verbose
```

### Run part of a test file

The `-filter` flag selects whole test files. To run only the runs that a change
affects, run a copy of the test file. `tools/select-tftest-runs` writes that
copy as `tests/<entity>_<target>.selected.tftest.hcl` and prints its path.

For each run that you name, the copy keeps these runs:

- the named run;
- each earlier run with the same state, because those runs build the state
  that the named run starts from; and
- each run that a kept run references through `run.<name>`, together with the
  earlier runs in its state.

A run has the state of its `state_key`. A run without a `state_key` has the
state of its module source, or the root module state. Every block that is not
a run stays in the copy, together with the runs that those blocks reference.
Terraform destroys every state at the end of the copy, so the runs after a
named run are not necessary.

Run the commands from the repository root. The paths after `go -C tools` are
relative to the `tools` directory.

1. List the runs in the test file:

   ```bash
   go -C tools run ./select-tftest-runs \
     -list ../integration-tests/tests/server_resource.tftest.hcl
   ```

   Each line shows a run, its state, and the number of runs that the copy
   keeps when you name that run.

2. Write the copy, then initialize again. Terraform installs the modules of a
   test file only during `init`:

   ```bash
   filter=$(go -C tools run ./select-tftest-runs \
     -run planAgain_createdServer_stateStable \
     ../integration-tests/tests/server_resource.tftest.hcl)
   terraform -chdir=integration-tests init -backend=false
   ```

   To keep more runs, repeat `-run`. To run a whole chain, name its last run.

3. Run the copy, then delete it:

   ```bash
   terraform -chdir=integration-tests test -filter="$filter" -verbose
   rm "integration-tests/$filter"
   ```

Delete the copy before you run all integration tests, or Terraform also runs
the copy. Git ignores the copy.

## Interpret the results

When the test succeeds, every run reports `pass` and Terraform finishes with
`Success`:

```text
tests/vpc_resource.tftest.hcl... in progress
  run "createVpc_nameDescriptionCidrAndRegionConfigured_resourceAndDataSourceStateReturned"... pass
  run "changeNameAndDescription_existingVpc_vpcUpdatedInPlace"... pass
  run "omitDescription_descriptionSet_descriptionPreservedInPlace"... pass
  run "clearDescription_descriptionSet_descriptionEmptyInPlace"... pass
  run "changeCidr_existingVpc_vpcReplaced"... pass
tests/vpc_resource.tftest.hcl... tearing down
tests/vpc_resource.tftest.hcl... pass

Success! 5 passed, 0 failed.
```

- Exit code `0` with every run marked `pass` means the test and cleanup
  completed successfully.
- If a run reports `fail`, inspect the `Error` and `error_message` shown below
  that run.
- Runs after a failure can be marked `skip`; this is not a separate failure in
  those runs. In a file with parallel chains, such as the server resource test,
  only the failing run's own chain is skipped; the other chains keep running.
- Parallel runs report as they finish, so the server resource test prints its
  runs interleaved rather than in file order.
- If `tearing down` or cleanup reports an error, the test is not complete even
  if the earlier assertions passed.

These tests use real infrastructure and can consume quota or incur costs.
Terraform applies changes without asking for confirmation. Use a project
dedicated to testing.

If cleanup fails, inspect the test output and manually remove any remaining
resources before running the test again.

## Add a new test

1. Create an independent module at `fixtures/<entity>/<target>`.
2. Create `tests/<entity>_<target>.tftest.hcl` with the required lifecycle or
   lookup runs. Follow the
   [run naming and chain index rules](../CONTRIBUTING.md#real-api-integration-tests).
3. Assert primary attributes, cloud-generated IDs, and platform metadata.
4. Run the file with:
   ```bash
   terraform -chdir=integration-tests test -filter='tests/<entity>_<target>.tftest.hcl' -verbose
   ```
