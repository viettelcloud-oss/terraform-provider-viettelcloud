package dbaascluster

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/netip"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/mapplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
	dbaasclustersdk "github.com/viettelcloud-oss/sdks/go/dbaascluster"
	projectsdk "github.com/viettelcloud-oss/sdks/go/project"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/compare"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/parse"
	dbaasclusterlookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/dbaascluster/lookup"
	projectlookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/project/lookup"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/replace"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/wait"
)

// dbClusterCreateTimeout, dbClusterDeleteTimeout and dbClusterPollInterval bound
// the waits on a cluster. A cluster provisions several nodes, so they are far
// longer than the waits on network objects. The backend waits about 52 minutes
// for the core to delete a cluster before it marks the cluster abnormal, so the
// delete wait covers that window.
const (
	dbClusterCreateTimeout = 60 * time.Minute
	dbClusterDeleteTimeout = 60 * time.Minute
	dbClusterPollInterval  = 15 * time.Second
)

var (
	_ resource.Resource                   = &DBClusterResource{}
	_ resource.ResourceWithConfigure      = &DBClusterResource{}
	_ resource.ResourceWithValidateConfig = &DBClusterResource{}
	_ resource.ResourceWithImportState    = &DBClusterResource{}
)

type DBClusterResource struct {
	client    *dbaasclustersdk.Client
	project   *projectsdk.Client
	projectID core.UUID
}

// clusterResolvers resolves the references of a plan to IDs. Tests replace
// them to keep request building offline.
type clusterResolvers struct {
	region          projectlookup.RegionResolveFunc
	zone            projectlookup.ZoneResolveFunc
	engineVersion   dbaasclusterlookup.EngineVersionResolveFunc
	resourcePackage dbaasclusterlookup.ResourcePackageResolveFunc
	storageClass    dbaasclusterlookup.StorageClassResolveFunc
}

func NewDBClusterResource() resource.Resource {
	return &DBClusterResource{}
}

func (r *DBClusterResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_dbc_cluster"
}

