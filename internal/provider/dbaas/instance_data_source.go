package dbaas

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
	dbaassdk "github.com/viettelcloud-oss/sdks/go/dbaas"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/parse"
	dbaaslookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/dbaas/lookup"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
)

var _ datasource.DataSource = &InstanceDataSource{}

type InstanceDataSource struct {
	client    *dbaassdk.Client
	projectID core.UUID
}

func NewInstanceDataSource() datasource.DataSource { return &InstanceDataSource{} }

func (d *InstanceDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_db_instance"
}

func (d *InstanceDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	computedString := func(description string) schema.StringAttribute {
		return schema.StringAttribute{Computed: true, Description: description}
	}
	computedInt := func(description string) schema.Int64Attribute {
		return schema.Int64Attribute{Computed: true, Description: description}
	}
	computedBool := func(description string) schema.BoolAttribute {
		return schema.BoolAttribute{Computed: true, Description: description}
	}
	resp.Schema = schema.Schema{
		Description: "Find one Viettel Cloud DBaaS DB instance by ID or exact name. Configure exactly one of id and name.",
		Attributes: map[string]schema.Attribute{
			"id":                    schema.StringAttribute{Optional: true, Computed: true, Description: "DB instance ID (UUID)."},
			"name":                  schema.StringAttribute{Optional: true, Computed: true, Description: "Exact DB instance name, unique in the project."},
			"description":           computedString("Description."),
			"database_type":         computedString("Database type."),
			"db_version":            computedString("DB version."),
			"db_version_id":         computedString("DB version ID (UUID)."),
			"region":                computedString("Region name."),
			"region_id":             computedString("Region ID (UUID)."),
			"flavor":                computedString("Flavor name."),
			"flavor_id":             computedString("Flavor ID (UUID)."),
			"flavor_vcpus":          computedInt("vCPUs of the flavor."),
			"flavor_ram":            computedInt("RAM of the flavor in MB."),
			"volume_type":           computedString("Volume type name."),
			"volume_type_id":        computedString("Volume type ID (UUID)."),
			"volume_type_disk_type": computedString("Disk type of the volume type."),
			"current_disk_size":     computedInt("Disk size in GB that the backend has provisioned, including any automatic increase."),
			"used_disk_size":        computedInt("Used disk space in MB."),
			"disk_full":             computedBool("Whether the disk is full."),
			"solution":              computedString("Solution."),
			"subnet_id":             computedString("Subnet ID (UUID)."),
			"subnet_name":           computedString("Subnet name."),
			"subnet_cidr":           computedString("Subnet CIDR."),
			"vpc_id":                computedString("VPC ID (UUID) of the subnet."),
			"vpc_name":              computedString("VPC name of the subnet."),
			"enable_public_access":  computedBool("Whether the instance has a public endpoint."),
			"maintenance_config": schema.SingleNestedAttribute{Computed: true, Description: "Maintenance window.", Attributes: map[string]schema.Attribute{
				"day_of_week": computedString("Lowercase day name."),
				"start_hour":  computedInt("Start hour, 0 to 23."),
			}},
			"keep_backup": computedBool("Whether backups remain after the instance is deleted."),
			"backup_policy": schema.SingleNestedAttribute{Computed: true, Description: "Automatic backup policy.", Attributes: map[string]schema.Attribute{
				"enabled":         computedBool("Whether automatic backups run."),
				"backup_interval": computedString("daily or weekly."),
				"day_of_week":     computedString("Lowercase day name of a weekly backup."),
				"start_hour":      computedInt("Start hour, 0 to 23."),
				"max_auto_backup": computedInt("Automatic backups kept."),
			}},
			"auto_increase_volume_size": schema.SingleNestedAttribute{Computed: true, Description: "Automatic disk increase.", Attributes: map[string]schema.Attribute{
				"enabled":   computedBool("Whether the disk grows automatically."),
				"threshold": computedInt("Used-space percentage that triggers an increase."),
				"limit":     computedInt("Maximum disk size in GB."),
			}},
			"allowed_cidrs":      schema.SetAttribute{Computed: true, ElementType: types.StringType, Description: "CIDRs allowed to connect."},
			"enable_ssl":         computedBool("Whether SSL is enabled."),
			"private_url":        computedString("Private endpoint host."),
			"public_url":         computedString("Public endpoint host."),
			"port":               computedInt("Database port."),
			"init_backup_id":     computedString("ID (UUID) of the backup that the instance was restored from, if any."),
			"project_id":         computedString("Owning project ID (UUID)."),
			"status":             computedString("Instance status."),
			"operational_status": computedString("Running operation."),
			"error_message":      computedString("Last error that the backend reported."),
			"created_at":         computedString("Creation timestamp (RFC3339)."),
			"updated_at":         computedString("Last update timestamp (RFC3339)."),
		},
	}
}

