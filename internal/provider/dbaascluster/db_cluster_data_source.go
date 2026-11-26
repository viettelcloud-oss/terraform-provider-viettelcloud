package dbaascluster

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
	dbaasclustersdk "github.com/viettelcloud-oss/sdks/go/dbaascluster"
	projectsdk "github.com/viettelcloud-oss/sdks/go/project"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/compare"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/parse"
	dbaasclusterlookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/dbaascluster/lookup"
	projectlookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/project/lookup"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
)

var (
	_ datasource.DataSource              = &DBClusterDataSource{}
	_ datasource.DataSourceWithConfigure = &DBClusterDataSource{}
)

type DBClusterDataSource struct {
	client    *dbaasclustersdk.Client
	project   *projectsdk.Client
	projectID core.UUID
}

func NewDBClusterDataSource() datasource.DataSource {
	return &DBClusterDataSource{}
}

func (d *DBClusterDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_dbc_cluster"
}

func (d *DBClusterDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Get a Viettel Cloud DB cluster by ID, name, engine, region or status. " +
			"Set at least one, and exactly one cluster must match. A name is unique per project and engine, so name with engine selects one cluster.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "DB cluster ID (UUID). Exactly one cluster must match.",
			},
			"name": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Exact name of the DB cluster to filter by.",
			},
			"engine": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Database engine to filter by: cassandra, memcached, mongodb, opensearch, starrocks or timescaledb.",
			},
			"region": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Project region name of the cluster to filter by.",
			},
			"status": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Status of the cluster to filter by, such as active.",
			},
			"description": schema.StringAttribute{Computed: true, Description: "Description of the DB cluster."},
			"topology":    schema.StringAttribute{Computed: true, Description: "Topology of the cluster."},
			"keep_backup": schema.BoolAttribute{
				Computed:    true,
				Description: "Whether backups are kept when the cluster is deleted.",
			},
			"engine_version": schema.StringAttribute{Computed: true, Description: "Engine version of the cluster."},
			"zone":           schema.StringAttribute{Computed: true, Description: "Zone name, or null for a multi-zone cluster."},
			"subnet_id":      schema.StringAttribute{Computed: true, Description: "ID of the subnet that the cluster connects to."},
			"subnet_name":    schema.StringAttribute{Computed: true, Description: "Name of the subnet that the cluster connects to."},
			"vpc_name":       schema.StringAttribute{Computed: true, Description: "Name of the VPC of the subnet."},
			"node_pools": schema.ListNestedAttribute{
				Computed:    true,
				Description: "Node pools of the cluster.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":              schema.StringAttribute{Computed: true, Description: "Node pool ID (UUID)."},
						"name":            schema.StringAttribute{Computed: true, Description: "Name of the node pool."},
						"role":            schema.StringAttribute{Computed: true, Description: "Role of the pool in the topology."},
						"replicas":        schema.Int64Attribute{Computed: true, Description: "Number of replicas in the pool."},
						"partition_count": schema.Int64Attribute{Computed: true, Description: "Number of partitions, which is the shard count for mongo_sharding."},
						"node_count":      schema.Int64Attribute{Computed: true, Description: "Number of nodes in the pool."},
						"resource_config": schema.StringAttribute{Computed: true, Description: "Name of the resource package of each node."},
						"storage_class":   schema.StringAttribute{Computed: true, Description: "Name of the storage class of the node volumes."},
						"disk_size":       schema.Int64Attribute{Computed: true, Description: "Volume size of each node in GB."},
						"used_disk_size":  schema.Int64Attribute{Computed: true, Description: "Used volume space of each node."},
						"engine_roles":    schema.SetAttribute{ElementType: types.StringType, Computed: true, Description: "Engine roles of the pool."},
						"pool_config": schema.SingleNestedAttribute{
							Computed:    true,
							Description: "Engine-specific settings of the pool.",
							Attributes: map[string]schema.Attribute{
								"attribute": schema.StringAttribute{Computed: true, Description: "Storage tier of the pool."},
							},
						},
					},
				},
			},
			"plugins":                          schema.SetAttribute{ElementType: types.StringType, Computed: true, Description: "Names of the installed plugins."},
			"allowed_cidrs":                    schema.SetAttribute{ElementType: types.StringType, Computed: true, Description: "IPv4 CIDR blocks that may connect to the cluster."},
			"enable_public_access":             schema.BoolAttribute{Computed: true, Description: "Whether the cluster is reachable from the internet."},
			"enable_ssl":                       schema.BoolAttribute{Computed: true, Description: "Whether connections use SSL."},
			"enable_auto_increase_volume_size": schema.BoolAttribute{Computed: true, Description: "Whether the backend grows node volumes automatically."},
			"enable_pitr_backup":               schema.BoolAttribute{Computed: true, Description: "Whether point-in-time recovery is enabled."},
			"enable_auto_backup":               schema.BoolAttribute{Computed: true, Description: "Whether automatic backup is enabled."},
			"auto_backup_config": schema.SingleNestedAttribute{
				Computed:    true,
				Description: "Automatic backup schedule, or null when the cluster has none.",
				Attributes: map[string]schema.Attribute{
					"enable_auto_backup": schema.BoolAttribute{Computed: true, Description: "Whether automatic backup is enabled."},
					"backup_interval":    schema.StringAttribute{Computed: true, Description: "Backup interval: daily or weekly."},
					"day_of_week":        schema.StringAttribute{Computed: true, Description: "Day of the week of a weekly backup."},
					"start_hour":         schema.Int64Attribute{Computed: true, Description: "Hour of day that the backup starts."},
					"max_auto_backup":    schema.Int64Attribute{Computed: true, Description: "Number of automatic backups to keep, or null for an engine that keeps backups by age."},
					"retention_days":     schema.Int64Attribute{Computed: true, Description: "Number of days to keep automatic backups, or null for an engine that keeps backups by count."},
				},
			},
			"maintenance_config": schema.SingleNestedAttribute{
				Computed:    true,
				Description: "Weekly maintenance window.",
				Attributes: map[string]schema.Attribute{
					"day_of_week": schema.StringAttribute{Computed: true, Description: "Day of the week."},
					"start_hour":  schema.Int64Attribute{Computed: true, Description: "Hour of day that maintenance starts."},
				},
			},
			"engine_config": schema.SingleNestedAttribute{
				Computed:    true,
				Description: "Engine settings, or null for an engine without settings. Only memcached has settings.",
				Attributes: map[string]schema.Attribute{
					"mode":                  schema.StringAttribute{Computed: true, Description: "Memcached mode: direct or mcrouter."},
					"auth_enabled":          schema.BoolAttribute{Computed: true, Description: "Whether clients must authenticate."},
					"mcrouter_routing_type": schema.StringAttribute{Computed: true, Description: "Routing of the mcrouter mode: sharded or replicated."},
				},
			},
			"metadata":           schema.MapAttribute{ElementType: types.StringType, Computed: true, Description: "Free-form string metadata."},
			"admin_username":     schema.StringAttribute{Computed: true, Description: "Username of the administrator account."},
			"endpoint":           schema.MapAttribute{ElementType: types.StringType, Computed: true, Description: "Connection endpoint that the backend reports. A value that is not a string is encoded as JSON."},
			"operational_status": schema.StringAttribute{Computed: true, Description: "Operation that the backend is running on the cluster, or noop."},
			"error_message":      schema.StringAttribute{Computed: true, Description: "Error message of the last failed operation."},
			"created_at":         schema.StringAttribute{Computed: true, Description: "Timestamp when the cluster was created (RFC3339)."},
			"updated_at":         schema.StringAttribute{Computed: true, Description: "Timestamp when the cluster was last updated (RFC3339)."},
		},
	}
}