func (r *DBClusterResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manage a Viettel Cloud DB cluster. Supports the cassandra, memcached, mongodb, opensearch, starrocks and timescaledb engines. " +
			"Only description and keep_backup change in place. Changing any other argument replaces the cluster and deletes its data, " +
			"so protect a production cluster with `lifecycle { prevent_destroy = true }`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "DB cluster ID (UUID).",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:      true,
				Description:   "Name of the DB cluster. It is unique per project and engine.",
				PlanModifiers: []planmodifier.String{replace.String(compare.SameTrimmedString)},
			},
			"description": schema.StringAttribute{
				Optional:      true,
				Computed:      true,
				Description:   "Description of the DB cluster. Set it to an empty string to clear it.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"keep_backup": schema.BoolAttribute{
				Optional: true,
				Computed: true,
				Description: "Whether the backups of the cluster are kept when the cluster is deleted. " +
					"The backend defaults to true. The opensearch engine always deletes its backups, so it accepts false only.",
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
			"engine": schema.StringAttribute{
				Required:      true,
				Description:   "Database engine: cassandra, memcached, mongodb, opensearch, starrocks or timescaledb.",
				PlanModifiers: []planmodifier.String{replace.String(compare.SameTrimmedString)},
			},
			"engine_version": schema.SingleNestedAttribute{
				Required:      true,
				Description:   "Active engine version of the selected engine.",
				Attributes:    reference.Attributes(criterionVersion, "Exact engine version, e.g. 7.0."),
				PlanModifiers: []planmodifier.Object{replace.Reference(criterionVersion, compare.SameTrimmedString)},
			},
			"region": schema.SingleNestedAttribute{
				Required:      true,
				Description:   "Project region where the cluster is created.",
				Attributes:    reference.Attributes(reference.Name, "Region name."),
				PlanModifiers: []planmodifier.Object{replace.Reference(reference.Name, compare.SameTrimmedString)},
			},
			"zone": schema.SingleNestedAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Project zone of the cluster. Omit it for a multi-zone cluster.",
				Attributes:  reference.PreservedAttributes(reference.Name, "Exact zone name."),
				PlanModifiers: []planmodifier.Object{
					objectplanmodifier.UseStateForUnknown(),
					replace.ReferenceIfConfigured(reference.Name, compare.SameTrimmedString),
				},
			},
			"subnet": schema.SingleNestedAttribute{
				Required:      true,
				Description:   "Private subnet that the cluster connects to.",
				Attributes:    reference.Attributes(reference.ID, "Subnet ID (UUID)."),
				PlanModifiers: []planmodifier.Object{replace.Reference(reference.ID, compare.SameUUIDString)},
			},
			"topology": schema.StringAttribute{
				Optional: true,
				Computed: true,
				Description: "Topology of the cluster. Required for mongodb: mongo_rs or mongo_sharding. " +
					"Must be omitted for the other engines, which have a single topology.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
					replace.StringIfConfigured(compare.SameTrimmedString),
				},
			},
			"node_pools": schema.ListNestedAttribute{
				Required: true,
				Description: "Node pools of the cluster. mongodb takes exactly one pool, named mongo for mongo_rs and shard for mongo_sharding. " +
					"cassandra takes one pool named cassandra. memcached takes a pool named memcached, plus a pool named mcrouter when engine_config.mode is mcrouter. " +
					"starrocks takes two pools named fe and be. opensearch takes one or more pools, and timescaledb exactly one pool, with names of your choice.",
				PlanModifiers: []planmodifier.List{nodePoolsRequireReplace{}},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.StringAttribute{
							Computed:      true,
							Description:   "Node pool ID (UUID).",
							PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
						},
						"name": schema.StringAttribute{
							Required:    true,
							Description: "Name of the node pool.",
						},
						"role": schema.StringAttribute{
							Computed:      true,
							Description:   "Role that the backend gives the pool in the topology.",
							PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
						},
						"replicas": schema.Int64Attribute{
							Required:    true,
							Description: "Number of replicas in the pool.",
						},
						"partition_count": schema.Int64Attribute{
							Optional: true,
							Computed: true,
							Description: "Number of partitions. For the mongo_sharding topology this is the number of shards and is required. " +
								"The backend defaults it to 1 elsewhere.",
							PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
						},
						"node_count": schema.Int64Attribute{
							Computed:      true,
							Description:   "Number of nodes in the pool: replicas multiplied by partition_count.",
							PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
						},
						"resource_config": schema.SingleNestedAttribute{
							Required:    true,
							Description: "CPU and memory package of each node.",
							Attributes:  reference.Attributes(reference.Name, "Exact resource package name."),
						},
						"storage_class": schema.SingleNestedAttribute{
							Optional:    true,
							Description: "Storage class of the node volumes. Required for every engine except memcached, whose nodes have no volume and reject it.",
							Attributes:  reference.Attributes(reference.Name, "Exact storage class name."),
						},
						"disk_size": schema.Int64Attribute{
							Optional: true,
							Description: "Requested volume size of each node in GB. Required for every engine except memcached, whose nodes have no volume and reject it. " +
								"The backend can grow a volume past this size when enable_auto_increase_volume_size is true, so Terraform keeps your value. " +
								"Read actual_disk_size for the size in use. A size above the volume in use replaces the cluster. " +
								"A size that is not larger is recorded without changing the volume, because a volume never shrinks.",
						},
						"actual_disk_size": schema.Int64Attribute{
							Computed:    true,
							Description: "Volume size of each node in GB as the backend reports it.",
						},
						"used_disk_size": schema.Int64Attribute{
							Computed:    true,
							Description: "Used volume space of each node as the backend reports it.",
						},
						"engine_roles": schema.SetAttribute{
							ElementType:   types.StringType,
							Optional:      true,
							Computed:      true,
							Description:   "Engine roles of the pool. opensearch only: data, cluster_manager, ingest or coordinating.",
							PlanModifiers: []planmodifier.Set{setplanmodifier.UseStateForUnknown()},
						},
						"pool_config": schema.SingleNestedAttribute{
							Optional:      true,
							Computed:      true,
							Description:   "Engine-specific settings of the pool. opensearch only.",
							PlanModifiers: []planmodifier.Object{objectplanmodifier.UseStateForUnknown()},
							Attributes: map[string]schema.Attribute{
								"attribute": schema.StringAttribute{
									Optional:      true,
									Computed:      true,
									Description:   "Storage tier of the pool: hot, warm or cold.",
									PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
								},
							},
						},
					},
				},
			},
			"root_password": schema.StringAttribute{
				Required:  true,
				Sensitive: true,
				WriteOnly: true,
				Description: "Password of the administrator account. It is used only when the cluster is created. " +
					"Terraform never stores it in the plan or the state, so changing it later has no effect and does not change the password of the cluster.",
			},
			"plugins": schema.SetNestedAttribute{
				Optional:      true,
				Description:   "Plugins to install. opensearch only.",
				PlanModifiers: []planmodifier.Set{pluginsRequireReplace()},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"name": schema.StringAttribute{Required: true, Description: "Plugin name."},
					},
				},
			},
			"allowed_cidrs": schema.SetAttribute{
				ElementType: types.StringType,
				Optional:    true,
				Computed:    true,
				Description: "IPv4 CIDR blocks that may connect to the cluster, such as 10.0.0.5/32. A bare address without a prefix length is rejected.",
				PlanModifiers: []planmodifier.Set{
					setplanmodifier.UseStateForUnknown(),
					allowedCIDRsRequireReplace(),
				},
			},
			"enable_public_access": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Whether the cluster is reachable from the internet. The backend defaults to false.",
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.UseStateForUnknown(),
					boolplanmodifier.RequiresReplaceIfConfigured(),
				},
			},
			"enable_ssl": schema.BoolAttribute{
				Optional: true,
				Computed: true,
				Description: "Whether connections use SSL. The backend defaults to false, " +
					"always enables it for the opensearch engine and the mongo_sharding topology, and always disables it for the cassandra and starrocks engines.",
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.UseStateForUnknown(),
					boolplanmodifier.RequiresReplaceIfConfigured(),
				},
			},
			"enable_auto_increase_volume_size": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Whether the backend grows node volumes automatically. The backend defaults to false.",
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.UseStateForUnknown(),
					boolplanmodifier.RequiresReplaceIfConfigured(),
				},
			},
			"enable_pitr_backup": schema.BoolAttribute{
				Optional: true,
				Computed: true,
				Description: "Whether point-in-time recovery is enabled. mongodb and timescaledb only, and it needs auto_backup_config with enable_auto_backup set to true. " +
					"The backend defaults to false.",
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.UseStateForUnknown(),
					boolplanmodifier.RequiresReplaceIfConfigured(),
				},
			},
			"enable_auto_backup": schema.BoolAttribute{
				Computed:      true,
				Description:   "Whether automatic backup is enabled. Set it with auto_backup_config.",
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
			"auto_backup_config": schema.SingleNestedAttribute{
				Optional:      true,
				Description:   "Automatic backup schedule. When omitted, the cluster has no schedule. The memcached and starrocks engines have no backups and reject it.",
				PlanModifiers: []planmodifier.Object{autoBackupRequiresReplace()},
				Attributes: map[string]schema.Attribute{
					"enable_auto_backup": schema.BoolAttribute{Required: true, Description: "Whether automatic backup is enabled."},
					"backup_interval":    schema.StringAttribute{Required: true, Description: "Backup interval: daily or weekly."},
					"day_of_week": schema.StringAttribute{
						Optional:    true,
						Description: "Lowercase day name, such as monday. Required for weekly backups and rejected for daily backups.",
					},
					"start_hour": schema.Int64Attribute{Required: true, Description: "Hour of day that the backup starts, 0 to 23, in backend time."},
					"max_auto_backup": schema.Int64Attribute{
						Optional:    true,
						Description: "Number of automatic backups to keep. Required for every engine with backups except timescaledb, which rejects it.",
					},
					"retention_days": schema.Int64Attribute{
						Optional:    true,
						Description: "Number of days to keep automatic backups. Required for timescaledb and rejected for the other engines.",
					},
				},
			},
			"maintenance_config": schema.SingleNestedAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Weekly maintenance window. The backend defaults to Sunday at hour 0.",
				// Each child replaces the cluster only when it is configured. On
				// the object, a replace modifier would compare a partly configured
				// window, whose omitted children are still unknown, and replace
				// the cluster on every update.
				PlanModifiers: []planmodifier.Object{objectplanmodifier.UseStateForUnknown()},
				Attributes: map[string]schema.Attribute{
					"day_of_week": schema.StringAttribute{
						Optional:      true,
						Computed:      true,
						Description:   "Lowercase day name, such as sunday.",
						PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown(), replace.StringIfConfigured(compare.SameTrimmedString)},
					},
					"start_hour": schema.Int64Attribute{
						Optional:      true,
						Computed:      true,
						Description:   "Hour of day that maintenance starts, 0 to 23, in backend time.",
						PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown(), int64planmodifier.RequiresReplaceIfConfigured()},
					},
				},
			},
			"engine_config": schema.SingleNestedAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Engine settings. memcached only; the other engines have no settings and reject it.",
				// Each child replaces the cluster only when it is configured, for
				// the reason given on maintenance_config.
				PlanModifiers: []planmodifier.Object{objectplanmodifier.UseStateForUnknown()},
				Attributes: map[string]schema.Attribute{
					"mode": schema.StringAttribute{
						Optional:      true,
						Computed:      true,
						Description:   "Memcached mode: direct or mcrouter. The backend defaults to direct.",
						PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown(), replace.StringIfConfigured(compare.SameTrimmedString)},
					},
					"auth_enabled": schema.BoolAttribute{
						Optional:      true,
						Computed:      true,
						Description:   "Whether clients must authenticate. The backend defaults to false, and the mcrouter mode does not support it.",
						PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown(), boolplanmodifier.RequiresReplaceIfConfigured()},
					},
					"mcrouter_routing_type": schema.StringAttribute{
						Optional:      true,
						Computed:      true,
						Description:   "Routing of the mcrouter mode: sharded or replicated. Required for the mcrouter mode and rejected otherwise.",
						PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown(), replace.StringIfConfigured(compare.SameTrimmedString)},
					},
				},
			},
			"metadata": schema.MapAttribute{
				ElementType: types.StringType,
				Optional:    true,
				Computed:    true,
				Description: "Free-form string metadata of the cluster.",
				PlanModifiers: []planmodifier.Map{
					mapplanmodifier.UseStateForUnknown(),
					mapplanmodifier.RequiresReplaceIfConfigured(),
				},
			},
			"admin_username": schema.StringAttribute{
				Computed:      true,
				Description:   "Username of the administrator account.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"endpoint": schema.MapAttribute{
				ElementType: types.StringType,
				Computed:    true,
				Description: "Connection endpoint that the backend reports. A value that is not a string is encoded as JSON.",
			},
			"status": schema.StringAttribute{
				Computed:    true,
				Description: "Status of the cluster, such as active.",
			},
			"operational_status": schema.StringAttribute{
				Computed:    true,
				Description: "Operation that the backend is running on the cluster, or noop.",
			},
			"error_message": schema.StringAttribute{
				Computed:    true,
				Description: "Error message of the last failed operation.",
			},
			"created_at": schema.StringAttribute{
				Computed:      true,
				Description:   "Timestamp when the cluster was created (RFC3339).",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"updated_at": schema.StringAttribute{
				Computed:    true,
				Description: "Timestamp when the cluster was last updated (RFC3339).",
			},
		},
	}
}