func (d *InstanceDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	data, ok := req.ProviderData.(*providerdata.Configured)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data type", fmt.Sprintf("Expected *providerdata.Configured, got %T", req.ProviderData))
		return
	}
	d.client, d.projectID = data.DBaaS, data.ProjectID
}

func (d *InstanceDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config InstanceDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	instance, diags := d.getInstance(ctx, config)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() || instance == nil {
		return
	}
	state := config
	populateInstanceDataSourceState(instance, &state)
	preserveInstanceConfiguredValues(instance, config, &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (d *InstanceDataSource) getInstance(ctx context.Context, config InstanceDataSourceModel) (*dbaassdk.DBInstanceSchema, diag.Diagnostics) {
	var diags diag.Diagnostics
	hasID, hasName := !config.ID.IsNull(), !config.Name.IsNull()
	switch {
	case !hasID && !hasName:
		diags.AddError("Missing DB Instance lookup criteria", "Specify id or name.")
	case hasID && hasName:
		diags.AddError("Conflicting DB Instance lookup criteria", "Specify only one of id and name.")
	case config.ID.IsUnknown() || config.Name.IsUnknown():
		diags.AddError("Unknown DB Instance lookup criteria", "The lookup argument must be known.")
	case hasName && strings.TrimSpace(config.Name.ValueString()) == "":
		diags.AddError("Invalid DB Instance lookup criteria", "name must not be empty or whitespace-only.")
	}
	if diags.HasError() {
		return nil, diags
	}
	if hasID {
		id, idDiags := parse.UUIDString(config.ID, "id")
		diags.Append(idDiags...)
		if diags.HasError() {
			return nil, diags
		}
		return d.getInstanceByID(ctx, id)
	}
	return d.getInstanceByName(ctx, config.Name.ValueString())
}

func (d *InstanceDataSource) getInstanceByID(ctx context.Context, id core.UUID) (*dbaassdk.DBInstanceSchema, diag.Diagnostics) {
	var diags diag.Diagnostics
	instance, err := d.client.GetDbInstance(ctx, id, dbaassdk.GetDbInstanceParams{ProjectID: d.projectID})
	if errors.Is(err, dbaassdk.ErrNotFound) {
		diags.AddError("DB instance not found", fmt.Sprintf("No DB instance found with id %s.", id))
	} else if err != nil {
		diags.AddError("Error reading DB instance", err.Error())
	}
	return instance, diags
}

func (d *InstanceDataSource) getInstanceByName(ctx context.Context, name string) (*dbaassdk.DBInstanceSchema, diag.Diagnostics) {
	var diags diag.Diagnostics
	match, err := dbaaslookup.NewInstanceFinder(d.client, d.projectID).Resolve(ctx, dbaaslookup.InstanceFilter{Name: &name})
	if err != nil {
		diags.AddError("Unable to resolve DB instance", err.Error())
		return nil, diags
	}
	return &match, diags
}
