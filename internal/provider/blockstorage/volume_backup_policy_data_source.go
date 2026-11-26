package blockstorage

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	blockstoragesdk "github.com/viettelcloud-oss/sdks/go/blockstorage"
	"github.com/viettelcloud-oss/sdks/go/core"
	projectsdk "github.com/viettelcloud-oss/sdks/go/project"

	blockstoragelookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/blockstorage/lookup"
	projectlookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/project/lookup"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
)

var _ datasource.DataSource = &VolumeBackupPolicyDataSource{}

type VolumeBackupPolicyDataSource struct {
	client    *blockstoragesdk.Client
	project   *projectsdk.Client
	projectID core.UUID
}

type volumeBackupPolicyCriteria struct{ id, name, region, status bool }

func NewVolumeBackupPolicyDataSource() datasource.DataSource {
	return &VolumeBackupPolicyDataSource{}
}

func (d *VolumeBackupPolicyDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_volume_backup_policy"
}

func (d *VolumeBackupPolicyDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Get one Viettel Cloud Block Storage volume backup policy by ID or exact filter criteria.",
		Attributes: map[string]schema.Attribute{
			"id":     schema.StringAttribute{Optional: true, Computed: true, Description: "Backup policy ID (UUID)."},
			"name":   schema.StringAttribute{Optional: true, Computed: true, Description: "Backup policy name to filter by."},
			"region": schema.StringAttribute{Optional: true, Computed: true, Description: "Project region name to filter by."},
			"status": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Policy status to filter by: enabled or disabled.",
				Validators:  []validator.String{trimmedBackupPolicyValue(stringvalidator.OneOf(string(blockstoragesdk.VolumeBackupPolicyStatusEnabled), string(blockstoragesdk.VolumeBackupPolicyStatusDisabled)))},
			},
			"description": schema.StringAttribute{Computed: true, Description: "Backup policy description."},
			"retention":   schema.Int64Attribute{Computed: true, Description: "Number of backups that the policy keeps for each volume."},
			"schedule": schema.SingleNestedAttribute{
				Computed:    true,
				Description: "When the policy takes backups.",
				Attributes: map[string]schema.Attribute{
					"execution_strategy": schema.StringAttribute{Computed: true, Description: "Schedule type: periodic or days_of_month."},
					"frequency":          schema.Int64Attribute{Computed: true, Description: "Number of frequency units between two backups. Null with `days_of_month`."},
					"frequency_unit":     schema.StringAttribute{Computed: true, Description: "Unit of `frequency`: day, week, or month. Null with `days_of_month`."},
					"first_execution":    schema.StringAttribute{Computed: true, Description: "Time of the first backup (RFC3339). Null with `days_of_month`."},
					"days_of_month":      schema.SetAttribute{Computed: true, ElementType: types.Int64Type, Description: "Days of the month on which the policy takes a backup. Null with `periodic`."},
					"time":               schema.StringAttribute{Computed: true, Description: "Time of day in UTC at which the policy takes a backup. Null with `periodic`."},
				},
			},
			"state":          schema.StringAttribute{Computed: true, Description: "State of the latest backup run."},
			"volume_count":   schema.Int64Attribute{Computed: true, Description: "Number of volumes that use the policy."},
			"next_execution": schema.StringAttribute{Computed: true, Description: "Time of the next backup (RFC3339). Null when none is planned."},
			"last_execution": schema.StringAttribute{Computed: true, Description: "Time of the latest backup run (RFC3339). Null before the first run."},
			"created_at":     schema.StringAttribute{Computed: true, Description: "Creation time (RFC3339)."},
			"updated_at":     schema.StringAttribute{Computed: true, Description: "Last update time (RFC3339)."},
		},
	}
}

func (d *VolumeBackupPolicyDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	data, ok := req.ProviderData.(*providerdata.Configured)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data type", fmt.Sprintf("Expected *providerdata.Configured, got %T", req.ProviderData))
		return
	}
	d.client, d.project, d.projectID = data.BlockStorage, data.Project, data.ProjectID
}

