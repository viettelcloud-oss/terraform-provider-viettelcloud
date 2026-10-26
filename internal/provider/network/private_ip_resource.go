package network

import (
	"context"
	"errors"
	"fmt"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/compare"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/parse"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
	networksdk "github.com/viettelcloud-oss/sdks/go/network"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
)

var (
	_ resource.Resource                = &PrivateIPResource{}
	_ resource.ResourceWithImportState = &PrivateIPResource{}
)

type PrivateIPResource struct {
	client    *networksdk.Client
	projectID core.UUID
}

func NewPrivateIPResource() resource.Resource {
	return &PrivateIPResource{}
}

func (r *PrivateIPResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_private_ip"
}

func (r *PrivateIPResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manage a Viettel Cloud private IP.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Private IP ID (UUID).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"description": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Optional description of the private IP. Omitting it preserves the backend value; set it to an empty string to clear it.",
			},
			"ip_address": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Private IP address. When omitted during creation, the platform allocates an address from the subnet; removing it from configuration preserves the assigned address.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplaceIfConfigured(),
				},
			},
			"mac_address": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "MAC address. When omitted during creation, the platform allocates one; removing it from configuration preserves the assigned address.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplaceIfConfigured(),
				},
			},
			"subnet": schema.SingleNestedAttribute{
				Required:    true,
				Description: "Subnet where the private IP is allocated.",
				Attributes:  reference.Attributes(reference.ID, "Subnet ID (UUID)."),
				PlanModifiers: []planmodifier.Object{
					objectplanmodifier.RequiresReplace(),
				},
			},
			"subnet_name": schema.StringAttribute{
				Computed:    true,
				Description: "Name of the subnet containing the private IP.",
			},
			"subnet_cidr": schema.StringAttribute{
				Computed:    true,
				Description: "CIDR of the subnet containing the private IP.",
			},
			"vpc_id": schema.StringAttribute{
				Computed:    true,
				Description: "VPC ID (UUID) containing the private IP.",
			},
			"vpc_name": schema.StringAttribute{
				Computed:    true,
				Description: "Name of the VPC containing the private IP.",
			},
			"region": schema.StringAttribute{
				Computed:    true,
				Description: "Region name inherited from the subnet.",
			},
			"server_id": schema.StringAttribute{
				Computed:    true,
				Description: "ID of the attached server, when present.",
			},
			"server_name": schema.StringAttribute{
				Computed:    true,
				Description: "Name of the attached server, when present.",
			},
			"device_owner": schema.StringAttribute{
				Computed:    true,
				Description: "Device owner classification assigned by the platform.",
			},
			"display_name": schema.StringAttribute{
				Computed:    true,
				Description: "Display name assigned by the platform.",
			},
			"port_security": schema.BoolAttribute{
				Computed:    true,
				Description: "Whether port security is enabled.",
			},
			"allowed_cidrs": schema.SetAttribute{
				Computed:    true,
				ElementType: types.StringType,
				Description: "CIDRs currently allowed by the platform for this private IP.",
			},
			"allowed_vips": schema.SetNestedAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Full set of private IPs allowed as VIPs. Omitting it preserves the backend set; use an empty set to clear all allowed VIPs.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: reference.PreservedAttributes(reference.ID, "Allowed VIP private IP ID (UUID)."),
				},
			},
			"attached_vips": schema.SetNestedAttribute{
				Computed:    true,
				Description: "Private IPs attached as allowed VIPs.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.StringAttribute{
							Computed:    true,
							Description: "Allowed VIP private IP ID (UUID).",
						},
						"ip_address": schema.StringAttribute{
							Computed:    true,
							Description: "Allowed VIP address.",
						},
					},
				},
			},
			"created_at": schema.StringAttribute{
				Computed:    true,
				Description: "Timestamp when the private IP was created (RFC3339).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"updated_at": schema.StringAttribute{
				Computed:    true,
				Description: "Timestamp when the private IP was last updated (RFC3339).",
			},
		},
	}
}

