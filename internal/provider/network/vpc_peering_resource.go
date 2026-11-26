package network

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/viettelcloud-oss/sdks/go/core"
	networksdk "github.com/viettelcloud-oss/sdks/go/network"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/compare"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/parse"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/replace"
)

var (
	_ resource.Resource                = &VPCPeeringResource{}
	_ resource.ResourceWithImportState = &VPCPeeringResource{}
)

type VPCPeeringResource struct {
	client    *networksdk.Client
	projectID core.UUID
}

func NewVPCPeeringResource() resource.Resource {
	return &VPCPeeringResource{}
}

func (r *VPCPeeringResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vpc_peering"
}

func (r *VPCPeeringResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manage a Viettel Cloud VPC peering from the requester side. " +
			"A peering with a VPC of another project, including a VPC shared into this project, " +
			"stays `pending` until that project accepts it outside Terraform. " +
			"Traffic flows only after each VPC has a route to the other VPC through the peering. " +
			"Replacing or destroying a peering also deletes every route that uses it as the next hop.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "VPC peering ID (UUID).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"requester_vpc": schema.SingleNestedAttribute{
				Required:    true,
				Description: "VPC that requests the peering. It must belong to the provider project. Changing it replaces the peering.",
				Attributes:  reference.Attributes(reference.ID, "VPC ID (UUID)."),
				PlanModifiers: []planmodifier.Object{
					replace.Reference(reference.ID, compare.SameUUIDString),
				},
			},
			"accepter_vpc": schema.SingleNestedAttribute{
				Required:    true,
				Description: "VPC that accepts the peering. It can belong to any project. Changing it replaces the peering.",
				Attributes:  reference.Attributes(reference.ID, "VPC ID (UUID)."),
				PlanModifiers: []planmodifier.Object{
					replace.Reference(reference.ID, compare.SameUUIDString),
				},
			},
			"description": schema.StringAttribute{
				Optional: true,
				Computed: true,
				Description: "Description of the peering, at most 255 characters. Omitting it preserves the current value; " +
					"set it to an empty string to clear it.",
			},
			"name": schema.StringAttribute{
				Computed:    true,
				Description: "Name assigned to the peering at creation.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"status": schema.StringAttribute{
				Computed:    true,
				Description: "Peering status: `pending`, `active`, `rejected`, or `error`.",
			},
			"vpc_peering_type": schema.StringAttribute{
				Computed:    true,
				Description: "`intra_region` when both VPCs are in one region, otherwise `inter_region`.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"accepter_project_id": schema.StringAttribute{
				Computed:    true,
				Description: "ID (UUID) of the project that owns the accepter VPC.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"created_at": schema.StringAttribute{
				Computed:    true,
				Description: "Timestamp when the peering was created (RFC3339).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"updated_at": schema.StringAttribute{
				Computed:    true,
				Description: "Timestamp when the peering was last updated (RFC3339).",
			},
		},
	}
}

func (r *VPCPeeringResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// Create maps the create response directly into state. The backend builds the
// peering within the request, so a same-project peering is already active and
// a cross-project peering stays pending until the other project accepts it.
func (r *VPCPeeringResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan VPCPeeringResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	body, diags := buildVPCPeeringCreateBody(plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	peering, err := r.client.CreateVpcPeering(ctx, networksdk.CreateVpcPeeringParams{ProjectID: r.projectID}, body)
	if err != nil {
		resp.Diagnostics.AddError("Error creating VPC peering", err.Error())
		return
	}

	populateVPCPeeringResourceState(peering, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *VPCPeeringResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state VPCPeeringResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	peeringID, diags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	peering, err := r.client.GetVpcPeering(ctx, peeringID, networksdk.GetVpcPeeringParams{ProjectID: r.projectID})
	if err != nil {
		if errors.Is(err, networksdk.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading VPC peering", err.Error())
		return
	}

	populateVPCPeeringResourceState(peering, &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *VPCPeeringResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan VPCPeeringResourceModel
	var state VPCPeeringResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	peeringID, diags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	peering, err := r.client.UpdateVpcPeering(
		ctx,
		peeringID,
		networksdk.UpdateVpcPeeringParams{ProjectID: r.projectID},
		buildVPCPeeringUpdateBody(plan, state),
	)
	if err != nil {
		resp.Diagnostics.AddError("Error updating VPC peering", err.Error())
		return
	}

	newState := plan
	populateVPCPeeringResourceState(peering, &newState)
	resp.Diagnostics.Append(resp.State.Set(ctx, &newState)...)
}

// Delete returns after the backend removes the peering and every route that
// uses it, so no wait follows the request.
func (r *VPCPeeringResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state VPCPeeringResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	peeringID, diags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.DeleteVpcPeering(ctx, peeringID, networksdk.DeleteVpcPeeringParams{ProjectID: r.projectID})
	if err != nil && !errors.Is(err, networksdk.ErrNotFound) {
		resp.Diagnostics.AddError("Error deleting VPC peering", err.Error())
	}
}

func (r *VPCPeeringResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func buildVPCPeeringCreateBody(plan VPCPeeringResourceModel) (networksdk.VPCPeeringCreateSchema, diag.Diagnostics) {
	var body networksdk.VPCPeeringCreateSchema
	var diags diag.Diagnostics

	requesterID, requesterDiags := parse.UUIDString(reference.Get(plan.RequesterVPC, reference.ID), "requester_vpc.id")
	diags.Append(requesterDiags...)
	accepterID, accepterDiags := parse.UUIDString(reference.Get(plan.AccepterVPC, reference.ID), "accepter_vpc.id")
	diags.Append(accepterDiags...)
	if diags.HasError() {
		return body, diags
	}

	body.RequesterVpcId = requesterID
	body.AccepterVpcId = accepterID
	if !plan.Description.IsNull() && !plan.Description.IsUnknown() {
		description := strings.TrimSpace(plan.Description.ValueString())
		body.Description = &description
	}
	return body, diags
}

// buildVPCPeeringUpdateBody always sets description, because the PUT body
// requires it. If the plan value is not known, it sends the prior value, so
// that the required field never clears the description. State keeps the
// configured spelling, so the prior value is trimmed too.
func buildVPCPeeringUpdateBody(plan, state VPCPeeringResourceModel) networksdk.VPCPeeringUpdateSchema {
	description := state.Description
	if !plan.Description.IsNull() && !plan.Description.IsUnknown() {
		description = plan.Description
	}
	return networksdk.VPCPeeringUpdateSchema{Description: strings.TrimSpace(description.ValueString())}
}