func (d *VolumeBackupPolicyDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config VolumeBackupPolicyDataSourceModel
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
	resp.Diagnostics.Append(populateVolumeBackupPolicyDataSourceState(policy, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	preserveVolumeBackupPolicyFilters(policy, &config, &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (d *VolumeBackupPolicyDataSource) getPolicy(ctx context.Context, config VolumeBackupPolicyDataSourceModel) (*blockstoragesdk.VolumeBackupPolicySchema, diag.Diagnostics) {
	criteria, diags := volumeBackupPolicyLookupCriteria(config)
	if diags.HasError() {
		return nil, diags
	}
	if criteria == (volumeBackupPolicyCriteria{id: true}) {
		return d.getPolicyByID(ctx, config.ID)
	}
	return d.getPolicyByFilter(ctx, config, criteria)
}

func (d *VolumeBackupPolicyDataSource) getPolicyByID(ctx context.Context, idAttr types.String) (*blockstoragesdk.VolumeBackupPolicySchema, diag.Diagnostics) {
	id, diags := parseBackupPolicyUUID(idAttr, "id")
	if diags.HasError() {
		return nil, diags
	}
	policy, err := d.client.GetVolumeBackupPolicy(ctx, id, blockstoragesdk.GetVolumeBackupPolicyParams{ProjectID: d.projectID})
	if errors.Is(err, blockstoragesdk.ErrNotFound) {
		diags.AddError("Volume backup policy not found", fmt.Sprintf("No volume backup policy found with id %s.", idAttr.ValueString()))
	} else if err != nil {
		diags.AddError("Error reading volume backup policy", err.Error())
	}
	return policy, diags
}

func (d *VolumeBackupPolicyDataSource) getPolicyByFilter(ctx context.Context, config VolumeBackupPolicyDataSourceModel, criteria volumeBackupPolicyCriteria) (*blockstoragesdk.VolumeBackupPolicySchema, diag.Diagnostics) {
	var (
		filter blockstoragelookup.VolumeBackupPolicyFilter
		diags  diag.Diagnostics
	)
	if criteria.id {
		id, idDiags := parseBackupPolicyUUID(config.ID, "id")
		diags.Append(idDiags...)
		if diags.HasError() {
			return nil, diags
		}
		filter.ID = &id
	}
	if criteria.name {
		name := strings.TrimSpace(config.Name.ValueString())
		filter.Name = &name
	}
	if criteria.status {
		status := blockstoragesdk.VolumeBackupPolicyStatus(strings.TrimSpace(config.Status.ValueString()))
		filter.Status = &status
	}
	if criteria.region {
		regionName := strings.TrimSpace(config.Region.ValueString())
		region, err := projectlookup.NewRegionFinder(d.project, d.projectID).Resolve(ctx, projectlookup.RegionFilter{Name: &regionName})
		if err != nil {
			diags.AddAttributeError(path.Root("region"), "Unable to resolve region", err.Error())
			return nil, diags
		}
		filter.RegionID = &region.Region.Id
	}
	policy, err := blockstoragelookup.NewVolumeBackupPolicyFinder(d.client, d.projectID).Resolve(ctx, filter)
	if err != nil {
		diags.AddError("Unable to resolve volume backup policy", err.Error())
		return nil, diags
	}
	return &policy, diags
}

func volumeBackupPolicyLookupCriteria(config VolumeBackupPolicyDataSourceModel) (volumeBackupPolicyCriteria, diag.Diagnostics) {
	var diags diag.Diagnostics
	criteria := volumeBackupPolicyCriteria{knownValue(config.ID), knownValue(config.Name), knownValue(config.Region), knownValue(config.Status)}
	if criteria == (volumeBackupPolicyCriteria{}) {
		diags.AddError("Missing Volume Backup Policy lookup criteria", "Specify at least one of id, name, region, or status.")
		return criteria, diags
	}
	for _, entry := range []struct {
		field string
		value types.String
		set   bool
	}{
		{"id", config.ID, criteria.id}, {"name", config.Name, criteria.name}, {"region", config.Region, criteria.region}, {"status", config.Status, criteria.status},
	} {
		if entry.set && strings.TrimSpace(entry.value.ValueString()) == "" {
			diags.AddError("Invalid Volume Backup Policy lookup criteria", fmt.Sprintf("%s must not be empty or whitespace-only.", entry.field))
		}
	}
	return criteria, diags
}