func (r *DBClusterResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	data, ok := req.ProviderData.(*providerdata.Configured)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data type", fmt.Sprintf("Expected *providerdata.Configured, got %T", req.ProviderData))
		return
	}
	r.client = data.DBaaSCluster
	r.project = data.Project
	r.projectID = data.ProjectID
}

func (r *DBClusterResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config DBClusterResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	engineCfg, diags := engineConfigOf(ctx, config)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(rejectBlankNames(config, engineCfg)...)
	resp.Diagnostics.Append(rejectBlankReferences(config, engineCfg)...)
	resp.Diagnostics.Append(validateSubnetID(config.Subnet)...)
	resp.Diagnostics.Append(validateAllowedCIDRs(config.AllowedCIDRs)...)
	if config.Engine.IsNull() || config.Engine.IsUnknown() {
		return
	}

	selected, ok := driverFor(config.Engine)
	if !ok {
		resp.Diagnostics.AddAttributeError(
			path.Root("engine"),
			"Unsupported engine",
			fmt.Sprintf("engine must be one of %s, got %q.", strings.Join(supportedEngines(), ", "), config.Engine.ValueString()),
		)
		return
	}
	resp.Diagnostics.Append(selected.validate(engineCfg)...)
}

func (r *DBClusterResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan DBClusterResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	// A write-only attribute is null in the plan, so read it from the config.
	var password types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("root_password"), &password)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !configured(password) || password.ValueString() == "" {
		resp.Diagnostics.AddAttributeError(path.Root("root_password"), "Missing root password", "root_password must be a known, non-empty value.")
		return
	}

	selected, ok := driverFor(plan.Engine)
	if !ok {
		resp.Diagnostics.AddAttributeError(path.Root("engine"), "Unsupported engine", fmt.Sprintf("engine must be one of %s.", strings.Join(supportedEngines(), ", ")))
		return
	}
	engineCfg, diags := engineConfigOf(ctx, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	// ValidateConfig skips the engine rules while engine is unknown, so apply
	// checks them again before any request.
	resp.Diagnostics.Append(selected.validate(engineCfg)...)
	if resp.Diagnostics.HasError() {
		return
	}

	refs, diags := resolveClusterRefs(ctx, plan, engineCfg, r.resolvers())
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	body, diags := buildDBClusterCreateBody(plan, password.ValueString(), engineCfg, refs, selected)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	created, err := r.client.CreateDbCluster(ctx, dbaasclustersdk.CreateDbClusterParams{ProjectID: r.projectID}, body)
	if err != nil {
		resp.Diagnostics.AddError("Error creating DB cluster", err.Error())
		return
	}

	// Record the ID before the wait, so that a cluster that fails to provision
	// stays in state, tainted, and Terraform can delete it.
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), created.Id.String())...)
	cluster, err := r.waitUntilActive(ctx, created.Id)
	if err != nil {
		resp.Diagnostics.AddError("Error waiting for DB cluster", err.Error())
		return
	}

	populateDBClusterResourceState(cluster, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *DBClusterResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state DBClusterResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	clusterID, diags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	cluster, err := r.get(ctx, clusterID)
	if err != nil {
		if errors.Is(err, dbaasclustersdk.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading DB cluster", err.Error())
		return
	}

	populateDBClusterResourceState(cluster, &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *DBClusterResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state DBClusterResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	clusterID, diags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	if body, changed := buildDBClusterUpdateBody(plan, state); changed {
		if _, err := r.client.UpdateDbCluster(ctx, clusterID, dbaasclustersdk.UpdateDbClusterParams{ProjectID: r.projectID}, body); err != nil {
			resp.Diagnostics.AddError("Error updating DB cluster", err.Error())
			return
		}
	}

	// Read the cluster back even when nothing was sent: a planned change makes
	// every Computed attribute unknown, and state may not hold unknown values.
	cluster, err := r.get(ctx, clusterID)
	if err != nil {
		resp.Diagnostics.AddError("Error reading DB cluster", err.Error())
		return
	}
	newState := plan
	populateDBClusterResourceState(cluster, &newState)
	resp.Diagnostics.Append(resp.State.Set(ctx, &newState)...)
}

func (r *DBClusterResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state DBClusterResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	clusterID, diags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.DeleteDbCluster(ctx, clusterID, dbaasclustersdk.DeleteDbClusterParams{ProjectID: r.projectID})
	if err != nil {
		if errors.Is(err, dbaasclustersdk.ErrNotFound) {
			return
		}
		resp.Diagnostics.AddError("Error deleting DB cluster", err.Error())
		return
	}
	if err := r.waitUntilDeleted(ctx, clusterID); err != nil {
		resp.Diagnostics.AddError("Error waiting for DB cluster deletion", err.Error())
	}
}

func (r *DBClusterResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func (r *DBClusterResource) get(ctx context.Context, id core.UUID) (*dbaasclustersdk.DBClusterDetailSchema, error) {
	return r.client.GetDbCluster(ctx, id, dbaasclustersdk.GetDbClusterParams{ProjectID: r.projectID})
}

func (r *DBClusterResource) resolvers() clusterResolvers {
	return clusterResolvers{
		region:          projectlookup.NewRegionFinder(r.project, r.projectID).Resolve,
		zone:            projectlookup.NewZoneFinder(r.project, r.projectID).Resolve,
		engineVersion:   dbaasclusterlookup.NewEngineVersionFinder(r.client).Resolve,
		resourcePackage: dbaasclusterlookup.NewResourcePackageFinder(r.client).Resolve,
		storageClass:    dbaasclusterlookup.NewStorageClassFinder(r.client).Resolve,
	}
}

// waitUntilActive waits until the cluster is active and idle. The status alone
// is not enough: a cluster reports active while an operation is still running.
func (r *DBClusterResource) waitUntilActive(ctx context.Context, id core.UUID) (*dbaasclustersdk.DBClusterDetailSchema, error) {
	w := wait.New[*dbaasclustersdk.DBClusterDetailSchema](dbClusterPollInterval, dbClusterCreateTimeout)
	cluster, err := w.WaitFor(ctx, func(ctx context.Context) (*dbaasclustersdk.DBClusterDetailSchema, error) {
		cluster, err := r.get(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("read DB cluster %s: %w", id, err)
		}
		ready, err := clusterReady(cluster)
		if err != nil {
			return nil, err
		}
		if !ready {
			return nil, fmt.Errorf("DB cluster %s is %s (operation %s): %w", id, cluster.Status, cluster.OperationalStatus, wait.ErrNotReady)
		}
		return cluster, nil
	})
	if err != nil {
		return nil, fmt.Errorf("waiting for DB cluster %s to become active: %w", id, err)
	}
	return cluster, nil
}

// waitUntilDeleted waits until the cluster is gone. When the core refuses the
// deletion, the backend sets the cluster abnormal and idle again, and the wait
// stops at once.
func (r *DBClusterResource) waitUntilDeleted(ctx context.Context, id core.UUID) error {
	w := wait.New[struct{}](dbClusterPollInterval, dbClusterDeleteTimeout)
	_, err := w.WaitFor(ctx, func(ctx context.Context) (struct{}, error) {
		cluster, err := r.get(ctx, id)
		if errors.Is(err, dbaasclustersdk.ErrNotFound) {
			return struct{}{}, nil
		}
		if err != nil {
			return struct{}{}, fmt.Errorf("read DB cluster %s: %w", id, err)
		}
		if cluster.Status == dbaasclustersdk.DBClusterStatusAbnormal && cluster.OperationalStatus == dbaasclustersdk.DBClusterOperationalStatusNoop {
			return struct{}{}, fmt.Errorf("DB cluster %s failed to delete: %s", id, statusReason(cluster))
		}
		return struct{}{}, fmt.Errorf("DB cluster %s is %s (operation %s): %w", id, cluster.Status, cluster.OperationalStatus, wait.ErrNotReady)
	})
	if err != nil {
		return fmt.Errorf("waiting for DB cluster %s deletion: %w", id, err)
	}
	return nil
}

// clusterReady reports whether the cluster is active and idle. It returns an
// error for a status that the backend does not leave without intervention.
func clusterReady(cluster *dbaasclustersdk.DBClusterDetailSchema) (bool, error) {
	if cluster.Status == dbaasclustersdk.DBClusterStatusError || cluster.Status == dbaasclustersdk.DBClusterStatusAbnormal {
		return false, fmt.Errorf("DB cluster %s entered status %s: %s", cluster.Id, cluster.Status, statusReason(cluster))
	}
	return cluster.Status == dbaasclustersdk.DBClusterStatusActive &&
		cluster.OperationalStatus == dbaasclustersdk.DBClusterOperationalStatusNoop, nil
}

// statusReason returns the error message of a failed cluster. The backend
// leaves it empty when its own retries run out.
func statusReason(cluster *dbaasclustersdk.DBClusterDetailSchema) string {
	if cluster.ErrorMessage == "" {
		return "the backend reported no error message"
	}
	return cluster.ErrorMessage
}

// resolveClusterRefs resolves every reference of a plan to an ID.
func resolveClusterRefs(
	ctx context.Context,
	plan DBClusterResourceModel,
	cfg engineConfig,
	resolve clusterResolvers,
) (resolvedRefs, diag.Diagnostics) {
	var diags diag.Diagnostics
	var refs resolvedRefs

	regionName := reference.Get(plan.Region, reference.Name)
	if !configured(regionName) || strings.TrimSpace(regionName.ValueString()) == "" {
		diags.AddAttributeError(path.Root("region"), "Missing region", "region.name must be configured and known.")
		return refs, diags
	}
	name := strings.TrimSpace(regionName.ValueString())
	region, err := resolve.region(ctx, projectlookup.RegionFilter{Name: &name})
	if err != nil {
		diags.AddAttributeError(path.Root("region"), "Unable to resolve region", "region.name: "+err.Error())
		return refs, diags
	}
	refs.RegionID = region.Region.Id

	if zoneName := reference.Get(plan.Zone, reference.Name); configured(zoneName) {
		value := strings.TrimSpace(zoneName.ValueString())
		zone, err := resolve.zone(ctx, projectlookup.ZoneFilter{Name: &value})
		if err != nil {
			diags.AddAttributeError(path.Root("zone"), "Unable to resolve zone", "zone.name: "+err.Error())
			return refs, diags
		}
		refs.ZoneID = &zone.Id
	}

	subnetID, subnetDiags := parse.UUIDString(reference.Get(plan.Subnet, reference.ID), "subnet.id")
	diags.Append(subnetDiags...)
	if diags.HasError() {
		return refs, diags
	}
	refs.SubnetID = subnetID

	version := reference.Get(plan.EngineVersion, criterionVersion)
	if !configured(version) || strings.TrimSpace(version.ValueString()) == "" {
		diags.AddAttributeError(path.Root("engine_version"), "Missing engine version", "engine_version.version must be configured and known.")
		return refs, diags
	}
	engine, versionValue, active := strings.TrimSpace(plan.Engine.ValueString()), strings.TrimSpace(version.ValueString()), true
	// A new cluster needs an active version. An existing cluster keeps its
	// version after the catalog deactivates it, so only Create filters on this.
	engineVersion, err := resolve.engineVersion(ctx, dbaasclusterlookup.EngineVersionFilter{Engine: &engine, Version: &versionValue, IsActive: &active})
	if err != nil {
		diags.AddAttributeError(path.Root("engine_version"), "Unable to resolve engine version", "engine_version.version: "+err.Error())
		return refs, diags
	}
	refs.EngineVersionID = engineVersion.Id

	for i, pool := range cfg.Pools {
		poolPath := path.Root("node_pools").AtListIndex(i)
		resourceConfigName := attributeString(pool.ResourceConfig, reference.Name)
		if !configured(resourceConfigName) {
			diags.AddAttributeError(poolPath, "Missing node pool reference", "resource_config.name must be configured and known.")
			return refs, diags
		}
		resourceConfigValue := strings.TrimSpace(resourceConfigName.ValueString())
		resourcePackage, err := resolve.resourcePackage(ctx, dbaasclusterlookup.ResourcePackageFilter{Name: &resourceConfigValue})
		if err != nil {
			diags.AddAttributeError(poolPath.AtName("resource_config"), "Unable to resolve resource package", err.Error())
			return refs, diags
		}
		resolved := resolvedPool{ResourceConfigID: resourcePackage.Id}

		// A pool without storage, such as a memcached pool, has no storage
		// class. ValidateConfig decides which pools need one.
		if !pool.StorageClass.IsNull() {
			storageClassName := attributeString(pool.StorageClass, reference.Name)
			if !configured(storageClassName) {
				diags.AddAttributeError(poolPath, "Missing node pool reference", "storage_class.name must be configured and known.")
				return refs, diags
			}
			storageClassValue := strings.TrimSpace(storageClassName.ValueString())
			storageClass, err := resolve.storageClass(ctx, dbaasclusterlookup.StorageClassFilter{Name: &storageClassValue})
			if err != nil {
				diags.AddAttributeError(poolPath.AtName("storage_class"), "Unable to resolve storage class", err.Error())
				return refs, diags
			}
			resolved.StorageClassID = &storageClass.Id
		}
		refs.Pools = append(refs.Pools, resolved)
	}
	return refs, diags
}

// rejectBlankNames reports a cluster, node pool or plugin name that is empty
// or only whitespace. The request trims each name, and the API needs a
// non-empty one.
func rejectBlankNames(config DBClusterResourceModel, cfg engineConfig) diag.Diagnostics {
	var diags diag.Diagnostics
	check := func(value types.String, at path.Path) {
		if configured(value) && strings.TrimSpace(value.ValueString()) == "" {
			diags.AddAttributeError(at, "Empty name", at.String()+" must not be empty or only whitespace.")
		}
	}
	check(config.Name, path.Root("name"))
	for i, pool := range cfg.Pools {
		check(pool.Name, path.Root("node_pools").AtListIndex(i).AtName("name"))
	}
	if configured(cfg.Plugins) {
		for _, element := range cfg.Plugins.Elements() {
			check(pluginName(element), path.Root("plugins"))
		}
	}
	return diags
}

// rejectBlankReferences reports a reference name that is empty or only
// whitespace. The finders treat a blank name as no filter, so a blank name
// could select an unrelated catalog entry at apply.
func rejectBlankReferences(config DBClusterResourceModel, cfg engineConfig) diag.Diagnostics {
	var diags diag.Diagnostics
	check := func(value types.String, at path.Path) {
		if configured(value) && strings.TrimSpace(value.ValueString()) == "" {
			diags.AddAttributeError(at, "Empty reference name", at.String()+" must not be empty or only whitespace.")
		}
	}
	check(reference.Get(config.EngineVersion, criterionVersion), path.Root("engine_version").AtName(criterionVersion))
	check(reference.Get(config.Region, reference.Name), path.Root("region").AtName(reference.Name))
	check(reference.Get(config.Zone, reference.Name), path.Root("zone").AtName(reference.Name))
	for i, pool := range cfg.Pools {
		poolPath := path.Root("node_pools").AtListIndex(i)
		check(attributeString(pool.ResourceConfig, reference.Name), poolPath.AtName("resource_config").AtName(reference.Name))
		check(attributeString(pool.StorageClass, reference.Name), poolPath.AtName("storage_class").AtName(reference.Name))
	}
	return diags
}

// validateSubnetID reports a subnet.id that is not a UUID, so that the error
// comes at plan and not at apply.
func validateSubnetID(subnet types.Object) diag.Diagnostics {
	var diags diag.Diagnostics
	id := reference.Get(subnet, reference.ID)
	if !configured(id) {
		return diags
	}
	if _, err := parse.UUID(id.ValueString()); err != nil {
		diags.AddAttributeError(path.Root("subnet").AtName(reference.ID), "Invalid subnet ID", fmt.Sprintf("subnet.id must be a UUID, got %q.", id.ValueString()))
	}
	return diags
}

// validateAllowedCIDRs reports an allowed_cidrs entry that is not an IPv4
// prefix, and two entries that are equal once outer whitespace is removed.
// The backend writes a bare address such as 10.0.0.5 as 10.0.0.5/32, and the
// replace check would then replace the cluster after an import, because it
// compares the strings.
func validateAllowedCIDRs(cidrs types.Set) diag.Diagnostics {
	var diags diag.Diagnostics
	if !configured(cidrs) {
		return diags
	}
	seen := make(map[string]bool, len(cidrs.Elements()))
	for _, element := range cidrs.Elements() {
		cidr, ok := element.(types.String)
		if !ok || !configured(cidr) {
			continue
		}
		value := strings.TrimSpace(cidr.ValueString())
		if prefix, err := netip.ParsePrefix(value); err != nil || !prefix.Addr().Is4() {
			diags.AddAttributeError(
				path.Root("allowed_cidrs"),
				"Invalid allowed CIDR",
				fmt.Sprintf("allowed_cidrs must hold IPv4 CIDR blocks with a prefix length, such as 10.0.0.5/32, got %q.", cidr.ValueString()),
			)
			continue
		}
		if seen[value] {
			diags.AddAttributeError(
				path.Root("allowed_cidrs"),
				"Duplicate allowed CIDR",
				fmt.Sprintf("allowed_cidrs holds %q more than once after outer whitespace is removed. List each CIDR block once.", value),
			)
		}
		seen[value] = true
	}
	return diags
}

// buildDBClusterCreateBody builds the create body. It sends only configured
// optional values, so that the backend applies its own defaults, and it never
// sends an unknown value.
func buildDBClusterCreateBody(
	plan DBClusterResourceModel,
	password string,
	cfg engineConfig,
	refs resolvedRefs,
	selected driver,
) (dbaasclustersdk.DBClusterCreateSchema, diag.Diagnostics) {
	var diags diag.Diagnostics
	engine := dbaasclustersdk.DBClusterEngineType(strings.TrimSpace(plan.Engine.ValueString()))
	engineVersionID := refs.EngineVersionID
	body := dbaasclustersdk.DBClusterCreateSchema{
		Name:            strings.TrimSpace(plan.Name.ValueString()),
		RegionId:        refs.RegionID,
		ZoneId:          core.NullableFromPtr(refs.ZoneID),
		SubnetId:        refs.SubnetID,
		Engine:          core.NullableFromPtr(&engine),
		EngineVersionId: core.NullableFromPtr(&engineVersionID),
		// The password is sent untrimmed, because its whitespace is part of
		// the secret.
		RootPassword: &password,
	}

	if configured(plan.Description) {
		description := strings.TrimSpace(plan.Description.ValueString())
		body.Description = &description
	}
	if configured(plan.KeepBackup) {
		keepBackup := plan.KeepBackup.ValueBool()
		body.KeepBackup = &keepBackup
	}
	body.EnablePublicAccess = boolPointer(plan.EnablePublicAccess)
	body.EnableSsl = boolPointer(plan.EnableSSL)
	body.EnablePitrBackup = boolPointer(plan.EnablePITRBackup)
	body.EnableAutoIncreaseVolumeSize = boolPointer(plan.EnableAutoIncreaseVolumeSize)

	if configured(plan.AllowedCIDRs) {
		cidrs := make([]string, 0, len(plan.AllowedCIDRs.Elements()))
		for _, element := range plan.AllowedCIDRs.Elements() {
			if cidr, ok := element.(types.String); ok && configured(cidr) {
				cidrs = append(cidrs, strings.TrimSpace(cidr.ValueString()))
			}
		}
		body.AllowedCidrs = &cidrs
	}
	// Metadata values are free-form user data whose whitespace can be
	// meaningful, so they are sent as configured and not trimmed.
	if configured(plan.Metadata) {
		metadata := make(map[string]any, len(plan.Metadata.Elements()))
		for key, element := range plan.Metadata.Elements() {
			if value, ok := element.(types.String); ok && configured(value) {
				metadata[key] = value.ValueString()
			}
		}
		body.Metadata = &metadata
	}
	body.MaintenanceConfig = maintenanceBody(plan.MaintenanceConfig)

	autoBackup, autoBackupDiags := autoBackupBody(plan.AutoBackupConfig)
	diags.Append(autoBackupDiags...)
	body.AutoBackupConfig = core.NullableFromPtr(autoBackup)

	diags.Append(selected.fillCreateBody(engineCreateInput{Config: cfg, Refs: refs}, &body)...)
	return body, diags
}

// buildDBClusterUpdateBody builds the PATCH body from the attributes that
// changed. The second result is false when nothing changed, and the caller then
// skips the request. An unknown planned value means that the attribute was
// omitted, and omission keeps the current value. A description change in
// outer whitespace only is not a change, because the request trims it.
func buildDBClusterUpdateBody(plan, state DBClusterResourceModel) (dbaasclustersdk.DBClusterPartialUpdateSchema, bool) {
	var body dbaasclustersdk.DBClusterPartialUpdateSchema
	changed := false
	if configured(plan.Description) && !compare.SameTrimmed(state.Description, plan.Description.ValueString()) {
		description := strings.TrimSpace(plan.Description.ValueString())
		body.Description = &description
		changed = true
	}
	if configured(plan.KeepBackup) && !plan.KeepBackup.Equal(state.KeepBackup) {
		keepBackup := plan.KeepBackup.ValueBool()
		body.KeepBackup = &keepBackup
		changed = true
	}
	return body, changed
}

// maintenanceBody returns the configured part of the maintenance window, or nil
// when nothing is configured.
func maintenanceBody(config types.Object) *dbaasclustersdk.MaintenanceConfig {
	if !configured(config) {
		return nil
	}
	var body dbaasclustersdk.MaintenanceConfig
	set := false
	if day := attributeString(config, "day_of_week"); configured(day) {
		dayOfWeek := dbaasclustersdk.DayOfWeek(strings.TrimSpace(day.ValueString()))
		body.DayOfWeek = &dayOfWeek
		set = true
	}
	if hour, ok := config.Attributes()["start_hour"].(types.Int64); ok && configured(hour) {
		startHour := int(hour.ValueInt64())
		body.StartHour = &startHour
		set = true
	}
	if !set {
		return nil
	}
	return &body
}

// autoBackupBody builds the schedule union. The SDK helpers add the
// backup_interval discriminator.
func autoBackupBody(config types.Object) (*dbaasclustersdk.AutoBackupConfigSchema, diag.Diagnostics) {
	var diags diag.Diagnostics
	if !configured(config) {
		return nil, diags
	}
	attributes := config.Attributes()
	enabled, _ := attributes["enable_auto_backup"].(types.Bool)
	interval, _ := attributes["backup_interval"].(types.String)
	day, _ := attributes["day_of_week"].(types.String)
	startHour, _ := attributes["start_hour"].(types.Int64)
	maxBackups, _ := attributes[retentionMaxBackups].(types.Int64)
	days, _ := attributes[retentionDays].(types.Int64)

	// Each engine keeps backups by one of the two retention attributes, so only
	// the configured one is sent.
	maximum, retention, hour := intPointer(maxBackups), intPointer(days), int(startHour.ValueInt64())
	var schedule dbaasclustersdk.AutoBackupConfigSchema
	var err error
	switch strings.TrimSpace(interval.ValueString()) {
	case "weekly":
		err = schedule.FromWeeklyAutoBackupConfig(dbaasclustersdk.WeeklyAutoBackupConfig{
			DayOfWeek:        dbaasclustersdk.DayOfWeek(strings.TrimSpace(day.ValueString())),
			EnableAutoBackup: enabled.ValueBool(),
			MaxAutoBackup:    core.NullableFromPtr(maximum),
			RetentionDays:    core.NullableFromPtr(retention),
			StartHour:        hour,
		})
	case "daily":
		err = schedule.FromDailyAutoBackupConfig(dbaasclustersdk.DailyAutoBackupConfig{
			EnableAutoBackup: enabled.ValueBool(),
			MaxAutoBackup:    core.NullableFromPtr(maximum),
			RetentionDays:    core.NullableFromPtr(retention),
			StartHour:        hour,
		})
	default:
		diags.AddAttributeError(path.Root("auto_backup_config").AtName("backup_interval"), "Invalid backup interval", "backup_interval must be daily or weekly.")
		return nil, diags
	}
	if err != nil {
		diags.AddError("Unable to build the auto backup schedule", err.Error())
		return nil, diags
	}
	return &schedule, diags
}

// intPointer returns a pointer to a configured int, or nil.
func intPointer(value types.Int64) *int {
	if !configured(value) {
		return nil
	}
	result := int(value.ValueInt64())
	return &result
}

// boolPointer returns a pointer to a configured bool, or nil. Nil leaves the
// field out of the request so that the backend default applies.
func boolPointer(value types.Bool) *bool {
	if !configured(value) {
		return nil
	}
	result := value.ValueBool()
	return &result
}

const (
	spellingReplaceDescription           = "Changing the value replaces the resource. A change of outer whitespace only updates state."
	spellingReplaceConfiguredDescription = "Changing the configured value replaces the resource. A change of outer whitespace, or removing the value, only updates state."
)

// pluginsRequireReplace replaces the cluster when plugins names other plugins.
// pluginsResourceValue keeps plugin names that differ from the backend names
// only by outer whitespace, so such a change keeps the cluster.
func pluginsRequireReplace() planmodifier.Set {
	return setplanmodifier.RequiresReplaceIf(
		func(_ context.Context, req planmodifier.SetRequest, resp *setplanmodifier.RequiresReplaceIfFuncResponse) {
			resp.RequiresReplace = !sameTrimmedSet(req.PlanValue, req.StateValue, pluginName)
		},
		spellingReplaceDescription, spellingReplaceDescription,
	)
}

// allowedCIDRsRequireReplace replaces the cluster when configured
// allowed_cidrs name other CIDR blocks, as RequiresReplaceIfConfigured does.
// State keeps an entry that differs from the backend entry only by outer
// whitespace, so such a change keeps the cluster.
func allowedCIDRsRequireReplace() planmodifier.Set {
	return setplanmodifier.RequiresReplaceIf(
		func(_ context.Context, req planmodifier.SetRequest, resp *setplanmodifier.RequiresReplaceIfFuncResponse) {
			resp.RequiresReplace = !req.ConfigValue.IsNull() && !sameTrimmedSet(req.PlanValue, req.StateValue, stringElement)
		},
		spellingReplaceConfiguredDescription, spellingReplaceConfiguredDescription,
	)
}

// autoBackupRequiresReplace replaces the cluster when the backup schedule
// changes. State keeps a backup_interval or day_of_week that differs from the
// backend value only by outer whitespace, so such a change keeps the cluster.
func autoBackupRequiresReplace() planmodifier.Object {
	return objectplanmodifier.RequiresReplaceIf(
		func(_ context.Context, req planmodifier.ObjectRequest, resp *objectplanmodifier.RequiresReplaceIfFuncResponse) {
			resp.RequiresReplace = !sameSchedule(req.PlanValue, req.StateValue)
		},
		spellingReplaceDescription, spellingReplaceDescription,
	)
}

// sameSchedule reports whether two known schedules are equal once outer
// whitespace is removed from their strings.
func sameSchedule(plan, state types.Object) bool {
	if !configured(plan) || !configured(state) {
		return false
	}
	prior := state.Attributes()
	for name, value := range plan.Attributes() {
		if text, ok := value.(types.String); ok {
			priorText, _ := prior[name].(types.String)
			if !text.Equal(priorText) && !compare.SameTrimmedString(text, priorText) {
				return false
			}
			continue
		}
		if !value.Equal(prior[name]) {
			return false
		}
	}
	return true
}

// sameTrimmedSet reports whether two known sets hold the same strings once
// outer whitespace is removed. element reads the string of a set element.
func sameTrimmedSet(v, w types.Set, element func(attr.Value) types.String) bool {
	if !configured(v) || !configured(w) || len(v.Elements()) != len(w.Elements()) {
		return false
	}
	planned, ok := trimmedSet(v, element)
	if !ok {
		return false
	}
	prior, ok := trimmedSet(w, element)
	return ok && maps.Equal(planned, prior)
}

// trimmedSet returns the strings of a known set without outer whitespace. It
// returns false when an element is null or unknown.
func trimmedSet(set types.Set, element func(attr.Value) types.String) (map[string]bool, bool) {
	values := make(map[string]bool, len(set.Elements()))
	for _, value := range set.Elements() {
		text := element(value)
		if !configured(text) {
			return nil, false
		}
		values[strings.TrimSpace(text.ValueString())] = true
	}
	return values, true
}

// stringElement reads an element of a string set.
func stringElement(value attr.Value) types.String {
	text, _ := value.(types.String)
	return text
}

// pluginName reads the name of a plugins element.
func pluginName(value attr.Value) types.String {
	object, _ := value.(types.Object)
	return attributeString(object, "name")
}