func (d *DBClusterDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	data, ok := req.ProviderData.(*providerdata.Configured)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data type", fmt.Sprintf("Expected *providerdata.Configured, got %T", req.ProviderData))
		return
	}
	d.client = data.DBaaSCluster
	d.project = data.Project
	d.projectID = data.ProjectID
}

func (d *DBClusterDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config DBClusterDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	cluster, diags := d.getDBCluster(ctx, config)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() || cluster == nil {
		return
	}

	state := config
	populateDBClusterDataSourceState(cluster, &state)
	preserveConfiguredLookupValues(cluster, config, &state)

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (d *DBClusterDataSource) getDBCluster(ctx context.Context, config DBClusterDataSourceModel) (*dbaasclustersdk.DBClusterDetailSchema, diag.Diagnostics) {
	var diags diag.Diagnostics

	hasID := configured(config.ID)
	hasName := configured(config.Name)
	hasEngine := configured(config.Engine)
	hasRegion := configured(config.Region)
	hasStatus := configured(config.Status)

	if !hasID && !hasName && !hasEngine && !hasRegion && !hasStatus {
		diags.AddError(
			"Missing DB Cluster lookup criteria",
			"Specify at least one of id, name, engine, region, or status to look up a DB cluster.",
		)
		return nil, diags
	}
	for _, filter := range []struct {
		set   bool
		value types.String
		name  string
	}{
		{hasName, config.Name, "name"},
		{hasEngine, config.Engine, "engine"},
		{hasRegion, config.Region, "region"},
		{hasStatus, config.Status, "status"},
	} {
		if filter.set && strings.TrimSpace(filter.value.ValueString()) == "" {
			diags.AddError("Invalid DB cluster "+filter.name+" filter", filter.name+" must not be empty.")
			return nil, diags
		}
	}

	// A lookup by ID alone needs no listing.
	if hasID && !hasName && !hasEngine && !hasRegion && !hasStatus {
		return d.getDBClusterByID(ctx, config.ID)
	}
	return d.getDBClusterByFilter(ctx, config)
}

func (d *DBClusterDataSource) getDBClusterByID(ctx context.Context, idAttr types.String) (*dbaasclustersdk.DBClusterDetailSchema, diag.Diagnostics) {
	var diags diag.Diagnostics

	clusterID, parseDiags := parse.UUIDString(idAttr, "id")
	diags.Append(parseDiags...)
	if diags.HasError() {
		return nil, diags
	}
	return d.getDetail(ctx, clusterID)
}

func (d *DBClusterDataSource) getDBClusterByFilter(ctx context.Context, config DBClusterDataSourceModel) (*dbaasclustersdk.DBClusterDetailSchema, diag.Diagnostics) {
	var diags diag.Diagnostics
	filter := dbaasclusterlookup.DBClusterFilter{}

	if configured(config.ID) {
		clusterID, parseDiags := parse.UUIDString(config.ID, "id")
		diags.Append(parseDiags...)
		if diags.HasError() {
			return nil, diags
		}
		filter.ID = &clusterID
	}
	if configured(config.Name) {
		name := config.Name.ValueString()
		filter.Name = &name
	}
	if configured(config.Engine) {
		engine := config.Engine.ValueString()
		filter.Engine = &engine
	}
	if configured(config.Status) {
		status := config.Status.ValueString()
		filter.Status = &status
	}
	if configured(config.Region) {
		name := config.Region.ValueString()
		region, err := projectlookup.NewRegionFinder(d.project, d.projectID).Resolve(ctx, projectlookup.RegionFilter{Name: &name})
		if err != nil {
			diags.AddError("Error resolving region", err.Error())
			return nil, diags
		}
		filter.RegionID = &region.Region.Id
	}

	candidate, err := dbaasclusterlookup.NewDBClusterFinder(d.client, d.projectID).Resolve(ctx, filter)
	if err != nil {
		diags.AddError("Error resolving DB cluster", err.Error())
		return nil, diags
	}
	// The list schema omits plugins and other detail fields.
	return d.getDetail(ctx, candidate.Id)
}

func (d *DBClusterDataSource) getDetail(ctx context.Context, id core.UUID) (*dbaasclustersdk.DBClusterDetailSchema, diag.Diagnostics) {
	var diags diag.Diagnostics
	cluster, err := d.client.GetDbCluster(ctx, id, dbaasclustersdk.GetDbClusterParams{ProjectID: d.projectID})
	if err != nil {
		if errors.Is(err, dbaasclustersdk.ErrNotFound) {
			diags.AddError("DB cluster not found", fmt.Sprintf("No DB cluster found with id %s", id))
			return nil, diags
		}
		diags.AddError("Error reading DB cluster", err.Error())
		return nil, diags
	}
	return cluster, diags
}

// preserveConfiguredLookupValues keeps the configured spelling of a lookup
// argument when it names the same value, to prevent a false difference.
func preserveConfiguredLookupValues(cluster *dbaasclustersdk.DBClusterDetailSchema, config DBClusterDataSourceModel, state *DBClusterDataSourceModel) {
	if compare.SameUUID(config.ID, cluster.Id) {
		state.ID = config.ID
	}
	if configured(config.Name) && strings.TrimSpace(config.Name.ValueString()) == cluster.Name {
		state.Name = config.Name
	}
	if configured(config.Engine) && strings.TrimSpace(config.Engine.ValueString()) == string(cluster.Engine) {
		state.Engine = config.Engine
	}
	if configured(config.Region) && strings.TrimSpace(config.Region.ValueString()) == cluster.Region.Name {
		state.Region = config.Region
	}
	if configured(config.Status) && strings.TrimSpace(config.Status.ValueString()) == string(cluster.Status) {
		state.Status = config.Status
	}
}
