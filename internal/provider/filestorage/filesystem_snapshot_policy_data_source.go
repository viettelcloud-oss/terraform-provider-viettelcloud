package filestorage

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
	filestoragesdk "github.com/viettelcloud-oss/sdks/go/filestorage"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/parse"
	filestoragelookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/filestorage/lookup"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
)

var _ datasource.DataSource = &FilesystemSnapshotPolicyDataSource{}

type FilesystemSnapshotPolicyDataSource struct {
	client    *filestoragesdk.Client
	projectID core.UUID
}

type filesystemSnapshotPolicyCriteria struct{ id, name, region bool }

func NewFilesystemSnapshotPolicyDataSource() datasource.DataSource {
	return &FilesystemSnapshotPolicyDataSource{}
}

func (d *FilesystemSnapshotPolicyDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_fs_filesystem_snapshot_policy"
}

func (d *FilesystemSnapshotPolicyDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Get one Viettel Cloud File Storage filesystem snapshot policy by ID or exact filter criteria.",
		Attributes: map[string]schema.Attribute{
			"id":               schema.StringAttribute{Optional: true, Computed: true, Description: "Snapshot policy ID (UUID)."},
			"name":             schema.StringAttribute{Optional: true, Computed: true, Description: "Snapshot policy name to filter by."},
			"region":           schema.StringAttribute{Optional: true, Computed: true, Description: "Project region name to filter by."},
			"description":      schema.StringAttribute{Computed: true, Description: "Snapshot policy description."},
			"days_of_week":     schema.ListAttribute{Computed: true, ElementType: types.StringType, Description: "Days of the week on which the policy takes a snapshot."},
			"time":             schema.StringAttribute{Computed: true, Description: "Time of day in UTC at which the policy takes a snapshot."},
			"retention":        schema.Int64Attribute{Computed: true, Description: "Number of snapshots the policy keeps."},
			"status":           schema.StringAttribute{Computed: true, Description: "Policy status: enabled or disabled."},
			"filesystem_count": schema.Int64Attribute{Computed: true, Description: "Number of filesystems that use the policy."},
			"state":            schema.StringAttribute{Computed: true, Description: "State of the latest policy run."},
			"region_id":        schema.StringAttribute{Computed: true, Description: "Region ID (UUID)."},
			"region_name":      schema.StringAttribute{Computed: true, Description: "Region name that the backend reports."},
			"project_id":       schema.StringAttribute{Computed: true, Description: "Project ID (UUID)."},
			"owner_id":         schema.StringAttribute{Computed: true, Description: "Owner ID (UUID)."},
			"created_at":       schema.StringAttribute{Computed: true, Description: "Creation time (RFC3339)."},
			"updated_at":       schema.StringAttribute{Computed: true, Description: "Last update time (RFC3339)."},
		},
	}
}

func (d *FilesystemSnapshotPolicyDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	data, ok := req.ProviderData.(*providerdata.Configured)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data type", fmt.Sprintf("Expected *providerdata.Configured, got %T", req.ProviderData))
		return
	}
	d.client, d.projectID = data.FileStorage, data.ProjectID
}

func (d *FilesystemSnapshotPolicyDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config FilesystemSnapshotPolicyDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	policy, diags := d.getPolicy(ctx, config)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() || policy == nil {
		return
	}
	state := config
	populateFilesystemSnapshotPolicyDataSourceState(policy, &state)
	preserveFilesystemSnapshotPolicyFilters(policy, &config, &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (d *FilesystemSnapshotPolicyDataSource) getPolicy(ctx context.Context, config FilesystemSnapshotPolicyDataSourceModel) (*filestoragesdk.FilesystemSnapshotPolicySchema, diag.Diagnostics) {
	criteria, diags := filesystemSnapshotPolicyLookupCriteria(config)
	if diags.HasError() {
		return nil, diags
	}
	if criteria == (filesystemSnapshotPolicyCriteria{id: true}) {
		return d.getPolicyByID(ctx, config.ID)
	}
	return d.getPolicyByFilter(ctx, config, criteria)
}

func (d *FilesystemSnapshotPolicyDataSource) getPolicyByID(ctx context.Context, idAttr types.String) (*filestoragesdk.FilesystemSnapshotPolicySchema, diag.Diagnostics) {
	id, diags := parse.UUIDString(idAttr, "id")
	if diags.HasError() {
		return nil, diags
	}
	policy, err := d.client.GetFilesystemSnapshotPolicy(ctx, id, filestoragesdk.GetFilesystemSnapshotPolicyParams{ProjectID: d.projectID})
	if errors.Is(err, filestoragesdk.ErrNotFound) {
		diags.AddError("Filesystem snapshot policy not found", fmt.Sprintf("No snapshot policy found with id %s.", idAttr.ValueString()))
	} else if err != nil {
		diags.AddError("Error reading filesystem snapshot policy", err.Error())
	}
	return policy, diags
}

func (d *FilesystemSnapshotPolicyDataSource) getPolicyByFilter(ctx context.Context, config FilesystemSnapshotPolicyDataSourceModel, criteria filesystemSnapshotPolicyCriteria) (*filestoragesdk.FilesystemSnapshotPolicySchema, diag.Diagnostics) {
	id, diags := parse.OptionalUUIDString(config.ID, "id")
	if diags.HasError() {
		return nil, diags
	}
	filter := filestoragelookup.FilesystemSnapshotPolicyFilter{ID: id}
	if criteria.name {
		name := config.Name.ValueString()
		filter.Name = &name
	}
	if criteria.region {
		region := config.Region.ValueString()
		filter.RegionName = &region
	}
	policy, err := filestoragelookup.NewFilesystemSnapshotPolicyFinder(d.client, d.projectID).Resolve(ctx, filter)
	if err != nil {
		diags.AddError("Unable to resolve filesystem snapshot policy", err.Error())
		return nil, diags
	}
	return &policy, diags
}

func filesystemSnapshotPolicyLookupCriteria(config FilesystemSnapshotPolicyDataSourceModel) (filesystemSnapshotPolicyCriteria, diag.Diagnostics) {
	var diags diag.Diagnostics
	criteria := filesystemSnapshotPolicyCriteria{knownValue(config.ID), knownValue(config.Name), knownValue(config.Region)}
	if criteria == (filesystemSnapshotPolicyCriteria{}) {
		diags.AddError("Missing FilesystemSnapshotPolicy lookup criteria", "Specify at least one of id, name, or region.")
		return criteria, diags
	}
	for _, entry := range []struct {
		field string
		value types.String
		set   bool
	}{
		{"id", config.ID, criteria.id}, {"name", config.Name, criteria.name}, {"region", config.Region, criteria.region},
	} {
		if entry.set && strings.TrimSpace(entry.value.ValueString()) == "" {
			diags.AddError("Invalid FilesystemSnapshotPolicy lookup criteria", fmt.Sprintf("%s must not be empty or whitespace-only.", entry.field))
		}
	}
	return criteria, diags
}