func (r *PrivateIPResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *PrivateIPResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan PrivateIPResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	body, diags := buildPrivateIPCreateBody(plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	allowedVIPsBody, allowedVIPsConfigured, diags := buildPrivateIPAllowedVIPsBody(plan.AllowedVIPs)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	privateIP, err := r.client.CreatePrivateIp(ctx, networksdk.CreatePrivateIpParams{
		ProjectID: r.projectID,
	}, body)
	if err != nil {
		resp.Diagnostics.AddError("Error creating private IP", err.Error())
		return
	}
	if allowedVIPsConfigured {
		updatedPrivateIP, updateErr := r.client.UpdatePrivateIpAllowedVips(ctx, privateIP.Id, networksdk.UpdatePrivateIpAllowedVipsParams{
			ProjectID: r.projectID,
		}, allowedVIPsBody)
		if updateErr != nil {
			populatePrivateIPResourceState(privateIP, &plan)
			resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
			resp.Diagnostics.AddError("Error configuring private IP allowed VIPs", updateErr.Error())
			return
		}
		privateIP = updatedPrivateIP
	}

	populatePrivateIPResourceState(privateIP, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PrivateIPResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state PrivateIPResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	privateIPID, diags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	privateIP, err := r.client.GetPrivateIp(ctx, privateIPID, networksdk.GetPrivateIpParams{
		ProjectID: r.projectID,
	})
	if err != nil {
		if errors.Is(err, networksdk.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading private IP", err.Error())
		return
	}

	populatePrivateIPResourceState(privateIP, &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *PrivateIPResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan PrivateIPResourceModel
	var state PrivateIPResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	privateIPID, diags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	body, descriptionChanged := buildPrivateIPUpdateBody(plan, state)
	allowedVIPsBody, allowedVIPsChanged, diags := buildPrivateIPAllowedVIPsUpdateBody(plan, state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	var privateIP *networksdk.PrivateIPSchema
	var err error
	if descriptionChanged {
		privateIP, err = r.client.PartialUpdatePrivateIp(ctx, privateIPID, networksdk.PartialUpdatePrivateIpParams{
			ProjectID: r.projectID,
		}, body)
		if err != nil {
			resp.Diagnostics.AddError("Error updating private IP", err.Error())
			return
		}
	}
	if allowedVIPsChanged {
		updatedPrivateIP, updateErr := r.client.UpdatePrivateIpAllowedVips(ctx, privateIPID, networksdk.UpdatePrivateIpAllowedVipsParams{
			ProjectID: r.projectID,
		}, allowedVIPsBody)
		if updateErr != nil {
			if privateIP != nil {
				newState := plan
				populatePrivateIPResourceState(privateIP, &newState)
				resp.Diagnostics.Append(resp.State.Set(ctx, &newState)...)
			}
			resp.Diagnostics.AddError("Error updating private IP allowed VIPs", updateErr.Error())
			return
		}
		privateIP = updatedPrivateIP
	}
	if !descriptionChanged && !allowedVIPsChanged {
		privateIP, err = r.client.GetPrivateIp(ctx, privateIPID, networksdk.GetPrivateIpParams{
			ProjectID: r.projectID,
		})
	}
	if err != nil {
		resp.Diagnostics.AddError("Error updating private IP", err.Error())
		return
	}

	newState := plan
	populatePrivateIPResourceState(privateIP, &newState)
	resp.Diagnostics.Append(resp.State.Set(ctx, &newState)...)
}

func (r *PrivateIPResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state PrivateIPResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	privateIPID, diags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.DeletePrivateIp(ctx, privateIPID, networksdk.DeletePrivateIpParams{
		ProjectID: r.projectID,
	})
	if err != nil && !errors.Is(err, networksdk.ErrNotFound) {
		resp.Diagnostics.AddError("Error deleting private IP", err.Error())
	}
}

func (r *PrivateIPResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func buildPrivateIPCreateBody(plan PrivateIPResourceModel) (networksdk.PrivateIPCreateSchema, diag.Diagnostics) {
	var body networksdk.PrivateIPCreateSchema
	var diags diag.Diagnostics

	subnetID, subnetDiags := parse.UUIDString(reference.Get(plan.Subnet, reference.ID), "subnet.id")
	diags.Append(subnetDiags...)
	if diags.HasError() {
		return body, diags
	}
	body.SubnetId = subnetID

	if !plan.Description.IsNull() && !plan.Description.IsUnknown() {
		description := plan.Description.ValueString()
		body.Description = &description
	}
	if !plan.IPAddress.IsNull() && !plan.IPAddress.IsUnknown() {
		ipAddress := plan.IPAddress.ValueString()
		body.IpAddress = &ipAddress
	}
	if !plan.MACAddress.IsNull() && !plan.MACAddress.IsUnknown() {
		macAddress := plan.MACAddress.ValueString()
		body.MacAddress = &macAddress
	}
	return body, diags
}

func buildPrivateIPUpdateBody(plan, state PrivateIPResourceModel) (networksdk.PrivateIPPartialUpdateSchema, bool) {
	var body networksdk.PrivateIPPartialUpdateSchema
	if plan.Description.IsNull() || plan.Description.IsUnknown() || plan.Description.Equal(state.Description) {
		return body, false
	}

	description := plan.Description.ValueString()
	body.Description = &description
	return body, true
}

func buildPrivateIPAllowedVIPsBody(value types.Set) (networksdk.PrivateIPAllowedVIPsUpdateSchema, bool, diag.Diagnostics) {
	var body networksdk.PrivateIPAllowedVIPsUpdateSchema
	var diags diag.Diagnostics
	if value.IsNull() || value.IsUnknown() {
		return body, false, diags
	}

	ids := reference.Strings(value, reference.ID)
	body.AllowedVipIds = make([]core.UUID, 0, len(ids))
	for i, stringValue := range ids {
		id, idDiags := parse.UUIDString(stringValue, fmt.Sprintf("allowed_vips[%d].id", i))
		diags.Append(idDiags...)
		if !idDiags.HasError() {
			body.AllowedVipIds = append(body.AllowedVipIds, id)
		}
	}
	return body, true, diags
}

func buildPrivateIPAllowedVIPsUpdateBody(
	plan, state PrivateIPResourceModel,
) (networksdk.PrivateIPAllowedVIPsUpdateSchema, bool, diag.Diagnostics) {
	body, configured, diags := buildPrivateIPAllowedVIPsBody(plan.AllowedVIPs)
	if diags.HasError() || !configured || compare.SameUUIDSet(privateIPAllowedVIPIDSet(plan.AllowedVIPs), privateIPAllowedVIPIDSet(state.AllowedVIPs)) {
		return body, false, diags
	}
	return body, true, diags
}
