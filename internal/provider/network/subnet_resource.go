package network

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/parse"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/viettelcloud-oss/sdks/go/core"
	networksdk "github.com/viettelcloud-oss/sdks/go/network"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
)

var (
	_ resource.Resource                = &SubnetResource{}
	_ resource.ResourceWithImportState = &SubnetResource{}
)

type SubnetResource struct {
	client    *networksdk.Client
	projectID core.UUID
}

func NewSubnetResource() resource.Resource {
	return &SubnetResource{}
}

func (r *SubnetResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_subnet"
}

func (r *SubnetResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manage a Viettel Cloud subnet.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Subnet ID (UUID).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Name of the subnet.",
			},
			"description": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Optional description of the subnet. Omitting it preserves the backend value; set it to an empty string to clear it.",
			},
			"cidr": schema.StringAttribute{
				Required: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
				Description: "IPv4 network CIDR inside the VPC, e.g. 10.0.1.0/24.",
			},
			"vpc": schema.SingleNestedAttribute{
				Required:    true,
				Description: "VPC where the subnet is created.",
				Attributes:  reference.Attributes(reference.ID, "VPC ID (UUID)."),
				PlanModifiers: []planmodifier.Object{
					objectplanmodifier.RequiresReplace(),
				},
			},
			"vpc_name": schema.StringAttribute{
				Computed:    true,
				Description: "Name of the VPC containing the subnet.",
			},
			"region": schema.StringAttribute{
				Computed:    true,
				Description: "Region name inherited from the VPC.",
			},
			"display_name": schema.StringAttribute{
				Computed:    true,
				Description: "Display name assigned by the platform.",
			},
			"created_at": schema.StringAttribute{
				Computed:    true,
				Description: "Timestamp when the subnet was created (RFC3339).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"updated_at": schema.StringAttribute{
				Computed:    true,
				Description: "Timestamp when the subnet was last updated (RFC3339).",
			},
		},
	}
}

func (r *SubnetResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	data, ok := req.ProviderData.(*providerdata.Configured)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data type", fmt.Sprintf("Expected *providerdata.Configured, got %T", req.ProviderData))
		return
	}
	r.client = data.Network
	r.projectID = data.ProjectID
}

func (r *SubnetResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan SubnetResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	body, diags := buildSubnetCreateBody(plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	subnet, err := r.client.CreateSubnet(ctx, networksdk.CreateSubnetParams{
		ProjectID: r.projectID,
	}, body)
	if err != nil {
		resp.Diagnostics.AddError("Error creating subnet", err.Error())
		return
	}

	populateSubnetResourceState(subnet, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *SubnetResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state SubnetResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	subnetID, diags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	subnet, err := r.client.GetSubnet(ctx, subnetID, networksdk.GetSubnetParams{
		ProjectID: r.projectID,
	})
	if err != nil {
		if errors.Is(err, networksdk.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading subnet", err.Error())
		return
	}

	populateSubnetResourceState(subnet, &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *SubnetResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan SubnetResourceModel
	var state SubnetResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	subnetID, diags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	body, changed, diags := buildSubnetUpdateBody(plan, state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	var subnet *networksdk.SubnetSchema
	var err error
	if changed {
		subnet, err = r.client.PartialUpdateSubnet(ctx, subnetID, networksdk.PartialUpdateSubnetParams{
			ProjectID: r.projectID,
		}, body)
	} else {
		subnet, err = r.client.GetSubnet(ctx, subnetID, networksdk.GetSubnetParams{
			ProjectID: r.projectID,
		})
	}
	if err != nil {
		resp.Diagnostics.AddError("Error updating subnet", err.Error())
		return
	}

	newState := plan
	populateSubnetResourceState(subnet, &newState)
	resp.Diagnostics.Append(resp.State.Set(ctx, &newState)...)
}

func (r *SubnetResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state SubnetResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	subnetID, diags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.DeleteSubnet(ctx, subnetID, networksdk.DeleteSubnetParams{
		ProjectID: r.projectID,
	})
	if err != nil && !errors.Is(err, networksdk.ErrNotFound) {
		resp.Diagnostics.AddError("Error deleting subnet", err.Error())
	}
}

func (r *SubnetResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func buildSubnetCreateBody(plan SubnetResourceModel) (networksdk.SubnetCreateSchema, diag.Diagnostics) {
	var body networksdk.SubnetCreateSchema
	var diags diag.Diagnostics

	if plan.Name.IsNull() || plan.Name.IsUnknown() {
		diags.AddError("Missing subnet name", "name must be configured and known.")
	}
	if plan.CIDR.IsNull() || plan.CIDR.IsUnknown() {
		diags.AddError("Missing subnet CIDR", "cidr must be configured and known.")
	}
	vpcID, vpcDiags := parse.UUIDString(reference.Get(plan.VPC, reference.ID), "vpc.id")
	diags.Append(vpcDiags...)
	if diags.HasError() {
		return body, diags
	}

	body.Name = strings.TrimSpace(plan.Name.ValueString())
	body.Cidr = plan.CIDR.ValueString()
	body.VpcId = vpcID
	if !plan.Description.IsNull() && !plan.Description.IsUnknown() {
		description := plan.Description.ValueString()
		body.Description = &description
	}
	return body, diags
}

func buildSubnetUpdateBody(
	plan SubnetResourceModel,
	state SubnetResourceModel,
) (networksdk.SubnetPartialUpdateSchema, bool, diag.Diagnostics) {
	var body networksdk.SubnetPartialUpdateSchema
	var diags diag.Diagnostics
	changed := false

	if plan.Name.IsNull() || plan.Name.IsUnknown() {
		diags.AddError("Missing subnet name", "name must be configured and known.")
		return body, changed, diags
	}
	desiredName := strings.TrimSpace(plan.Name.ValueString())
	if state.Name.IsNull() || state.Name.IsUnknown() || strings.TrimSpace(state.Name.ValueString()) != desiredName {
		body.Name = &desiredName
		changed = true
	}

	if !plan.Description.IsNull() && !plan.Description.IsUnknown() &&
		(state.Description.IsNull() || state.Description.IsUnknown() || !plan.Description.Equal(state.Description)) {
		description := plan.Description.ValueString()
		body.Description = &description
		changed = true
	}
	return body, changed, diags
}
