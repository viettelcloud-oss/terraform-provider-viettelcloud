package server

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	blockstorageselector "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/blockstorage/selector"
	serverselector "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/server/selector"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/selector"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	blockstoragesdk "github.com/viettelcloud-oss/sdks/go/blockstorage"
	"github.com/viettelcloud-oss/sdks/go/core"
	networksdk "github.com/viettelcloud-oss/sdks/go/network"
	projectsdk "github.com/viettelcloud-oss/sdks/go/project"
	serversdk "github.com/viettelcloud-oss/sdks/go/server"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/parse"
	blockstoragelookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/blockstorage/lookup"
	networklookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/network/lookup"
	projectlookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/project/lookup"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
	serverlookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/server/lookup"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/wait"
)

const (
	serverOperationTimeout = 30 * time.Minute
	serverPollInterval     = 10 * time.Second
	// transientBackendErrorCode identifies a backend refusal that may succeed
	// when retried after the upstream service recovers.
	transientBackendErrorCode = "provider_error"
)

// elasticIPAttachTimeout bounds retries after a server-side attach refusal.
// It is shorter than serverOperationTimeout because an attach still refused
// minutes later is a standing problem rather than an operation in flight.
const elasticIPAttachTimeout = 2 * time.Minute

// privateIPDeleteInterval and privateIPDeleteTimeout bound private IP cleanup.
// This network operation uses a shorter interval and timeout than server
// lifecycle operations.
const (
	privateIPDeleteInterval = 2 * time.Second
	privateIPDeleteTimeout  = 5 * time.Minute
)

// volumeDetachInterval and volumeDetachTimeout bound the wait for a deleted
// server's volumes to leave in-use. The backend detaches them in the background
// after the server is gone, so this covers a detach, not a server operation.
const (
	volumeDetachInterval = 5 * time.Second
	volumeDetachTimeout  = 5 * time.Minute
)

var (
	_ resource.Resource                   = &ServerResource{}
	_ resource.ResourceWithImportState    = &ServerResource{}
	_ resource.ResourceWithValidateConfig = &ServerResource{}
	_ resource.ResourceWithModifyPlan     = &ServerResource{}
)

type ServerResource struct {
	client             *serversdk.Client
	blockStorageClient *blockstoragesdk.Client
	networkClient      *networksdk.Client
	projectClient      *projectsdk.Client
	projectID          core.UUID
}

// serverUpdateRequest carries every mutation an update applies, already built
// and validated, so the mutating sequence performs no further plan decoding.
type serverUpdateRequest struct {
	patchServer       bool
	patchBody         serversdk.ServerPartialUpdateSchema
	rebuild           bool
	rebuildBody       serversdk.ServerRebuildSchema
	rebuiltImageID    core.UUID
	resize            bool
	resizeBody        serversdk.ServerResizeSchema
	updateBandwidth   bool
	bandwidth         int
	replaceGroups     bool
	securityGroupBody serversdk.ServerUpdateSecurityGroupSchema
	attachments       attachmentUpdatePlan
	changePowerState  bool
	desiredPowerState types.String
	priorPowerState   types.String
}

type privateIPUpdatePlan struct {
	expectedCount int
	desiredIDs    map[core.UUID]struct{}
	removedIDs    map[core.UUID]struct{}
	deletedIDs    []core.UUID
	existingIDs   []core.UUID
	subnetIDs     []core.UUID
}

type elasticIPUpdatePlan struct {
	expectedCount int
	desiredIDs    map[core.UUID]struct{}
	removedIDs    map[core.UUID]struct{}
	deletedIDs    []core.UUID
	existingIDs   []core.UUID
	newConfigs    []ElasticIPInputModel
	regionID      core.UUID
}

type bootVolumeUpdatePlan struct {
	resizeID   core.UUID
	resizeSize int
}

type dataVolumeUpdatePlan struct {
	resizeIDs      []core.UUID
	resizeSizes    []int
	desiredIDs     map[core.UUID]struct{}
	removedIDs     map[core.UUID]struct{}
	deletedIDs     []core.UUID
	existingIDs    []core.UUID
	newBodies      []blockstoragesdk.VolumeCreateSchema
	newIndexes     []int
	planned        []DataVolumeInputModel
	updates        []dataVolumePropertyUpdate
	cleanupConfigs map[core.UUID]DataVolumeInputModel
	detachConfigs  []DataVolumeInputModel
	recordCreated  func(DataVolumeInputModel) diag.Diagnostics
}

type attachmentUpdatePlan struct {
	private    *privateIPUpdatePlan
	elastic    *elasticIPUpdatePlan
	boot       *bootVolumeUpdatePlan
	dataVolume *dataVolumeUpdatePlan
}

// privateIPStatePlanModifier and elasticIPStatePlanModifier carry the computed
// half of an unchanged attachment forward from state. Both lists mix
// practitioner configuration with values only the backend can supply. A plan
// must therefore keep the computed values that an unchanged attachment already
// has, and leave the rest unknown. Otherwise Terraform either reports drift on
// every plan or promises values the apply cannot honor.
type (
	privateIPStatePlanModifier struct{}
	elasticIPStatePlanModifier struct{}
)

// zoneResolveFunc and subnetResolveFunc resolve the human-readable references
// that a create request carries to SDK IDs. Injection lets request builders run
// without a live client, as with the lookup packages' resolve functions.
type (
	zoneResolveFunc   func(context.Context, string) (projectsdk.ProjectZoneSchema, error)
	subnetResolveFunc func(context.Context, string) (networksdk.SubnetSchema, error)
)

type dataVolumeStatePlanModifier struct{}

type bootIOPSStatePlanModifier struct{}

type dataVolumePropertyUpdate struct {
	id           core.UUID
	iops         *int
	volumeTypeID core.UUID
}

func NewServerResource() resource.Resource {
	return &ServerResource{}
}

func (r *ServerResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_server"
}

func (r *ServerResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	imageSelector := serverselector.ImageAttribute()
	imageSelector.Description += " Required for image and local_disk boot types. Selecting a different image rebuilds the server in place."
	bootVolumeType := blockstorageselector.VolumeTypeAttribute()
	bootVolumeType.Description += " Required for image and custom_image boot types. Selecting a different volume type replaces the server."

	dataVolumeType := blockstorageselector.VolumeTypeAttribute()
	dataVolumeType.Computed = true
	dataVolumeType.Description += " Required for kind new. Selecting a different type updates the volume in place. The backend supplies the type for kind existing."
	for name, attribute := range dataVolumeType.Attributes {
		switch attribute := attribute.(type) {
		case schema.StringAttribute:
			attribute.Computed = true
			dataVolumeType.Attributes[name] = attribute
		case schema.BoolAttribute:
			attribute.Computed = true
			dataVolumeType.Attributes[name] = attribute
		}
	}

	resp.Schema = schema.Schema{
		Description: "Manage a Viettel Cloud server.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
				Description: "Server ID (UUID).",
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Server name.",
			},
			"description": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Optional server description. Omitting it preserves the backend value; set an empty string to clear it.",
			},
			"boot": schema.SingleNestedAttribute{
				Required:    true,
				Description: "Boot configuration. Supported boot types are image, custom_image, local_disk, and volume.",
				Attributes: map[string]schema.Attribute{
					"boot_type": schema.StringAttribute{
						Required: true, Description: "Boot source type.",
						PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
					},
					"image": imageSelector,
					"custom_image": schema.SingleNestedAttribute{
						Optional:    true,
						Description: "Custom image to boot from. Required for the custom_image boot type.",
						Attributes:  reference.Attributes(reference.ID, "Custom image ID (UUID)."),
					},
					"volume": schema.SingleNestedAttribute{
						Optional:      true,
						Description:   "Existing boot volume. Required for the volume boot type.",
						Attributes:    reference.Attributes(reference.ID, "Volume ID (UUID)."),
						PlanModifiers: []planmodifier.Object{objectplanmodifier.RequiresReplace()},
					},
					"volume_type": bootVolumeType,
					"volume_size": schema.Int64Attribute{
						Optional: true, Description: "Server-created boot volume size in GB. Updates can only increase it.",
					},
					"iops": schema.Int64Attribute{
						Optional: true, Computed: true,
						Description: "Requested boot volume IOPS. The backend allocates a value when it is omitted. Size updates can change unconfigured IOPS.",
						// The backend allocates IOPS from the volume type and size when
						// the attribute is omitted, so an omitted value must keep the
						// allocated state instead of planning a replacement.
						PlanModifiers: []planmodifier.Int64{bootIOPSStatePlanModifier{}, int64planmodifier.RequiresReplaceIfConfigured()},
					},
					"delete_on_termination": schema.BoolAttribute{
						Optional: true,
						Computed: true,
						Description: "Delete the boot volume after deleting the server. Defaults to true for boot types " +
							"image and custom_image, and to false for boot type volume.",
						PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
					},
				},
			},
			"flavor": schema.SingleNestedAttribute{
				Required:    true,
				Description: "Predefined or custom server flavor.",
				Attributes: map[string]schema.Attribute{
					"kind": schema.StringAttribute{Required: true, Description: "Flavor kind: predefined or custom."},
					"name": schema.StringAttribute{Optional: true, Description: "Exact predefined flavor name."},
					"family": schema.StringAttribute{
						Optional:    true,
						Description: "Custom flavor family: basic, premium, enterprise, gpu, or spot.",
					},
					"vcpus": schema.Int64Attribute{Optional: true, Description: "Custom flavor virtual CPU count."},
					"ram":   schema.Int64Attribute{Optional: true, Description: "Custom flavor RAM in GiB."},
				},
			},
			"zone": schema.SingleNestedAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Project zone that hosts the server.",
				Attributes:  reference.PreservedAttributes(reference.Name, "Exact zone name."),
				PlanModifiers: []planmodifier.Object{
					objectplanmodifier.RequiresReplaceIfConfigured(),
				},
			},
			"key_pair": schema.SingleNestedAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Key pair associated with the server.",
				Attributes:  reference.PreservedAttributes(reference.ID, "Key pair ID (UUID)."),
				PlanModifiers: []planmodifier.Object{
					objectplanmodifier.UseStateForUnknown(),
					objectplanmodifier.RequiresReplaceIfConfigured(),
				},
			},
			"key_pair_name": schema.StringAttribute{
				Computed:    true,
				Description: "Key pair name associated with the server.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"placement_group": schema.SingleNestedAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Placement group associated with the server.",
				Attributes:  reference.PreservedAttributes(reference.ID, "Placement group ID (UUID)."),
				PlanModifiers: []planmodifier.Object{
					objectplanmodifier.UseStateForUnknown(),
					objectplanmodifier.RequiresReplaceIfConfigured(),
				},
			},
			"placement_group_name": schema.StringAttribute{
				Computed:    true,
				Description: "Placement group name associated with the server.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"data_volumes": schema.ListNestedAttribute{
				Optional: true, Computed: true,
				Description:   "Data volume attachments. kind new creates a volume, and kind existing attaches a volume by ID. Omission preserves attachments. An empty list detaches all data volumes.",
				PlanModifiers: []planmodifier.List{dataVolumeStatePlanModifier{}},
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"kind":                  schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("new"), Description: "Attachment kind: new or existing. Defaults to new for compatibility with earlier configurations."},
					"attached":              schema.BoolAttribute{Computed: true, Description: "Whether the volume currently attaches to this server. A failed operation can leave a new volume allocated but detached."},
					"id":                    schema.StringAttribute{Optional: true, Computed: true, Description: "Volume ID (UUID). Required for kind existing. For kind new, omit it on create. Set a prior volume ID to identify it during updates."},
					"volume_type":           dataVolumeType,
					"volume_size":           schema.Int64Attribute{Optional: true, Computed: true, Description: "Volume size in GB. Required for kind new. Updates can only increase it. For kind existing, the backend supplies this value."},
					"iops":                  schema.Int64Attribute{Optional: true, Computed: true, Description: "Requested IOPS for kind new. Updates keep the volume ID. The backend supplies the effective value when omitted. Size or type updates can change unconfigured IOPS. Omit it for kind existing."},
					"delete_on_termination": schema.BoolAttribute{Optional: true, Computed: true, Description: "Delete the volume with the server. Defaults to true for kind new and false for kind existing. Removing a new attachment also deletes its volume when true. Removing an existing attachment only detaches it. Omitting this flag preserves its last value."},
				}},
			},
			"private_ips": schema.ListNestedAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Private IP attachments. kind subnet creates an IP; kind ip attaches an existing IP.",
				PlanModifiers: []planmodifier.List{
					preservePrivateIPState(),
				},
				// Every input is also Computed, because Terraform plans an omitted
				// list as null instead of its prior value when that value holds a
				// non-Computed attribute. preservePrivateIPState plans configured
				// inputs as written.
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"kind": schema.StringAttribute{
						Optional:    true,
						Computed:    true,
						Description: "Attachment kind: subnet or ip. Required for every attachment.",
					},
					"id": schema.StringAttribute{
						Optional:    true,
						Computed:    true,
						Description: "Private IP ID. Required when kind is ip; computed when kind is subnet.",
					},
					"subnet": schema.SingleNestedAttribute{
						Optional:    true,
						Computed:    true,
						Description: "Subnet that allocates the private IP. Required when kind is subnet; must be omitted when kind is ip.",
						Attributes: map[string]schema.Attribute{
							reference.ID: schema.StringAttribute{
								Optional:    true,
								Computed:    true,
								Description: "Subnet ID (UUID). Configure exactly one of id or cidr.",
							},
							subnetCIDR: schema.StringAttribute{
								Optional:    true,
								Computed:    true,
								Description: "Exact subnet CIDR. Configure exactly one of id or cidr.",
							},
						},
					},
					"delete_on_termination": schema.BoolAttribute{
						Optional: true,
						Computed: true,
						Description: "Delete this private IP after deleting the server or removing the attachment from configuration. " +
							"Defaults to true for kind subnet and false for kind ip.",
					},
					"ip_address":  schema.StringAttribute{Computed: true},
					"mac_address": schema.StringAttribute{Computed: true},
				}},
			},
			"elastic_ips": schema.ListNestedAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Elastic IP attachments. kind existing attaches an IP; kind new creates an IP.",
				PlanModifiers: []planmodifier.List{
					preserveElasticIPState(),
				},
				// Every input is also Computed for the reason given on private_ips.
				// preserveElasticIPState plans configured inputs as written.
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"kind": schema.StringAttribute{
						Optional:    true,
						Computed:    true,
						Description: "Attachment kind: existing or new. Required for every attachment.",
					},
					"id": schema.StringAttribute{
						Optional:    true,
						Computed:    true,
						Description: "Elastic IP ID. Required when kind is existing; computed when kind is new.",
					},
					"enable_ipv4": schema.BoolAttribute{
						Optional:    true,
						Computed:    true,
						Description: "Enable IPv4 when kind is new. Defaults to true.",
					},
					"enable_ipv6": schema.BoolAttribute{
						Optional:    true,
						Computed:    true,
						Description: "Enable IPv6 when kind is new. Defaults to false.",
					},
					"delete_on_termination": schema.BoolAttribute{
						Optional: true,
						Computed: true,
						Description: "Delete this elastic IP after deleting the server or removing the attachment from configuration. " +
							"Defaults to true for kind new and false for kind existing.",
					},
					"ip_address":   schema.StringAttribute{Computed: true},
					"ipv6_address": schema.StringAttribute{Computed: true},
					"status":       schema.StringAttribute{Computed: true},
				}},
			},
			"user_data": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Sensitive:   true,
				Description: "Cloud-init user data.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
					stringplanmodifier.RequiresReplaceIfConfigured(),
				},
			},
			"bandwidth": schema.Int64Attribute{
				Optional: true,
				Computed: true,
				Description: "Server bandwidth in Mbps. The backend enforces the supported range and rejects a " +
					"value below its minimum, so omit it to accept the backend default.",
			},
			"security_groups": schema.SetNestedAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Security groups attached to the server. Configure an empty set to detach all security groups.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: reference.PreservedAttributes(reference.ID, "Security group ID (UUID)."),
				},
			},
			"quantity": schema.Int64Attribute{
				Optional:    true,
				Computed:    true,
				Default:     int64default.StaticInt64(1),
				Description: "Number of servers to create. A Terraform server resource manages exactly one server, so only 1 is supported.",
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.RequiresReplace(),
				},
			},
			"power_state": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Desired power state. Supported values are running and shutdown.",
			},
			"status":          schema.StringAttribute{Computed: true, Description: "Current lifecycle status."},
			"server_type":     schema.StringAttribute{Computed: true, Description: "Backend server type."},
			"data_volume_ids": schema.ListAttribute{Computed: true, ElementType: types.StringType, Description: "Attached data volume IDs."},
			"created_at":      schema.StringAttribute{Computed: true, Description: "Creation timestamp (RFC3339)."},
		},
	}
}

func (r *ServerResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	data, ok := req.ProviderData.(*providerdata.Configured)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data type", fmt.Sprintf("Expected *providerdata.Configured, got %T", req.ProviderData))
		return
	}
	r.client = data.Server
	r.blockStorageClient = data.BlockStorage
	r.networkClient = data.Network
	r.projectClient = data.Project
	r.projectID = data.ProjectID
}

// ValidateConfig reports the structural mistakes a practitioner can make in a
// server configuration while Terraform is still planning. Create and Update
// repeat these checks because a value can be unknown until apply, but reporting
// them here avoids creating or mutating a server that cannot succeed.
func (r *ServerResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config ServerResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	validateServerQuantity(config.Quantity, &resp.Diagnostics)
	validateServerPowerState(config.PowerState, &resp.Diagnostics)
	validateServerFlavor(ctx, config.Flavor, &resp.Diagnostics)
	validateServerBoot(ctx, config.Boot, &resp.Diagnostics)
	validateServerPrivateIPs(ctx, config.PrivateIPs, &resp.Diagnostics)
	validateServerElasticIPs(ctx, config.ElasticIPs, &resp.Diagnostics)
	validateServerDataVolumes(ctx, config.DataVolumes, &resp.Diagnostics)
	validateServerUUID(reference.Get(config.KeyPair, reference.ID), path.Root("key_pair").AtName(reference.ID), "key_pair.id", &resp.Diagnostics)
	validateServerUUID(
		reference.Get(config.PlacementGroup, reference.ID),
		path.Root("placement_group").AtName(reference.ID),
		"placement_group.id",
		&resp.Diagnostics,
	)
}

// ModifyPlan removes computed-only updates after the attribute modifiers have
// restored unchanged attachment and volume state. Import can initially propose
// different computed values, causing Framework to mark all runtime outputs
// unknown before those modifiers run. Preserve them only when restoring the
// unconfigured computed outputs makes the entire plan identical to refreshed
// state. Any configured change or unknown input keeps the normal update plan.
func (r *ServerResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
		return
	}
	r.planSelectors(ctx, req, resp)
	if resp.Diagnostics.HasError() || !req.Config.Raw.IsFullyKnown() || len(resp.RequiresReplace) > 0 {
		return
	}
	candidate := resp.Plan
	for name, attribute := range resp.Plan.Schema.GetAttributes() {
		if !attribute.IsComputed() {
			continue
		}
		p := path.Root(name)
		var configured, planned, prior attr.Value
		resp.Diagnostics.Append(req.Config.GetAttribute(ctx, p, &configured)...)
		resp.Diagnostics.Append(resp.Plan.GetAttribute(ctx, p, &planned)...)
		resp.Diagnostics.Append(req.State.GetAttribute(ctx, p, &prior)...)
		if resp.Diagnostics.HasError() {
			return
		}
		if configured.IsNull() && planned.IsUnknown() {
			resp.Diagnostics.Append(candidate.SetAttribute(ctx, p, prior)...)
		}
	}
	if !resp.Diagnostics.HasError() && candidate.Raw.Equal(req.State.Raw) {
		resp.Plan = candidate
	}
}

func (r *ServerResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan ServerResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	desiredPowerState := plan.PowerState
	resp.Diagnostics.Append(validateDesiredPowerState(desiredPowerState)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// Keep the planned attachment lists before populateServerPendingState
	// rewrites them. It turns a list that the practitioner left unset into a known
	// empty one, which would otherwise read as "the plan fixed zero
	// attachments".
	plannedPrivateIPs, plannedElasticIPs := plan.PrivateIPs, plan.ElasticIPs

	body, diags := r.buildCreateBody(ctx, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(recordServerSelectorIDs(ctx, body, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	created, err := r.client.CreateServer(ctx, serversdk.CreateServerParams{ProjectID: r.projectID}, body)
	if err != nil {
		resp.Diagnostics.AddError("Error creating server", err.Error())
		return
	}
	if created == nil || len(created.ServerIds) != 1 {
		resp.Diagnostics.AddError("Error creating server", "The API must return exactly one server ID.")
		return
	}

	serverID := created.ServerIds[0]
	resp.Diagnostics.Append(populateServerPendingState(ctx, &plan, serverID)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.waitUntilReady(ctx, serverID); err != nil {
		resp.Diagnostics.AddError(
			"Error waiting for server to be ready",
			fmt.Sprintf("%s\n\nServer ID %s was preserved in Terraform state to avoid creating an untracked duplicate.", err, serverID),
		)
		return
	}
	if isDesiredPowerState(desiredPowerState, serversdk.ServerPowerStateShutdown) {
		if err := r.requestServerOperation(ctx, serverID, func(ctx context.Context) error {
			return r.client.StopServer(ctx, serverID, serversdk.StopServerParams{ProjectID: r.projectID})
		}); err != nil {
			resp.Diagnostics.AddError("Error stopping server after creation", err.Error())
			return
		}
		if _, err := r.waitUntilShutdown(ctx, serverID); err != nil {
			resp.Diagnostics.AddError("Error waiting for server shutdown after creation", err.Error())
			return
		}
	}
	server, err := r.waitUntilAttachmentsSettled(ctx, serverID, plannedPrivateIPs, plannedElasticIPs)
	if err != nil {
		resp.Diagnostics.AddError("Error reading server after creation", err.Error())
		return
	}
	if server == nil {
		resp.Diagnostics.AddError("Error reading server after creation", emptyServerResponse(serverID))
		return
	}
	attached, attachDiags := r.attachExistingDataVolumesAfterCreate(ctx, serverID, &plan)
	resp.Diagnostics.Append(attachDiags...)
	if attached != nil {
		server = attached
	}
	resp.Diagnostics.Append(r.refreshServerResourceState(ctx, server, &plan)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *ServerResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state ServerResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	serverID, diags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	server, err := r.get(ctx, serverID)
	if errors.Is(err, serversdk.ErrNotFound) {
		resp.State.RemoveResource(ctx)
		return
	}
	if isTransientEnumError(err) {
		// The backend reports a status this provider cannot map, so there is
		// nothing to refresh into. Keeping the last known state lets the
		// practitioner still plan a replacement or destroy the server instead
		// of being blocked by every refresh.
		resp.Diagnostics.AddWarning(
			"Unable to refresh server state",
			fmt.Sprintf(
				"The API reported a status this provider does not support, so Terraform kept the last known state for server %s: %s",
				serverID, err,
			),
		)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading server", err.Error())
		return
	}
	// A deleted server can still be readable for a while, so treat that status
	// like a missing server instead of refreshing unusable state.
	if server == nil || server.Status == serversdk.ServerStatusDeleted {
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(r.refreshServerResourceState(ctx, server, &state)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *ServerResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan ServerResourceModel
	var state ServerResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	serverID, diags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diags...)
	changePowerState := powerStateChanged(plan, state)
	if changePowerState {
		resp.Diagnostics.Append(validateDesiredPowerState(plan.PowerState)...)
	}
	if resp.Diagnostics.HasError() {
		return
	}

	body, changed, updateDiags := buildServerUpdateBody(plan, state)
	resp.Diagnostics.Append(updateDiags...)
	if resp.Diagnostics.HasError() {
		return
	}
	bootChanged, bootDiags := bootSourceChanged(ctx, plan, state)
	resp.Diagnostics.Append(bootDiags...)
	var rebuildBody serversdk.ServerRebuildSchema
	var expectedImageID core.UUID
	if bootChanged && !resp.Diagnostics.HasError() {
		imageFinder := serverlookup.NewImageFinder(r.client, r.projectID)
		rebuildBody, expectedImageID, bootDiags = buildServerRebuildBody(ctx, plan.Boot, r.projectID, imageFinder.Resolve)
		resp.Diagnostics.Append(bootDiags...)
		if !resp.Diagnostics.HasError() && rebuildBody.ImageId != nil {
			var boot BootInputModel
			resp.Diagnostics.Append(plan.Boot.As(ctx, &boot, basetypes.ObjectAsOptions{})...)
			var prior BootInputModel
			resp.Diagnostics.Append(state.Boot.As(ctx, &prior, basetypes.ObjectAsOptions{})...)
			if !resp.Diagnostics.HasError() {
				currentID, known := selector.ID(prior.Image)
				if !known {
					var err error
					currentID, err = r.currentSelectorID(ctx, state, "image", -1)
					if err != nil {
						resp.Diagnostics.AddError("Unable to read server image identity", err.Error())
					}
				}
				bootChanged = currentID != expectedImageID
				boot.Image = selector.WithID(boot.Image, types.StringValue(expectedImageID.String()))
				var valueDiags diag.Diagnostics
				plan.Boot, valueDiags = types.ObjectValueFrom(ctx, bootResourceAttributeTypes(), boot)
				resp.Diagnostics.Append(valueDiags...)
			}
		}
	}
	needsResize, flavorDiags := flavorChanged(ctx, plan, state)
	resp.Diagnostics.Append(flavorDiags...)
	var resizeBody serversdk.ServerResizeSchema
	if needsResize && !resp.Diagnostics.HasError() {
		resizeBody, flavorDiags = r.buildServerResizeBody(ctx, serverID, plan.Flavor)
		resp.Diagnostics.Append(flavorDiags...)
	}
	var securityGroupBody serversdk.ServerUpdateSecurityGroupSchema
	if securityGroupsChanged(plan, state) && !resp.Diagnostics.HasError() {
		securityGroupBody, diags = buildSecurityGroupUpdateBody(plan.SecurityGroups)
		resp.Diagnostics.Append(diags...)
	}
	attachmentPlan, attachmentDiags := r.buildAttachmentUpdatePlan(ctx, serverID, plan, state)
	resp.Diagnostics.Append(attachmentDiags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if data := attachmentPlan.dataVolume; data != nil {
		// Record each allocation against prior state before any waiter or
		// attachment request can fail. The planned list can contain unknown
		// entries for volumes that do not exist yet.
		data.recordCreated = func(created DataVolumeInputModel) diag.Diagnostics {
			var diags diag.Diagnostics
			var volumes []DataVolumeInputModel
			if !state.DataVolumes.IsNull() && !state.DataVolumes.IsUnknown() {
				diags.Append(state.DataVolumes.ElementsAs(ctx, &volumes, false)...)
			}
			if diags.HasError() {
				return diags
			}
			volumes = append(volumes, created)
			value, valueDiags := types.ListValueFrom(ctx, types.ObjectType{AttrTypes: dataVolumeResourceAttributeTypes()}, volumes)
			diags.Append(valueDiags...)
			if !diags.HasError() {
				diags.Append(resp.State.SetAttribute(ctx, path.Root("data_volumes"), value)...)
				state.DataVolumes = value
			}
			return diags
		}
	}

	// Every request below mutates the server, so the refresh must run even when
	// one step fails. Otherwise Terraform keeps prior state for the changes that
	// already succeeded and reports drift that no longer exists.
	resp.Diagnostics.Append(r.applyServerUpdate(ctx, serverID, serverUpdateRequest{
		patchServer:       changed,
		patchBody:         body,
		rebuild:           bootChanged,
		rebuildBody:       rebuildBody,
		rebuiltImageID:    expectedImageID,
		resize:            needsResize,
		resizeBody:        resizeBody,
		updateBandwidth:   bandwidthChanged(plan, state),
		bandwidth:         int(plan.Bandwidth.ValueInt64()),
		replaceGroups:     securityGroupsChanged(plan, state),
		securityGroupBody: securityGroupBody,
		attachments:       attachmentPlan,
		changePowerState:  changePowerState,
		desiredPowerState: plan.PowerState,
		priorPowerState:   state.PowerState,
	})...)

	// An attach or detach can lag the update response, so the refresh has to
	// run against a server whose attachments already match the plan. A failed
	// step above may never reach that count. Waiting it out would replace the
	// real failure with a timeout, so refresh against the current response and
	// let the diagnostics already collected stand.
	var server *serversdk.ServerDetailSchema
	var err error
	if resp.Diagnostics.HasError() {
		server, err = r.get(ctx, serverID)
	} else {
		server, err = r.waitUntilAttachmentsSettled(ctx, serverID, plan.PrivateIPs, plan.ElasticIPs)
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading server after update", err.Error())
		return
	}
	if server == nil {
		resp.Diagnostics.AddError("Error reading server after update", emptyServerResponse(serverID))
		return
	}
	if data := attachmentPlan.dataVolume; data != nil {
		value, valueDiags := types.ListValueFrom(ctx, types.ObjectType{AttrTypes: dataVolumeResourceAttributeTypes()}, dataVolumesAfterUpdate(data, server))
		resp.Diagnostics.Append(valueDiags...)
		if !valueDiags.HasError() {
			plan.DataVolumes = value
		}
	}
	resp.Diagnostics.Append(r.refreshServerResourceState(ctx, server, &plan)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *ServerResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state ServerResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	serverID, diags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// The volume attachments are only readable while the server exists, so the
	// cascade targets are resolved before deletion and deleted afterwards.
	var volumeIDs []core.UUID
	server, err := r.get(ctx, serverID)
	switch {
	case errors.Is(err, serversdk.ErrNotFound):
	case isTransientEnumError(err):
		// A server in a status this provider cannot map, such as one that failed
		// to build, must still be destroyable. Skip the state refresh and the
		// shutdown and delete it directly.
		resp.Diagnostics.Append(r.deleteServer(ctx, serverID, nil)...)
	case err != nil:
		resp.Diagnostics.AddError("Error reading server before deletion", err.Error())
	case server != nil:
		resp.Diagnostics.Append(populateServerResourceState(ctx, server, &state)...)
		targets, targetDiags := serverVolumeCascadeTargets(ctx, state, server)
		resp.Diagnostics.Append(targetDiags...)
		if !resp.Diagnostics.HasError() {
			volumeIDs = targets
			// Report a shutdown failure only when deletion also fails, because
			// then it explains why deletion was rejected.
			resp.Diagnostics.Append(r.deleteServer(ctx, serverID, r.stopBeforeDeletion(ctx, serverID, server))...)
		}
	}
	if resp.Diagnostics.HasError() {
		return
	}
	r.deleteTerminatedAttachments(ctx, state, &resp.Diagnostics)
	r.deleteTerminatedVolumes(ctx, volumeIDs, &resp.Diagnostics)
}

func (r *ServerResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// --- Server lifecycle: fetch, waiters and deletion ---

func (r *ServerResource) get(ctx context.Context, id core.UUID) (*serversdk.ServerDetailSchema, error) {
	return r.client.GetServer(ctx, id, serversdk.GetServerParams{ProjectID: r.projectID})
}

func (r *ServerResource) waitUntilReady(ctx context.Context, id core.UUID) error {
	return r.waitForServer(ctx, id, "ready", func(server *serversdk.ServerDetailSchema) (bool, error) {
		if server.PowerState == serversdk.ServerPowerStateRunning && !serverIsBusy(server) {
			return true, nil
		}
		if server.Status == serversdk.ServerStatusDeleted {
			return false, fmt.Errorf("server %s entered terminal status %s", id, server.Status)
		}
		// A server can report a stopped status while it is still building, so a
		// non-running status is terminal only once provisioning has finished.
		if !serverIsBusy(server) && (server.Status == serversdk.ServerStatusShutoff ||
			server.Status == serversdk.ServerStatusStopped || server.Status == serversdk.ServerStatusSuspended) {
			return false, fmt.Errorf("server %s entered terminal status %s", id, server.Status)
		}
		return false, nil
	})
}

func (r *ServerResource) waitUntilShutdown(ctx context.Context, id core.UUID) (*serversdk.ServerDetailSchema, error) {
	server, err := r.pollServer(ctx, id, "shutdown", func(server *serversdk.ServerDetailSchema) (bool, error) {
		return serverIsShutdown(server) && !serverIsBusy(server), nil
	})
	if errors.Is(err, serversdk.ErrNotFound) {
		return nil, nil
	}
	return server, err
}

func (r *ServerResource) waitUntilNotPending(ctx context.Context, id core.UUID) (*serversdk.ServerDetailSchema, error) {
	return r.pollServer(ctx, id, "power state to leave pending", func(server *serversdk.ServerDetailSchema) (bool, error) {
		return server.PowerState != serversdk.ServerPowerStatePending, nil
	})
}

func (r *ServerResource) waitUntilBandwidth(ctx context.Context, id core.UUID, expected int) error {
	return r.waitForServer(ctx, id, "bandwidth update", func(server *serversdk.ServerDetailSchema) (bool, error) {
		return server.Bandwidth != nil && *server.Bandwidth == expected && !serverIsBusy(server), nil
	})
}

// waitUntilAttachmentsSettled waits for a non-busy server with the planned
// attachment counts and allocated addresses. Detail responses can lag updates;
// refreshing too early can produce a list length that violates the plan.
// Null or unknown lists impose no count or address requirement. This waiter
// does not compare attachment IDs.
func (r *ServerResource) waitUntilAttachmentsSettled(
	ctx context.Context,
	id core.UUID,
	privateIPs types.List,
	elasticIPs types.List,
) (*serversdk.ServerDetailSchema, error) {
	privateIPsFixed := !privateIPs.IsNull() && !privateIPs.IsUnknown()
	elasticIPsFixed := !elasticIPs.IsNull() && !elasticIPs.IsUnknown()
	wantPrivateIPs, wantElasticIPs := len(privateIPs.Elements()), len(elasticIPs.Elements())

	gotPrivateIPs, gotElasticIPs := 0, 0
	addressesPending := false
	result, err := r.pollServer(ctx, id, "attachments to settle", func(server *serversdk.ServerDetailSchema) (bool, error) {
		gotPrivateIPs, gotElasticIPs = len(server.PrivateIps), len(server.ElasticIps)
		privateIPsReady := gotPrivateIPs == wantPrivateIPs && privateIPAddressesAssigned(server.PrivateIps)
		elasticIPsReady := gotElasticIPs == wantElasticIPs && elasticIPAddressesAssigned(server.ElasticIps)
		addressesPending = (privateIPsFixed && !privateIPAddressesAssigned(server.PrivateIps)) ||
			(elasticIPsFixed && !elasticIPAddressesAssigned(server.ElasticIps))
		if serverIsBusy(server) ||
			(privateIPsFixed && !privateIPsReady) ||
			(elasticIPsFixed && !elasticIPsReady) {
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		if addressesPending {
			return nil, fmt.Errorf(
				"%w (the API reported %d private IPs and %d elastic IPs, and at least one of them still has no address)",
				err, gotPrivateIPs, gotElasticIPs,
			)
		}
		return nil, fmt.Errorf(
			"%w (the API reported %d private IPs and %d elastic IPs)",
			err, gotPrivateIPs, gotElasticIPs,
		)
	}
	return result, nil
}

// waitForServer polls the server until settled reports the operation finished.
// The poll function classifies its own outcomes for the shared waiter: a
// missing server is fatal; other read failures are reported as not-ready.
// A single slow response, or a status this provider
// cannot map, must not abandon an operation that is still running. The reason
// the server was not ready surfaces in the timeout error.
func (r *ServerResource) waitForServer(
	ctx context.Context,
	id core.UUID,
	operation string,
	settled func(*serversdk.ServerDetailSchema) (bool, error),
) error {
	_, err := r.pollServer(ctx, id, operation, settled)
	return err
}

// pollServer returns the response from the successful poll so callers can use
// the settled values without another read.
func (r *ServerResource) pollServer(
	ctx context.Context,
	id core.UUID,
	operation string,
	settled func(*serversdk.ServerDetailSchema) (bool, error),
) (*serversdk.ServerDetailSchema, error) {
	w := wait.New[*serversdk.ServerDetailSchema](serverPollInterval, serverOperationTimeout)
	server, err := w.WaitFor(ctx, func(ctx context.Context) (*serversdk.ServerDetailSchema, error) {
		server, err := r.get(ctx, id)
		switch {
		case errors.Is(err, serversdk.ErrNotFound):
			return nil, err
		case err != nil:
			return nil, fmt.Errorf("poll server %s: %s: %w", id, err, wait.ErrNotReady)
		case server == nil:
			return nil, fmt.Errorf("the API returned an empty response for server %s: %w", id, wait.ErrNotReady)
		}
		complete, completeErr := settled(server)
		if completeErr != nil {
			return nil, completeErr
		}
		if !complete {
			return nil, fmt.Errorf("server %s has not finished %s yet: %w", id, operation, wait.ErrNotReady)
		}
		return server, nil
	})
	if err != nil {
		return nil, fmt.Errorf("waiting for server %s %s: %w", id, operation, err)
	}
	return server, nil
}

// waitUntilDeleted treats a missing server or one with deleted status as gone.
// A read that fails for any other reason is
// reported as not-ready so a server that is still being torn down is not
// abandoned mid-deletion.
func (r *ServerResource) waitUntilDeleted(ctx context.Context, id core.UUID) error {
	w := wait.New[struct{}](serverPollInterval, serverOperationTimeout)
	_, err := w.WaitFor(ctx, func(ctx context.Context) (struct{}, error) {
		server, err := r.get(ctx, id)
		switch {
		case errors.Is(err, serversdk.ErrNotFound):
			return struct{}{}, nil
		case err != nil:
			return struct{}{}, fmt.Errorf("poll server %s: %s: %w", id, err, wait.ErrNotReady)
		case server != nil && server.Status == serversdk.ServerStatusDeleted:
			return struct{}{}, nil
		}
		return struct{}{}, fmt.Errorf("server %s is not deleted yet: %w", id, wait.ErrNotReady)
	})
	if err != nil {
		return fmt.Errorf("waiting for server %s deletion: %w", id, err)
	}
	return nil
}

// planSelectors restores known catalog IDs before the computed no-op guard.
// Unrelated unknown configuration must not suppress immutable selector changes.
func (r *ServerResource) planSelectors(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	var plan, state ServerResourceModel
	resp.Diagnostics.Append(resp.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	decide := func(planned, prior types.Object, p path.Path, image bool, index int) types.Object {
		if planned.IsNull() && prior.IsNull() {
			return planned
		}
		find := func(ctx context.Context) ([]core.UUID, error) {
			var ids []core.UUID
			if image {
				filter, diags := serverselector.ImageFilter(ctx, planned, p.String())
				if diags.HasError() {
					return nil, fmt.Errorf("%v", diags)
				}
				candidates, err := serverlookup.NewImageFinder(r.client, r.projectID).Find(ctx, filter)
				for _, candidate := range candidates {
					ids = append(ids, candidate.Id)
				}
				return ids, err
			}
			filter, diags := blockstorageselector.VolumeTypeFilter(ctx, planned, p.String())
			if diags.HasError() {
				return nil, fmt.Errorf("%v", diags)
			}
			if zoneName := reference.Get(state.Zone, reference.Name); !zoneName.IsNull() && !zoneName.IsUnknown() {
				name := strings.TrimSpace(zoneName.ValueString())
				zone, err := projectlookup.NewZoneFinder(r.projectClient, r.projectID).Resolve(ctx, projectlookup.ZoneFilter{Name: &name})
				if err != nil {
					return nil, err
				}
				filter.ZoneID = &zone.Id
			}
			candidates, err := blockstoragelookup.NewVolumeTypeFinder(r.blockStorageClient).Find(ctx, filter)
			for _, candidate := range candidates {
				ids = append(ids, candidate.Id)
			}
			return ids, err
		}
		kind := "volume_type"
		if image {
			kind = "image"
		}
		decision, id, diags := selector.Decide(ctx, planned, prior, p.String(), find,
			func(ctx context.Context) (core.UUID, error) { return r.currentSelectorID(ctx, state, kind, index) })
		resp.Diagnostics.Append(diags...)
		if !image && index < 0 && decision != selector.Same {
			resp.RequiresReplace = append(resp.RequiresReplace, p)
		}
		value := selector.WithID(planned, id)
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, p, value)...)
		return value
	}
	if plan.Boot.IsUnknown() {
		resp.RequiresReplace = append(resp.RequiresReplace, path.Root("boot").AtName("volume_type"))
	} else if !plan.Boot.IsNull() && !state.Boot.IsNull() && !state.Boot.IsUnknown() {
		var planned, prior BootInputModel
		resp.Diagnostics.Append(plan.Boot.As(ctx, &planned, basetypes.ObjectAsOptions{})...)
		resp.Diagnostics.Append(state.Boot.As(ctx, &prior, basetypes.ObjectAsOptions{})...)
		if resp.Diagnostics.HasError() {
			return
		}
		if planned.BootType.Equal(prior.BootType) {
			switch normalizedString(planned.BootType) {
			case "image", "local_disk":
				decide(planned.Image, prior.Image, path.Root("boot").AtName("image"), true, -1)
			}
			decide(planned.VolumeType, prior.VolumeType, path.Root("boot").AtName("volume_type"), false, -1)
		}
	}
	if resp.Diagnostics.HasError() {
		return
	}
	if plan.DataVolumes.IsUnknown() {
		return
	}
	if plan.DataVolumes.IsNull() || state.DataVolumes.IsNull() || state.DataVolumes.IsUnknown() {
		return
	}
	var planned, prior []DataVolumeInputModel
	resp.Diagnostics.Append(plan.DataVolumes.ElementsAs(ctx, &planned, false)...)
	resp.Diagnostics.Append(state.DataVolumes.ElementsAs(ctx, &prior, false)...)
	if resp.Diagnostics.HasError() {
		return
	}
	matches, _, matchDiags := matchDataVolumeConfigs(planned, prior)
	resp.Diagnostics.Append(matchDiags...)
	if resp.Diagnostics.HasError() {
		return
	}
	for i := range planned {
		if matches[i] < 0 || dataVolumeKind(planned[i]) != "new" {
			continue
		}
		decide(planned[i].VolumeType, prior[matches[i]].VolumeType, path.Root("data_volumes").AtListIndex(i).AtName("volume_type"), false, matches[i])
		if resp.Diagnostics.HasError() {
			return
		}
	}
}

// currentSelectorID reads resource responses only for state without selector IDs.
func (r *ServerResource) currentSelectorID(ctx context.Context, state ServerResourceModel, kind string, index int) (core.UUID, error) {
	serverID, diags := parse.UUIDString(state.ID, "id")
	if diags.HasError() {
		return core.UUID{}, fmt.Errorf("%v", diags)
	}
	server, err := r.get(ctx, serverID)
	if err != nil {
		return core.UUID{}, err
	}
	if server == nil {
		return core.UUID{}, fmt.Errorf("server response is empty")
	}
	if kind == "image" && server.Image != nil {
		return server.Image.Id, nil
	}
	volumeID, found := serverRootVolumeID(server)
	if index >= 0 {
		ids := dataVolumeIDs(server)
		// Each configured volume keeps its own ID across attachment reordering.
		if !state.DataVolumes.IsNull() && !state.DataVolumes.IsUnknown() {
			var stored []DataVolumeInputModel
			if diags := state.DataVolumes.ElementsAs(ctx, &stored, false); diags.HasError() {
				return core.UUID{}, fmt.Errorf("%v", diags)
			}
			if index < len(stored) && !stored[index].ID.IsNull() && !stored[index].ID.IsUnknown() {
				ids = make([]string, index+1)
				ids[index] = stored[index].ID.ValueString()
			}
		}
		if index >= len(ids) {
			return core.UUID{}, fmt.Errorf("data volume %d is missing", index)
		}
		volumeID, err = core.ParseUUID(ids[index])
		found = err == nil
	}
	if !found {
		return core.UUID{}, fmt.Errorf("server volume identity is missing: %w", selector.ErrNoIdentity)
	}
	volume, err := r.blockStorageClient.GetVolume(ctx, volumeID, blockstoragesdk.GetVolumeParams{ProjectID: r.projectID})
	if err != nil {
		return core.UUID{}, err
	}
	if volume != nil {
		if kind == "image" && volume.CreateFrom.Image != nil {
			return volume.CreateFrom.Image.Id, nil
		}
		if kind == "volume_type" && volume.CreateFrom.VolumeType != nil {
			return volume.CreateFrom.VolumeType.Id, nil
		}
	}
	return core.UUID{}, fmt.Errorf("server volume %s: %w", kind, selector.ErrNoIdentity)
}

func (r *ServerResource) refreshServerDataVolumeState(ctx context.Context, server *serversdk.ServerDetailSchema, state *ServerResourceModel) diag.Diagnostics {
	var diags diag.Diagnostics
	var configured []DataVolumeInputModel
	if !state.DataVolumes.IsNull() && !state.DataVolumes.IsUnknown() {
		diags.Append(state.DataVolumes.ElementsAs(ctx, &configured, false)...)
	}
	if diags.HasError() {
		return diags
	}
	var attachments []serversdk.NestedServerVolumeSchema
	for _, attachment := range server.Volumes {
		if attachment.MountAs == "data" {
			attachments = append(attachments, attachment)
		}
	}
	matches, used := matchAttachmentIDs(len(configured), len(attachments), func(i, j int) bool {
		id, err := core.ParseUUID(configured[i].ID.ValueString())
		return err == nil && id == attachments[j].Volume.Id
	}, func(i int) bool { return !configured[i].ID.IsNull() && !configured[i].ID.IsUnknown() })
	result := make([]DataVolumeInputModel, 0, len(attachments))
	for i, config := range configured {
		if !config.ID.IsNull() && !config.ID.IsUnknown() {
			_, idDiags := parse.UUIDString(config.ID, fmt.Sprintf("data_volumes[%d].id", i))
			diags.Append(idDiags...)
			if diags.HasError() {
				return diags
			}
		}
		if matches[i] < 0 {
			// A new volume remains owned when allocation succeeds but attach
			// fails. Retaining it lets the next update retry with the same ID.
			if dataVolumeKind(config) == "new" && !config.ID.IsNull() && !config.ID.IsUnknown() {
				id, _ := core.ParseUUID(config.ID.ValueString())
				volume, err := r.blockStorageClient.GetVolume(ctx, id, blockstoragesdk.GetVolumeParams{ProjectID: r.projectID})
				if errors.Is(err, blockstoragesdk.ErrNotFound) {
					continue
				}
				if err != nil || volume == nil {
					diags.AddError("Error reading detached server-owned volume", fmt.Sprintf("Volume %s: %v", id, err))
					return diags
				}
				diags.Append(populateServerDataVolume(volume, false, &config)...)
				result = append(result, config)
			}
			continue
		}
		volume, err := r.blockStorageClient.GetVolume(ctx, attachments[matches[i]].Volume.Id, blockstoragesdk.GetVolumeParams{ProjectID: r.projectID})
		if err != nil || volume == nil {
			diags.AddError("Error reading server data volume", fmt.Sprintf("Unable to read volume %s: %v", attachments[matches[i]].Volume.Id, err))
			return diags
		}
		diags.Append(populateServerDataVolume(volume, true, &config)...)
		result = append(result, config)
	}
	for i, attachment := range attachments {
		if used[i] {
			continue
		}
		volume, err := r.blockStorageClient.GetVolume(ctx, attachment.Volume.Id, blockstoragesdk.GetVolumeParams{ProjectID: r.projectID})
		if err != nil || volume == nil {
			diags.AddError("Error reading server data volume", fmt.Sprintf("Unable to read volume %s: %v", attachment.Volume.Id, err))
			return diags
		}
		config := DataVolumeInputModel{Kind: types.StringValue("existing")}
		diags.Append(populateServerDataVolume(volume, true, &config)...)
		result = append(result, config)
	}
	if diags.HasError() {
		return diags
	}
	state.DataVolumes, diags = types.ListValueFrom(ctx, types.ObjectType{AttrTypes: dataVolumeResourceAttributeTypes()}, result)
	return diags
}

func (r *ServerResource) attachExistingDataVolumesAfterCreate(ctx context.Context, serverID core.UUID, plan *ServerResourceModel) (*serversdk.ServerDetailSchema, diag.Diagnostics) {
	var diags diag.Diagnostics
	var configs []DataVolumeInputModel
	if plan.DataVolumes.IsNull() || plan.DataVolumes.IsUnknown() {
		return nil, diags
	}
	diags.Append(plan.DataVolumes.ElementsAs(ctx, &configs, false)...)
	if diags.HasError() {
		return nil, diags
	}
	var latest *serversdk.ServerDetailSchema
	for i, config := range configs {
		if dataVolumeKind(config) != "existing" {
			continue
		}
		id, idDiags := parse.UUIDString(config.ID, fmt.Sprintf("data_volumes[%d].id", i))
		diags.Append(idDiags...)
		if diags.HasError() {
			return latest, diags
		}
		if err := r.requestServerOperation(ctx, serverID, func(ctx context.Context) error {
			return r.client.AttachVolume(ctx, serverID, serversdk.AttachVolumeParams{ProjectID: r.projectID}, serversdk.ServerVolumeAttachSchema{VolumeId: id})
		}); err != nil {
			diags.AddError("Error attaching server data volume", err.Error())
			return latest, diags
		}
		var err error
		latest, err = r.waitUntilDataVolumeAttachment(ctx, serverID, id, true)
		if err != nil {
			diags.AddError("Error waiting for server data volume attachment", err.Error())
			return latest, diags
		}
	}
	return latest, diags
}

func (r *ServerResource) buildDataVolumeUpdatePlan(ctx context.Context, plan, state ServerResourceModel) (*dataVolumeUpdatePlan, diag.Diagnostics) {
	var diags diag.Diagnostics
	if plan.DataVolumes.IsUnknown() {
		diags.AddError("Unknown data volumes", "data_volumes must be known before updating the server.")
		return nil, diags
	}
	if plan.DataVolumes.IsNull() {
		return nil, diags
	}
	var planned, prior []DataVolumeInputModel
	diags.Append(plan.DataVolumes.ElementsAs(ctx, &planned, false)...)
	if !state.DataVolumes.IsNull() && !state.DataVolumes.IsUnknown() {
		diags.Append(state.DataVolumes.ElementsAs(ctx, &prior, false)...)
	}
	if diags.HasError() {
		return nil, diags
	}
	matches, used, matchDiags := matchDataVolumeConfigs(planned, prior)
	diags.Append(matchDiags...)
	if diags.HasError() {
		return nil, diags
	}
	resolvedSelectors := false
	result := &dataVolumeUpdatePlan{desiredIDs: make(map[core.UUID]struct{}), removedIDs: make(map[core.UUID]struct{}), planned: planned, cleanupConfigs: make(map[core.UUID]DataVolumeInputModel)}
	for i, config := range planned {
		if matches[i] >= 0 {
			old := prior[matches[i]]
			id, idDiags := parse.UUIDString(old.ID, fmt.Sprintf("prior data_volumes[%d].id", matches[i]))
			diags.Append(idDiags...)
			if idDiags.HasError() {
				continue
			}
			// An omitted list preserves detached ownership with attached=false.
			// Configured entries plan attached=true before an attachment retry.
			if config.Attached.Equal(types.BoolValue(false)) {
				continue
			}
			result.desiredIDs[id] = struct{}{}
			if !old.Attached.IsNull() && !old.Attached.IsUnknown() && !old.Attached.ValueBool() {
				result.existingIDs = append(result.existingIDs, id)
			}
			if dataVolumeKind(config) == "new" && !config.VolumeSize.Equal(old.VolumeSize) {
				target, ok := increasedVolumeSize(config.VolumeSize, old.VolumeSize, fmt.Sprintf("data_volumes[%d].volume_size", i), &diags)
				if ok && r.resolveVolumeResize(ctx, id, dataVolumeLabel(id), target, &diags) {
					result.resizeIDs = append(result.resizeIDs, id)
					result.resizeSizes = append(result.resizeSizes, target)
				}
			}
			if dataVolumeKind(config) == "new" {
				update := dataVolumePropertyUpdate{id: id}
				if !config.IOPS.IsNull() && !config.IOPS.IsUnknown() && !config.IOPS.Equal(old.IOPS) {
					iops := int(config.IOPS.ValueInt64())
					update.iops = &iops
				}
				selectedID, known := selector.ID(config.VolumeType)
				currentID, currentKnown := selector.ID(old.VolumeType)
				// A null ID is an entry that the backend does not identify, so
				// only a pending selection resolves a new volume type.
				pending := selector.Pending(config.VolumeType)
				if pending && selector.Equal(config.VolumeType, old.VolumeType) && currentKnown {
					selectedID, known, pending = currentID, true, false
				}
				if pending && !selector.Equal(config.VolumeType, old.VolumeType) || known && (!currentKnown || selectedID != currentID) {
					volume, err := r.blockStorageClient.GetVolume(ctx, id, blockstoragesdk.GetVolumeParams{ProjectID: r.projectID})
					if err != nil || volume == nil || volume.CreateFrom.VolumeType == nil {
						diags.AddError("Error reading data volume before type change", fmt.Sprintf("Volume %s does not identify its type: %v", id, err))
						continue
					}
					currentID = volume.CreateFrom.VolumeType.Id
					if !known {
						zoneID := volume.Zone.Id
						volumeType, typeDiags := blockstorageselector.ResolveVolumeType(ctx, config.VolumeType, fmt.Sprintf("data_volumes[%d].volume_type", i), &zoneID, int(config.VolumeSize.ValueInt64()), blockstoragelookup.NewVolumeTypeFinder(r.blockStorageClient).ResolveForCreate)
						diags.Append(typeDiags...)
						if typeDiags.HasError() {
							continue
						}
						selectedID, known = volumeType.Id, true
						resolvedSelectors = true
					}
					if selectedID != currentID {
						update.volumeTypeID = selectedID
					}
				}
				if known {
					result.planned[i].VolumeType = selector.WithID(config.VolumeType, types.StringValue(selectedID.String()))
				}
				if update.iops != nil || update.volumeTypeID != core.NilUUID {
					result.updates = append(result.updates, update)
				}
			}
			continue
		}
		switch dataVolumeKind(config) {
		case "existing":
			id, idDiags := parse.UUIDString(config.ID, fmt.Sprintf("data_volumes[%d].id", i))
			diags.Append(idDiags...)
			if !idDiags.HasError() {
				result.existingIDs = append(result.existingIDs, id)
				result.desiredIDs[id] = struct{}{}
			}
		case "new":
			body, bodyDiags := r.buildNewDataVolumeBody(ctx, state, &result.planned[i])
			diags.Append(bodyDiags...)
			result.newBodies = append(result.newBodies, body)
			result.newIndexes = append(result.newIndexes, i)
		default:
			diags.AddError("Invalid data volume kind", "data_volumes.kind must be new or existing.")
		}
	}
	for i, config := range prior {
		if used[i] {
			continue
		}
		id, idDiags := parse.UUIDString(config.ID, fmt.Sprintf("prior data_volumes[%d].id", i))
		diags.Append(idDiags...)
		if idDiags.HasError() {
			continue
		}
		result.removedIDs[id] = struct{}{}
		if dataVolumeKind(config) == "new" && boolValueOrDefault(config.DeleteOnTermination, true) {
			result.deletedIDs = append(result.deletedIDs, id)
			result.cleanupConfigs[id] = config
		} else {
			result.detachConfigs = append(result.detachConfigs, config)
		}
	}
	if diags.HasError() {
		return nil, diags
	}
	if len(result.resizeIDs)+len(result.removedIDs)+len(result.existingIDs)+len(result.newBodies)+len(result.updates) == 0 && !resolvedSelectors {
		return nil, diags
	}
	return result, diags
}

// dataVolumesAfterUpdate lists the entries that the refresh after an update
// matches against the server. An update can fail before it creates a new
// volume or detaches a removed one. The refresh matches an entry without an ID
// by position. An uncreated volume could then take the identity of a removed
// attachment, which can be a volume that the practitioner owns. The list
// therefore leaves out each uncreated volume and keeps each removed entry that
// is still attached, with its kind and cleanup flag, for the next apply.
func dataVolumesAfterUpdate(plan *dataVolumeUpdatePlan, server *serversdk.ServerDetailSchema) []DataVolumeInputModel {
	attached := make(map[core.UUID]struct{})
	for _, attachment := range server.Volumes {
		if attachment.MountAs == "data" {
			attached[attachment.Volume.Id] = struct{}{}
		}
	}
	volumes := make([]DataVolumeInputModel, 0, len(plan.planned)+len(plan.detachConfigs)+len(plan.cleanupConfigs))
	for _, config := range plan.planned {
		if !config.ID.IsUnknown() {
			volumes = append(volumes, config)
		}
	}
	for _, config := range plan.detachConfigs {
		if id, err := core.ParseUUID(config.ID.ValueString()); err == nil {
			if _, ok := attached[id]; ok {
				volumes = append(volumes, config)
			}
		}
	}
	for _, config := range plan.cleanupConfigs {
		volumes = append(volumes, config)
	}
	return volumes
}

func (r *ServerResource) buildNewDataVolumeBody(ctx context.Context, state ServerResourceModel, config *DataVolumeInputModel) (blockstoragesdk.VolumeCreateSchema, diag.Diagnostics) {
	var body blockstoragesdk.VolumeCreateSchema
	var diags diag.Diagnostics
	if config.VolumeSize.IsNull() || config.VolumeSize.IsUnknown() || config.VolumeSize.ValueInt64() < 1 {
		diags.AddError("Invalid new data volume size", "volume_size must be known and greater than zero for kind new.")
		return body, diags
	}
	serverID, idDiags := parse.UUIDString(state.ID, "id")
	diags.Append(idDiags...)
	if diags.HasError() {
		return body, diags
	}
	server, err := r.get(ctx, serverID)
	if err != nil || server == nil {
		diags.AddError("Error reading server before volume creation", fmt.Sprint(err))
		return body, diags
	}
	zoneID := server.Zone.Id
	volumeTypeID, known := selector.ID(config.VolumeType)
	if !known {
		volumeType, typeDiags := blockstorageselector.ResolveVolumeType(ctx, config.VolumeType, "data_volumes.volume_type", &zoneID, int(config.VolumeSize.ValueInt64()), blockstoragelookup.NewVolumeTypeFinder(r.blockStorageClient).ResolveForCreate)
		diags.Append(typeDiags...)
		if diags.HasError() {
			return body, diags
		}
		volumeTypeID = volumeType.Id
	}
	config.VolumeType = selector.WithID(config.VolumeType, types.StringValue(volumeTypeID.String()))
	body.Name = "server-data-" + core.NewUUID().String()
	err = body.CreateFrom.FromVolumeCreateEmptySchema(blockstoragesdk.VolumeCreateEmptySchema{Size: int(config.VolumeSize.ValueInt64()), VolumeTypeId: volumeTypeID})
	if err != nil {
		diags.AddError("Error building data volume source", err.Error())
	}
	if !config.IOPS.IsNull() && !config.IOPS.IsUnknown() {
		iops := int(config.IOPS.ValueInt64())
		body.Iops = &iops
	}
	return body, diags
}

func (r *ServerResource) applyDataVolumeUpdate(ctx context.Context, serverID core.UUID, plan *dataVolumeUpdatePlan) diag.Diagnostics {
	var diags diag.Diagnostics
	for id := range plan.removedIDs {
		server, err := r.get(ctx, serverID)
		if err != nil || server == nil {
			diags.AddError("Error reading server before volume detach", fmt.Sprint(err))
			return diags
		}
		for _, attachment := range server.Volumes {
			if attachment.Volume.Id != id {
				continue
			}
			if attachment.MountAs != "data" {
				diags.AddError("Invalid data volume attachment", "A data_volumes entry cannot detach a boot volume.")
				return diags
			}
			err := r.requestServerOperation(ctx, serverID, func(ctx context.Context) error {
				return r.client.DetachVolume(ctx, serverID, serversdk.DetachVolumeParams{ProjectID: r.projectID}, serversdk.ServerVolumeAttachSchema{VolumeId: id})
			})
			if err != nil && !errors.Is(err, serversdk.ErrNotFound) {
				diags.AddError("Error detaching server data volume", err.Error())
				return diags
			}
			break
		}
		if _, err := r.waitUntilDataVolumeAttachment(ctx, serverID, id, false); err != nil {
			diags.AddError("Error waiting for server data volume detach", err.Error())
			return diags
		}
	}
	for _, id := range plan.deletedIDs {
		if err := r.blockStorageClient.DeleteVolume(ctx, id, blockstoragesdk.DeleteVolumeParams{ProjectID: r.projectID}); err != nil && !errors.Is(err, blockstoragesdk.ErrNotFound) {
			diags.AddError("Error deleting detached server data volume", err.Error())
			return diags
		}
		if err := r.waitUntilDataVolumeDeleted(ctx, id); err != nil {
			diags.AddError("Error waiting for data volume deletion", err.Error())
			return diags
		}
		delete(plan.cleanupConfigs, id)
	}
	for _, update := range plan.updates {
		if update.volumeTypeID != core.NilUUID {
			if err := r.blockStorageClient.RetypeVolume(ctx, update.id, blockstoragesdk.RetypeVolumeParams{ProjectID: r.projectID}, blockstoragesdk.VolumeRetypeSchema{VolumeTypeId: update.volumeTypeID}); err != nil {
				diags.AddError("Error changing server data volume type", err.Error())
				return diags
			}
			if err := r.waitUntilDataVolumeProperties(ctx, dataVolumePropertyUpdate{id: update.id, volumeTypeID: update.volumeTypeID}); err != nil {
				diags.AddError("Error waiting for server data volume type", err.Error())
				return diags
			}
		}
		if update.iops != nil {
			if _, err := r.blockStorageClient.UpdateVolume(ctx, update.id, blockstoragesdk.UpdateVolumeParams{ProjectID: r.projectID}, blockstoragesdk.VolumePartialUpdateSchema{Iops: update.iops}); err != nil {
				diags.AddError("Error updating server data volume IOPS", err.Error())
				return diags
			}
		}
		if update.iops != nil {
			if err := r.waitUntilDataVolumeProperties(ctx, update); err != nil {
				diags.AddError("Error waiting for server data volume update", err.Error())
				return diags
			}
		}
	}
	for i, id := range plan.resizeIDs {
		diags.Append(r.resizeVolume(ctx, id, plan.resizeSizes[i], dataVolumeLabel(id))...)
		if diags.HasError() {
			return diags
		}
	}
	for _, id := range plan.existingIDs {
		if err := r.requestServerOperation(ctx, serverID, func(ctx context.Context) error {
			return r.client.AttachVolume(ctx, serverID, serversdk.AttachVolumeParams{ProjectID: r.projectID}, serversdk.ServerVolumeAttachSchema{VolumeId: id})
		}); err != nil {
			diags.AddError("Error attaching server data volume", err.Error())
			return diags
		}
		if _, err := r.waitUntilDataVolumeAttachment(ctx, serverID, id, true); err != nil {
			diags.AddError("Error waiting for server data volume attachment", err.Error())
			return diags
		}
	}
	for i, body := range plan.newBodies {
		volume, err := r.blockStorageClient.CreateVolume(ctx, blockstoragesdk.CreateVolumeParams{ProjectID: r.projectID}, body)
		if err != nil || volume == nil {
			diags.AddError("Error creating server data volume", fmt.Sprint(err))
			return diags
		}
		plan.planned[plan.newIndexes[i]].ID = types.StringValue(volume.Id.String())
		if plan.recordCreated != nil {
			created := plan.planned[plan.newIndexes[i]]
			diags.Append(populateServerDataVolume(volume, false, &created)...)
			if !diags.HasError() {
				diags.Append(plan.recordCreated(created)...)
			}
			if diags.HasError() {
				return diags
			}
		}
		if err := r.waitUntilDataVolumeAvailable(ctx, volume.Id); err != nil {
			diags.AddError("Error waiting for new server data volume", err.Error())
			return diags
		}
		if err := r.requestServerOperation(ctx, serverID, func(ctx context.Context) error {
			return r.client.AttachVolume(ctx, serverID, serversdk.AttachVolumeParams{ProjectID: r.projectID}, serversdk.ServerVolumeAttachSchema{VolumeId: volume.Id})
		}); err != nil {
			cleanupErr := r.blockStorageClient.DeleteVolume(ctx, volume.Id, blockstoragesdk.DeleteVolumeParams{ProjectID: r.projectID})
			diags.AddError("Error attaching new server data volume", fmt.Sprintf("Volume %s: %v. Cleanup result: %v", volume.Id, err, cleanupErr))
			return diags
		}
		if _, err := r.waitUntilDataVolumeAttachment(ctx, serverID, volume.Id, true); err != nil {
			diags.AddError("Error waiting for new data volume attachment", fmt.Sprintf("Volume %s: %v", volume.Id, err))
			return diags
		}
		plan.desiredIDs[volume.Id] = struct{}{}
	}
	if plan.desiredIDs != nil {
		err := r.waitForServer(ctx, serverID, "data volume attachments to settle", func(server *serversdk.ServerDetailSchema) (bool, error) {
			ids := dataVolumeIDs(server)
			if serverIsBusy(server) || len(ids) != len(plan.desiredIDs) {
				return false, nil
			}
			for _, value := range ids {
				id, err := core.ParseUUID(value)
				if err != nil {
					return false, err
				}
				if _, ok := plan.desiredIDs[id]; !ok {
					return false, nil
				}
			}
			return true, nil
		})
		if err != nil {
			diags.AddError("Error waiting for server data volumes", err.Error())
		}
	}
	return diags
}

func (r *ServerResource) waitUntilDataVolumeAttachment(ctx context.Context, serverID, volumeID core.UUID, attached bool) (*serversdk.ServerDetailSchema, error) {
	w := wait.New[*serversdk.ServerDetailSchema](serverPollInterval, serverOperationTimeout)
	return w.WaitFor(ctx, func(ctx context.Context) (*serversdk.ServerDetailSchema, error) {
		server, err := r.get(ctx, serverID)
		if err != nil {
			return nil, err
		}
		if server == nil {
			return nil, fmt.Errorf("server %s returned an empty response", serverID)
		}
		present := false
		for _, attachment := range server.Volumes {
			if attachment.Volume.Id == volumeID {
				if attachment.MountAs != "data" {
					return nil, fmt.Errorf("volume %s is a boot volume", volumeID)
				}
				present = true
			}
		}
		volume, err := r.blockStorageClient.GetVolume(ctx, volumeID, blockstoragesdk.GetVolumeParams{ProjectID: r.projectID})
		if !attached && errors.Is(err, blockstoragesdk.ErrNotFound) && !present {
			return server, nil
		}
		if err != nil {
			return nil, err
		}
		if volume == nil {
			return nil, fmt.Errorf("volume %s returned an empty response", volumeID)
		}
		if serverVolumeStatusFailed(volume.Status) {
			return nil, fmt.Errorf("volume %s entered terminal status %s", volumeID, volume.Status)
		}
		if !serverIsBusy(server) && present == attached && ((!attached && serverVolumeStatusReady(volume.Status)) || (attached && volume.Status == blockstoragesdk.VolumeStatusInUse)) {
			return server, nil
		}
		return nil, fmt.Errorf("volume %s attached=%t status=%s: %w", volumeID, present, volume.Status, wait.ErrNotReady)
	})
}

func (r *ServerResource) waitUntilDataVolumeAvailable(ctx context.Context, id core.UUID) error {
	w := wait.New[bool](serverPollInterval, serverOperationTimeout)
	_, err := w.WaitFor(ctx, func(ctx context.Context) (bool, error) {
		volume, err := r.blockStorageClient.GetVolume(ctx, id, blockstoragesdk.GetVolumeParams{ProjectID: r.projectID})
		if err != nil {
			return false, err
		}
		if volume == nil {
			return false, fmt.Errorf("volume %s returned an empty response", id)
		}
		if volume.Status == blockstoragesdk.VolumeStatusAvailable {
			return true, nil
		}
		if serverVolumeStatusFailed(volume.Status) {
			return false, fmt.Errorf("volume %s entered terminal status %s", id, volume.Status)
		}
		return false, fmt.Errorf("volume %s status=%s: %w", id, volume.Status, wait.ErrNotReady)
	})
	return err
}

func (r *ServerResource) waitUntilDataVolumeProperties(ctx context.Context, update dataVolumePropertyUpdate) error {
	w := wait.New[bool](serverPollInterval, serverOperationTimeout)
	_, err := w.WaitFor(ctx, func(ctx context.Context) (bool, error) {
		volume, err := r.blockStorageClient.GetVolume(ctx, update.id, blockstoragesdk.GetVolumeParams{ProjectID: r.projectID})
		if err != nil {
			return false, err
		}
		if volume == nil {
			return false, fmt.Errorf("volume %s returned an empty response", update.id)
		}
		if serverVolumeStatusFailed(volume.Status) {
			return false, fmt.Errorf("volume %s entered terminal status %s", update.id, volume.Status)
		}
		typeMatches := update.volumeTypeID == core.NilUUID || volume.CreateFrom.VolumeType != nil && volume.CreateFrom.VolumeType.Id == update.volumeTypeID
		iopsMatch := update.iops == nil || volume.Iops != nil && *volume.Iops == *update.iops
		if serverVolumeStatusReady(volume.Status) && typeMatches && iopsMatch {
			return true, nil
		}
		return false, fmt.Errorf("volume %s still reports previous properties: %w", update.id, wait.ErrNotReady)
	})
	return err
}

func (r *ServerResource) waitUntilDataVolumeDeleted(ctx context.Context, id core.UUID) error {
	// The volume can still report its previous status for a poll or two after
	// the backend accepts the delete. A ready status therefore means a failed
	// deletion only after the waiter observes deleting.
	sawDeleting := false
	w := wait.New[bool](serverPollInterval, serverOperationTimeout)
	_, err := w.WaitFor(ctx, func(ctx context.Context) (bool, error) {
		volume, err := r.blockStorageClient.GetVolume(ctx, id, blockstoragesdk.GetVolumeParams{ProjectID: r.projectID})
		if errors.Is(err, blockstoragesdk.ErrNotFound) {
			return true, nil
		}
		if err != nil {
			return false, err
		}
		if volume == nil {
			return false, fmt.Errorf("volume %s returned an empty response", id)
		}
		if volume.Status == blockstoragesdk.VolumeStatusDeleting {
			sawDeleting = true
		}
		if serverVolumeStatusFailed(volume.Status) || sawDeleting && serverVolumeStatusReady(volume.Status) {
			return false, fmt.Errorf("volume %s stopped deleting and settled in status %s", id, volume.Status)
		}
		return false, fmt.Errorf("volume %s status=%s: %w", id, volume.Status, wait.ErrNotReady)
	})
	return err
}

func (r *ServerResource) buildCreateBody(ctx context.Context, plan ServerResourceModel) (serversdk.ServerCreateSchema, diag.Diagnostics) {
	zoneFinder := projectlookup.NewZoneFinder(r.projectClient, r.projectID)
	flavorFinder := serverlookup.NewFlavorFinder(r.client, r.projectID)
	imageFinder := serverlookup.NewImageFinder(r.client, r.projectID)
	volumeTypeFinder := blockstoragelookup.NewVolumeTypeFinder(r.blockStorageClient)
	subnetFinder := networklookup.NewSubnetFinder(r.networkClient, r.projectID)
	return buildServerCreateBody(
		ctx,
		plan,
		func(ctx context.Context, name string) (projectsdk.ProjectZoneSchema, error) {
			return zoneFinder.Resolve(ctx, projectlookup.ZoneFilter{Name: &name})
		},
		flavorFinder.Resolve,
		imageFinder.Resolve,
		volumeTypeFinder.ResolveForCreate,
		func(ctx context.Context, cidr string) (networksdk.SubnetSchema, error) {
			return subnetFinder.Resolve(ctx, networklookup.SubnetFilter{CIDR: &cidr})
		},
	)
}

func (r *ServerResource) deleteTerminatedAttachments(ctx context.Context, state ServerResourceModel, diags *diag.Diagnostics) {
	privateIPIDs, elasticIPIDs, valueDiags := serverCascadeTargets(ctx, state)
	diags.Append(valueDiags...)
	if diags.HasError() {
		return
	}
	for _, id := range privateIPIDs {
		if err := r.deletePrivateIPWithRetry(ctx, id); err != nil && !errors.Is(err, networksdk.ErrNotFound) {
			diags.AddError("Error deleting server private IP", fmt.Sprintf("Unable to delete private IP %s: %s", id, err))
			continue
		}
		if err := r.waitUntilPrivateIPDeleted(ctx, id); err != nil {
			diags.AddError("Error waiting for server private IP deletion", fmt.Sprintf("Unable to confirm private IP %s was deleted: %s", id, err))
		}
	}
	for _, id := range elasticIPIDs {
		if err := r.deleteElasticIPWithRetry(ctx, id); err != nil && !errors.Is(err, networksdk.ErrNotFound) {
			diags.AddError("Error deleting server elastic IP", fmt.Sprintf("Unable to delete elastic IP %s: %s", id, err))
		}
	}
}

func (r *ServerResource) deleteElasticIPWithRetry(ctx context.Context, id core.UUID) error {
	w := wait.New[struct{}](privateIPDeleteInterval, privateIPDeleteTimeout)
	_, err := w.WaitFor(ctx, func(ctx context.Context) (struct{}, error) {
		err := r.networkClient.DeleteElasticIp(ctx, id, networksdk.DeleteElasticIpParams{ProjectID: r.projectID})
		switch {
		case err == nil || errors.Is(err, networksdk.ErrNotFound):
			return struct{}{}, nil
		case isElasticIPAttachedError(err):
			return struct{}{}, fmt.Errorf("elastic IP %s is not ready to delete: %s: %w", id, err, wait.ErrNotReady)
		default:
			return struct{}{}, err
		}
	})
	return err
}

func isElasticIPAttachedError(err error) bool {
	if errors.Is(err, networksdk.ErrConflict) {
		return true
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "attached") || strings.Contains(s, "associated") || strings.Contains(s, "in use") || strings.Contains(s, "in-use")
}

// deleteTerminatedVolumes deletes the volumes a destroyed server left behind,
// which the backend keeps. Each target is polled first because the attachment
// is released in the background and an in-use volume cannot be deleted.
func (r *ServerResource) deleteTerminatedVolumes(ctx context.Context, ids []core.UUID, diags *diag.Diagnostics) {
	for _, id := range ids {
		if err := r.waitUntilVolumeDetached(ctx, id); err != nil {
			diags.AddError("Error waiting for server volume to detach", err.Error())
			continue
		}
		if err := r.deleteVolumeWithRetry(ctx, id); err != nil && !errors.Is(err, blockstoragesdk.ErrNotFound) {
			diags.AddError("Error deleting server volume", fmt.Sprintf("Unable to delete volume %s: %s", id, err))
		}
	}
}

func (r *ServerResource) deleteVolumeWithRetry(ctx context.Context, id core.UUID) error {
	w := wait.New[struct{}](volumeDetachInterval, volumeDetachTimeout)
	_, err := w.WaitFor(ctx, func(ctx context.Context) (struct{}, error) {
		err := r.blockStorageClient.DeleteVolume(ctx, id, blockstoragesdk.DeleteVolumeParams{ProjectID: r.projectID})
		switch {
		case err == nil || errors.Is(err, blockstoragesdk.ErrNotFound):
			return struct{}{}, nil
		case isVolumeConflictError(err):
			return struct{}{}, fmt.Errorf("volume %s is not ready to delete: %s: %w", id, err, wait.ErrNotReady)
		default:
			return struct{}{}, err
		}
	})
	return err
}

func isVolumeConflictError(err error) bool {
	if errors.Is(err, blockstoragesdk.ErrConflict) {
		return true
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "dependent objects") ||
		strings.Contains(s, "conflict") ||
		strings.Contains(s, "in-use") ||
		strings.Contains(s, "in use") ||
		strings.Contains(s, "attached")
}

// waitUntilVolumeDetached waits for a volume to become deletable: until the
// backend finishes detaching it, a delete is rejected with "Volume with status
// in-use is not allowed to delete". A read that fails is reported as not-ready
// so a detach still in progress is not abandoned; an abandoned detach ends the
// wait, because the volume will not become available on its own.
func (r *ServerResource) waitUntilVolumeDetached(ctx context.Context, id core.UUID) error {
	w := wait.New[struct{}](volumeDetachInterval, volumeDetachTimeout)
	_, err := w.WaitFor(ctx, func(ctx context.Context) (struct{}, error) {
		volume, err := r.blockStorageClient.GetVolume(ctx, id, blockstoragesdk.GetVolumeParams{ProjectID: r.projectID})
		switch {
		case errors.Is(err, blockstoragesdk.ErrNotFound):
			return struct{}{}, nil
		case err != nil:
			return struct{}{}, fmt.Errorf("poll volume %s: %s: %w", id, err, wait.ErrNotReady)
		case volume == nil:
			return struct{}{}, fmt.Errorf("the API returned an empty response for volume %s: %w", id, wait.ErrNotReady)
		case volumeDeletable(volume.Status):
			return struct{}{}, nil
		case volumeDetachFailed(volume.Status):
			return struct{}{}, fmt.Errorf("volume %s stopped detaching and settled in status %q", id, volume.Status)
		}
		return struct{}{}, fmt.Errorf("volume %s is still attached (status=%s): %w", id, volume.Status, wait.ErrNotReady)
	})
	if err != nil {
		return fmt.Errorf("waiting for volume %s to detach: %w", id, err)
	}
	return nil
}

// volumeDeletable reports the statuses that end the detach wait. Available is
// the status the API accepts a deletion in; deleting means the backend already
// removed the volume, leaving the delete call a tolerated no-op.
func volumeDeletable(status blockstoragesdk.VolumeStatus) bool {
	return status == blockstoragesdk.VolumeStatusAvailable || status == blockstoragesdk.VolumeStatusDeleting
}

// volumeDetachFailed reports a detach the backend abandoned. Polling it to the
// deadline would only delay the destroy, and the status names the reason.
func volumeDetachFailed(status blockstoragesdk.VolumeStatus) bool {
	return status == blockstoragesdk.VolumeStatusDetachFailed || status == blockstoragesdk.VolumeStatusFailed
}

func (r *ServerResource) deleteServer(ctx context.Context, id core.UUID, stopErr error) diag.Diagnostics {
	var diags diag.Diagnostics
	if err := r.requestDeletion(ctx, id); err != nil {
		diags.AddError("Error deleting server", deletionErrorDetail(err, stopErr))
		return diags
	}
	if err := r.waitUntilDeleted(ctx, id); err != nil {
		diags.AddError("Error waiting for server deletion", deletionErrorDetail(err, stopErr))
	}
	return diags
}

// requestDeletion retries a failed delete when the error reports an operation
// in progress or the server detail shows a busy state. This lets deletion wait
// for provisioning to finish. Other failures return immediately.
func (r *ServerResource) requestDeletion(ctx context.Context, id core.UUID) error {
	w := wait.New[struct{}](serverPollInterval, serverOperationTimeout)
	_, err := w.WaitFor(ctx, func(ctx context.Context) (struct{}, error) {
		err := r.client.DeleteServer(ctx, id, serversdk.DeleteServerParams{ProjectID: r.projectID})
		if err == nil || errors.Is(err, serversdk.ErrNotFound) {
			return struct{}{}, nil
		}
		if !serverOperationInProgress(err) && !r.serverIsSettling(ctx, id) {
			return struct{}{}, err
		}
		return struct{}{}, fmt.Errorf("server %s has not accepted deletion yet: %s: %w", id, err, wait.ErrNotReady)
	})
	return err
}

// requestServerOperation waits for the backend's operation lock to be released.
// The detail response can show the target power state while the previous
// operation still holds the lock. Only an explicit busy rejection is replayed;
// transport failures and other conflicts retain the SDK's retry semantics.
func (r *ServerResource) requestServerOperation(ctx context.Context, id core.UUID, request func(context.Context) error) error {
	w := wait.New[struct{}](serverPollInterval, serverOperationTimeout)
	_, err := w.WaitFor(ctx, func(ctx context.Context) (struct{}, error) {
		err := request(ctx)
		if !serverOperationInProgress(err) {
			return struct{}{}, err
		}
		return struct{}{}, fmt.Errorf("server %s is still completing another operation: %s: %w", id, err, wait.ErrNotReady)
	})
	return err
}

// serverIsSettling reports whether a rejected request is worth retrying. An
// unreadable server is not treated as settling, so the rejection surfaces with
// the reason the backend gave instead of after the whole deadline.
func (r *ServerResource) serverIsSettling(ctx context.Context, id core.UUID) bool {
	server, err := r.get(ctx, id)
	if err != nil || server == nil {
		return false
	}
	return serverIsBusy(server)
}

func (r *ServerResource) stopBeforeDeletion(ctx context.Context, id core.UUID, server *serversdk.ServerDetailSchema) error {
	if serverIsShutdown(server) {
		return nil
	}
	if err := r.requestServerOperation(ctx, id, func(ctx context.Context) error {
		return r.client.StopServer(ctx, id, serversdk.StopServerParams{ProjectID: r.projectID})
	}); err != nil {
		if errors.Is(err, serversdk.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("stop server %s: %w", id, err)
	}
	if _, err := r.waitUntilShutdown(ctx, id); err != nil {
		return fmt.Errorf("wait for server %s shutdown: %w", id, err)
	}
	return nil
}

// --- Response-to-state refresh ---

func (r *ServerResource) refreshServerResourceState(
	ctx context.Context,
	server *serversdk.ServerDetailSchema,
	state *ServerResourceModel,
) diag.Diagnostics {
	var diags diag.Diagnostics
	diags.Append(r.initializeImportedFlavorState(ctx, server, state)...)
	if diags.HasError() {
		return diags
	}
	diags.Append(populateServerResourceState(ctx, server, state)...)
	if diags.HasError() || server == nil {
		return diags
	}
	diags.Append(r.refreshServerBootState(ctx, server, state)...)
	if diags.HasError() {
		return diags
	}
	diags.Append(r.refreshServerDataVolumeState(ctx, server, state)...)
	return diags
}

func (r *ServerResource) initializeImportedFlavorState(
	ctx context.Context,
	server *serversdk.ServerDetailSchema,
	state *ServerResourceModel,
) diag.Diagnostics {
	var diags diag.Diagnostics
	if server == nil || !state.Flavor.IsNull() && !state.Flavor.IsUnknown() {
		return diags
	}
	flavorID := server.Flavor.Id
	candidates, err := serverlookup.NewFlavorFinder(r.client, r.projectID).Find(ctx, serverlookup.FlavorFilter{ID: &flavorID})
	if err != nil {
		diags.AddError("Error identifying imported server flavor", err.Error())
		return diags
	}
	if len(candidates) > 1 {
		diags.AddError("Ambiguous imported server flavor", fmt.Sprintf("Flavor ID %s matched %d catalog entries.", flavorID, len(candidates)))
		return diags
	}
	values := map[string]attr.Value{
		"kind": types.StringValue("custom"), "name": types.StringNull(), "family": types.StringNull(),
		"vcpus": types.Int64Value(int64(server.Flavor.Vcpus)), "ram": types.Int64Value(int64(server.Flavor.Ram)),
	}
	if len(candidates) == 1 {
		values["kind"] = types.StringValue("predefined")
		values["name"] = types.StringValue(candidates[0].Name)
		values["vcpus"] = types.Int64Null()
		values["ram"] = types.Int64Null()
	}
	state.Flavor, diags = types.ObjectValue(flavorAttributeTypes(), values)
	return diags
}

func (r *ServerResource) refreshServerBootState(
	ctx context.Context,
	server *serversdk.ServerDetailSchema,
	state *ServerResourceModel,
) diag.Diagnostics {
	var diags diag.Diagnostics
	current, hasCurrent := decodeCurrentBoot(ctx, state.Boot, &diags)
	if diags.HasError() {
		return diags
	}

	rootVolumeID, hasRootVolume := serverRootVolumeID(server)
	var rootVolume *blockstoragesdk.VolumeDetailSchema
	// An explicitly configured existing-volume boot is already unambiguous from
	// the server attachment ID and does not depend on volume-origin metadata.
	if hasRootVolume && normalizedString(current.BootType) != "volume" {
		volume, err := r.blockStorageClient.GetVolume(
			ctx,
			rootVolumeID,
			blockstoragesdk.GetVolumeParams{ProjectID: r.projectID},
		)
		if err != nil {
			diags.AddError("Error reading server boot volume", fmt.Sprintf("Unable to read root volume %s: %s", rootVolumeID, err))
			return diags
		}
		if volume == nil {
			diags.AddError("Error reading server boot volume", fmt.Sprintf("The API returned an empty response for root volume %s.", rootVolumeID))
			return diags
		}
		rootVolume = volume
	}

	inferredBootType := inferServerBootType(server, rootVolume)
	bootType := selectServerBootType(current.BootType, inferredBootType, hasRootVolume)
	if bootType == "" {
		diags.AddError("Unable to determine server boot configuration", "The server response contains neither a root volume nor an image.")
		return diags
	}
	if !hasCurrent {
		current = BootInputModel{}
	}
	state.Boot = serverBootStateObject(server, rootVolume, rootVolumeID, bootType, current, &diags)
	if diags.HasError() {
		return diags
	}
	return diags
}

// buildBootVolumeResizePlan resolves a changed boot.volume_size against the
// server's root volume, which only the server response identifies. It returns
// nil when the size did not change.
func (r *ServerResource) buildBootVolumeResizePlan(
	ctx context.Context,
	serverID core.UUID,
	plan ServerResourceModel,
	state ServerResourceModel,
) (*bootVolumeUpdatePlan, diag.Diagnostics) {
	var diags diag.Diagnostics
	plannedBoot, hasPlannedBoot := decodeCurrentBoot(ctx, plan.Boot, &diags)
	priorBoot, hasPriorBoot := decodeCurrentBoot(ctx, state.Boot, &diags)
	if diags.HasError() {
		return nil, diags
	}
	if !hasPlannedBoot || !hasPriorBoot || plannedBoot.VolumeSize.Equal(priorBoot.VolumeSize) {
		return nil, diags
	}
	target, ok := increasedVolumeSize(plannedBoot.VolumeSize, priorBoot.VolumeSize, "boot.volume_size", &diags)
	if !ok {
		return nil, diags
	}
	server, err := r.get(ctx, serverID)
	if err != nil {
		diags.AddError("Error reading server before boot volume resize", err.Error())
		return nil, diags
	}
	if server == nil {
		diags.AddError("Error reading server before boot volume resize", emptyServerResponse(serverID))
		return nil, diags
	}
	id, found := serverRootVolumeID(server)
	if !found {
		diags.AddError("Unable to resize boot volume", "The server response does not identify a root volume.")
		return nil, diags
	}
	if !r.resolveVolumeResize(ctx, id, bootVolumeLabel(id), target, &diags) {
		return nil, diags
	}
	return &bootVolumeUpdatePlan{resizeID: id, resizeSize: target}, diags
}

// resolveVolumeResize checks target against the volume's current backend size,
// rejecting a shrink before anything is mutated. It reports false when the
// backend already reports target, so an extend is never sent with nothing to do.
func (r *ServerResource) resolveVolumeResize(
	ctx context.Context,
	id core.UUID,
	label string,
	target int,
	diags *diag.Diagnostics,
) bool {
	volume, err := r.blockStorageClient.GetVolume(
		ctx,
		id,
		blockstoragesdk.GetVolumeParams{ProjectID: r.projectID},
	)
	if err != nil {
		diags.AddError("Error reading server volume before resize", fmt.Sprintf("Unable to read %s: %s", label, err))
		return false
	}
	if volume == nil {
		diags.AddError("Error reading server volume before resize", fmt.Sprintf("The API returned an empty response for %s.", label))
		return false
	}
	if target < volume.Size {
		diags.AddError("Invalid server volume size update", fmt.Sprintf("%s can only increase from its current backend size of %d GiB; planned value is %d GiB.", label, volume.Size, target))
		return false
	}
	return target > volume.Size
}

// resizeVolume extends one server-owned volume and waits for its reported size.
func (r *ServerResource) resizeVolume(ctx context.Context, id core.UUID, size int, label string) diag.Diagnostics {
	var diags diag.Diagnostics
	body := blockstoragesdk.VolumeExtendSchema{Size: size}
	if err := r.blockStorageClient.ExtendVolume(
		ctx,
		id,
		blockstoragesdk.ExtendVolumeParams{ProjectID: r.projectID},
		body,
	); err != nil {
		diags.AddError("Error resizing server volume", fmt.Sprintf("Unable to resize %s: %s", label, err))
		return diags
	}
	if err := r.waitUntilVolumeResized(ctx, id, size); err != nil {
		diags.AddError("Error waiting for server volume resize", fmt.Sprintf("%s: %s", label, err))
	}
	return diags
}

// waitUntilVolumeResized waits until the volume reports the new size. A ready
// status is not proof on its own: the extend call only returns "accepted",
// and the volume keeps reporting the status it already had until the resize
// actually starts. A waiter that stops there returns before the new size
// exists, and the caller stores the old one. The reported size is the
// observable that settles. A backend that overshoots the requested size ends
// the wait too, rather than polling to the timeout; the honest value is then
// stored and Terraform reports the mismatch.
func (r *ServerResource) waitUntilVolumeResized(ctx context.Context, id core.UUID, size int) error {
	w := wait.New[*blockstoragesdk.VolumeDetailSchema](serverPollInterval, serverOperationTimeout)
	_, err := w.WaitFor(ctx, func(ctx context.Context) (*blockstoragesdk.VolumeDetailSchema, error) {
		volume, err := r.blockStorageClient.GetVolume(
			ctx,
			id,
			blockstoragesdk.GetVolumeParams{ProjectID: r.projectID},
		)
		if err != nil {
			return nil, fmt.Errorf("poll volume %s: %w", id, err)
		}
		if volume == nil {
			return nil, fmt.Errorf("volume %s returned an empty response", id)
		}
		if serverVolumeStatusReady(volume.Status) && volume.Size >= size {
			return volume, nil
		}
		if serverVolumeStatusFailed(volume.Status) {
			return nil, fmt.Errorf("volume %s entered terminal status %q while resizing to %d GiB", id, volume.Status, size)
		}
		return nil, fmt.Errorf("status=%s, size=%d GiB: %w", volume.Status, volume.Size, wait.ErrNotReady)
	})
	if err != nil {
		return fmt.Errorf("waiting for volume %s to reach %d GiB: %w", id, size, err)
	}
	return nil
}

// --- Update sequencing and per-attribute mutations ---

// applyServerUpdate coordinates all server mutations using a 3-phase pipeline
// to minimize downtime and ensure at most one stop/start power cycle:
// Live updates -> Offline updates -> Power state restoration
func (r *ServerResource) applyServerUpdate(
	ctx context.Context,
	serverID core.UUID,
	req serverUpdateRequest,
) diag.Diagnostics {
	var diags diag.Diagnostics

	wasRunning := powerStateIsRunning(req.priorPowerState)
	wasShutdown := powerStateIsShutdown(req.priorPowerState)
	explicitShutdown := req.changePowerState && isDesiredPowerState(req.desiredPowerState, serversdk.ServerPowerStateShutdown)

	// Rebuild and resize require the server to be in a known operational state.
	// Transient or frozen states (paused, suspended, pending, rescuing) must be
	// resolved by the practitioner before compute or OS disk modifications.
	if !wasRunning && !wasShutdown {
		if req.rebuild {
			diags.AddError(
				"Cannot rebuild server in current power state",
				fmt.Sprintf("Server is currently in %q power state. Rebuild requires the server to be either %q or %q.", req.priorPowerState.ValueString(), serversdk.ServerPowerStateRunning, serversdk.ServerPowerStateShutdown),
			)
			return diags
		}
		if req.resize {
			diags.AddError(
				"Cannot resize server in current power state",
				fmt.Sprintf("Server is currently in %q power state. Resize requires the server to be either %q or %q.", req.priorPowerState.ValueString(), serversdk.ServerPowerStateRunning, serversdk.ServerPowerStateShutdown),
			)
			return diags
		}
	}

	// --- Phase 1: Live updates on active server ---
	// Metadata, network configurations, and non-disruptive capacity changes are
	// applied first while the server remains in its prior power state.
	if req.patchServer {
		if _, err := r.client.PartialUpdateServer(
			ctx,
			serverID,
			serversdk.PartialUpdateServerParams{ProjectID: r.projectID},
			req.patchBody,
		); err != nil {
			diags.AddError("Error updating server", err.Error())
			return diags
		}
	}
	if req.updateBandwidth {
		diags.Append(r.updateBandwidth(ctx, serverID, req.bandwidth)...)
		if diags.HasError() {
			return diags
		}
	}
	if req.replaceGroups {
		diags.Append(r.updateSecurityGroups(ctx, serverID, req.securityGroupBody)...)
		if diags.HasError() {
			return diags
		}
	}
	if boot := req.attachments.boot; boot != nil {
		diags.Append(r.resizeVolume(ctx, boot.resizeID, boot.resizeSize, bootVolumeLabel(boot.resizeID))...)
		if diags.HasError() {
			return diags
		}
	}
	if data := req.attachments.dataVolume; data != nil {
		diags.Append(r.applyDataVolumeUpdate(ctx, serverID, data)...)
		if diags.HasError() {
			return diags
		}
	}
	if req.attachments.private != nil {
		diags.Append(r.applyPrivateIPUpdate(ctx, serverID, *req.attachments.private)...)
		if diags.HasError() {
			return diags
		}
	}
	if req.attachments.elastic != nil {
		diags.Append(r.applyElasticIPUpdate(ctx, serverID, *req.attachments.elastic)...)
		if diags.HasError() {
			return diags
		}
	}

	// Live resize can update capacity without stopping the server. A successful
	// resize is preserved even if subsequent offline steps fail.
	needsOfflineResize := false
	if req.resize {
		if wasRunning && !explicitShutdown {
			needsShutdown, resizeDiags := r.tryLiveResize(ctx, serverID, req.resizeBody)
			if resizeDiags.HasError() {
				return resizeDiags
			}
			needsOfflineResize = needsShutdown
		} else {
			needsOfflineResize = true
		}
	}

	// --- Phase 2: Offline updates within a single stop/start power cycle ---
	// Operations that cannot run live (rebuild, offline resize fallback, or
	// explicit shutdown) are grouped into at most one shutdown window.
	stoppedForUpdate := false
	needsStop := wasRunning && (explicitShutdown || req.rebuild || needsOfflineResize)
	if needsStop {
		if err := r.requestServerOperation(ctx, serverID, func(ctx context.Context) error {
			return r.client.StopServer(ctx, serverID, serversdk.StopServerParams{ProjectID: r.projectID})
		}); err != nil {
			diags.AddError("Error stopping server", err.Error())
			return diags
		}
		if _, err := r.waitUntilShutdown(ctx, serverID); err != nil {
			diags.AddError("Error waiting for server shutdown", err.Error())
			return diags
		}
		stoppedForUpdate = !explicitShutdown
	}

	if needsOfflineResize {
		diags.Append(r.resizeOfflineServer(ctx, serverID, req.resizeBody)...)
		if diags.HasError() {
			if stoppedForUpdate {
				r.startAfterOfflineUpdate(ctx, serverID, &diags)
			}
			return diags
		}
	}

	if req.rebuild {
		diags.Append(r.rebuildServer(ctx, serverID, req.rebuildBody, req.rebuiltImageID)...)
		if diags.HasError() {
			if stoppedForUpdate {
				r.startAfterOfflineUpdate(ctx, serverID, &diags)
			}
			return diags
		}
	}

	// --- Phase 3: Power state restoration and reconciliation ---
	if stoppedForUpdate {
		r.startAfterOfflineUpdate(ctx, serverID, &diags)
		if diags.HasError() {
			return diags
		}
	}

	if !wasRunning && req.changePowerState && isDesiredPowerState(req.desiredPowerState, serversdk.ServerPowerStateRunning) {
		if req.rebuild || needsOfflineResize {
			r.startAfterOfflineUpdate(ctx, serverID, &diags)
		} else {
			diags.Append(r.updatePowerState(ctx, serverID, req.desiredPowerState)...)
		}
	}

	return diags
}

func (r *ServerResource) startAfterOfflineUpdate(ctx context.Context, serverID core.UUID, diags *diag.Diagnostics) {
	// Resize and rebuild responses can expose their target values before the
	// server leaves its pending power state. Wait for that operation transition,
	// then verify the expected shutdown state before starting a new operation.
	server, err := r.waitUntilNotPending(ctx, serverID)
	if err != nil {
		diags.AddError("Error waiting to start server after offline update", err.Error())
		return
	}
	if !serverIsShutdown(server) {
		diags.AddError(
			"Error waiting to start server after offline update",
			fmt.Sprintf("Server %s left pending in power state %q; expected shutdown before start.", serverID, server.PowerState),
		)
		return
	}
	if err := r.requestServerOperation(ctx, serverID, func(ctx context.Context) error {
		return r.client.StartServer(ctx, serverID, serversdk.StartServerParams{ProjectID: r.projectID})
	}); err != nil {
		diags.AddError("Error starting server after offline update", err.Error())
		return
	}
	if err := r.waitUntilRunning(ctx, serverID); err != nil {
		diags.AddError("Error waiting for server start after offline update", err.Error())
	}
}

func (r *ServerResource) updateBandwidth(ctx context.Context, serverID core.UUID, bandwidth int) diag.Diagnostics {
	var diags diag.Diagnostics
	if err := r.client.UpdateServerBandwidth(
		ctx,
		serverID,
		serversdk.UpdateServerBandwidthParams{ProjectID: r.projectID},
		serversdk.ServerUpdateBandwidthSchema{Bandwidth: &bandwidth},
	); err != nil {
		diags.AddError("Error updating server bandwidth", err.Error())
		return diags
	}
	if err := r.waitUntilBandwidth(ctx, serverID, bandwidth); err != nil {
		diags.AddError("Error waiting for server bandwidth update", err.Error())
	}
	return diags
}

func (r *ServerResource) buildServerResizeBody(
	ctx context.Context,
	serverID core.UUID,
	flavorValue types.Object,
) (serversdk.ServerResizeSchema, diag.Diagnostics) {
	var diags diag.Diagnostics
	server, err := r.get(ctx, serverID)
	if err != nil {
		diags.AddError("Error reading server before resize", err.Error())
		return serversdk.ServerResizeSchema{}, diags
	}
	if server == nil {
		diags.AddError("Error reading server before resize", emptyServerResponse(serverID))
		return serversdk.ServerResizeSchema{}, diags
	}
	flavorFinder := serverlookup.NewFlavorFinder(r.client, r.projectID)
	flavor := buildFlavor(ctx, flavorValue, &server.Zone.Id, flavorFinder.Resolve, &diags)
	if diags.HasError() {
		return serversdk.ServerResizeSchema{}, diags
	}
	return serversdk.ServerResizeSchema{Flavor: flavor}, diags
}

// tryLiveResize attempts to resize the server while it is running. It sets
// live_resize=true and returns needsShutdown=true if the backend explicitly
// rejects live resize and demands a stopped server.
func (r *ServerResource) tryLiveResize(
	ctx context.Context,
	serverID core.UUID,
	body serversdk.ServerResizeSchema,
) (bool, diag.Diagnostics) {
	var diags diag.Diagnostics
	liveResize := true
	liveBody := body
	liveBody.LiveResize = &liveResize
	err := r.requestServerOperation(ctx, serverID, func(ctx context.Context) error {
		return r.client.ResizeServer(ctx, serverID, serversdk.ResizeServerParams{ProjectID: r.projectID}, liveBody)
	})
	if resizeRequiresShutdown(err) {
		return true, nil
	}
	if err != nil {
		diags.AddError("Error resizing server", err.Error())
		return false, diags
	}
	if err := r.waitUntilFlavor(ctx, serverID, body.Flavor); err != nil {
		diags.AddError("Error waiting for server resize", err.Error())
		return false, diags
	}
	return false, diags
}

// resizeOfflineServer issues the resize request against a server that is
// already shut down, using live_resize=false. The caller manages power state.
func (r *ServerResource) resizeOfflineServer(
	ctx context.Context,
	serverID core.UUID,
	body serversdk.ServerResizeSchema,
) diag.Diagnostics {
	var diags diag.Diagnostics
	offlineBody := body
	liveResize := false
	offlineBody.LiveResize = &liveResize
	err := r.requestServerOperation(ctx, serverID, func(ctx context.Context) error {
		return r.client.ResizeServer(ctx, serverID, serversdk.ResizeServerParams{ProjectID: r.projectID}, offlineBody)
	})
	if err != nil {
		diags.AddError("Error resizing stopped server", err.Error())
		return diags
	}
	if err := r.waitUntilFlavor(ctx, serverID, body.Flavor); err != nil {
		diags.AddError("Error waiting for server resize", err.Error())
	}
	return diags
}

func (r *ServerResource) waitUntilFlavor(ctx context.Context, serverID core.UUID, expected serversdk.ServerFlavor) error {
	return r.waitForServer(ctx, serverID, "resize", func(server *serversdk.ServerDetailSchema) (bool, error) {
		matches, err := serverFlavorMatches(server.Flavor, expected)
		return matches && !serverIsBusy(server), err
	})
}

func (r *ServerResource) rebuildServer(
	ctx context.Context,
	serverID core.UUID,
	body serversdk.ServerRebuildSchema,
	expectedImageID core.UUID,
) diag.Diagnostics {
	var diags diag.Diagnostics
	err := r.requestServerOperation(ctx, serverID, func(ctx context.Context) error {
		return r.client.RebuildServer(ctx, serverID, serversdk.RebuildServerParams{ProjectID: r.projectID}, body)
	})
	if err != nil {
		diags.AddError("Error rebuilding server", err.Error())
		return diags
	}
	if err := r.waitUntilImage(ctx, serverID, expectedImageID); err != nil {
		diags.AddError("Error waiting for server rebuild", err.Error())
	}
	return diags
}

func (r *ServerResource) waitUntilImage(ctx context.Context, serverID, expectedImageID core.UUID) error {
	return r.waitForServer(ctx, serverID, "rebuild", func(server *serversdk.ServerDetailSchema) (bool, error) {
		return server.Image != nil && server.Image.Id == expectedImageID && !serverIsBusy(server), nil
	})
}

func (r *ServerResource) updateSecurityGroups(
	ctx context.Context,
	serverID core.UUID,
	body serversdk.ServerUpdateSecurityGroupSchema,
) diag.Diagnostics {
	var diags diag.Diagnostics
	if err := r.client.UpdateServerSecurityGroup(
		ctx,
		serverID,
		serversdk.UpdateServerSecurityGroupParams{ProjectID: r.projectID},
		body,
	); err != nil {
		diags.AddError("Error updating server security groups", err.Error())
		return diags
	}
	expected := make(map[core.UUID]struct{}, len(body.SecurityGroups))
	for _, securityGroup := range body.SecurityGroups {
		expected[securityGroup.Id] = struct{}{}
	}
	if err := r.waitUntilSecurityGroups(ctx, serverID, expected); err != nil {
		diags.AddError("Error waiting for server security group update", err.Error())
	}
	return diags
}

func (r *ServerResource) waitUntilSecurityGroups(ctx context.Context, serverID core.UUID, expected map[core.UUID]struct{}) error {
	return r.waitForServer(ctx, serverID, "security group update", func(server *serversdk.ServerDetailSchema) (bool, error) {
		if len(server.SecurityGroups) != len(expected) {
			return false, nil
		}
		for _, securityGroup := range server.SecurityGroups {
			if _, ok := expected[securityGroup.Id]; !ok {
				return false, nil
			}
		}
		return !serverIsBusy(server), nil
	})
}

func (r *ServerResource) updatePowerState(ctx context.Context, serverID core.UUID, desired types.String) diag.Diagnostics {
	diags := validateDesiredPowerState(desired)
	if diags.HasError() || desired.IsNull() || desired.IsUnknown() {
		return diags
	}
	switch strings.TrimSpace(desired.ValueString()) {
	case string(serversdk.ServerPowerStateRunning):
		if err := r.requestServerOperation(ctx, serverID, func(ctx context.Context) error {
			return r.client.StartServer(ctx, serverID, serversdk.StartServerParams{ProjectID: r.projectID})
		}); err != nil {
			diags.AddError("Error starting server", err.Error())
			return diags
		}
		if err := r.waitUntilRunning(ctx, serverID); err != nil {
			diags.AddError("Error waiting for server start", err.Error())
		}
	case string(serversdk.ServerPowerStateShutdown):
		if err := r.requestServerOperation(ctx, serverID, func(ctx context.Context) error {
			return r.client.StopServer(ctx, serverID, serversdk.StopServerParams{ProjectID: r.projectID})
		}); err != nil {
			diags.AddError("Error stopping server", err.Error())
			return diags
		}
		if _, err := r.waitUntilShutdown(ctx, serverID); err != nil {
			diags.AddError("Error waiting for server stop", err.Error())
		}
	}
	return diags
}

func (r *ServerResource) waitUntilRunning(ctx context.Context, serverID core.UUID) error {
	return r.waitForServer(ctx, serverID, "start", func(server *serversdk.ServerDetailSchema) (bool, error) {
		if server.PowerState == serversdk.ServerPowerStateRunning && !serverIsBusy(server) {
			return true, nil
		}
		if server.Status == serversdk.ServerStatusDeleted {
			return false, fmt.Errorf("server %s was deleted while waiting to start", serverID)
		}
		return false, nil
	})
}

// --- Private-IP and elastic-IP attachment reconciliation ---

func (r *ServerResource) buildAttachmentUpdatePlan(
	ctx context.Context,
	serverID core.UUID,
	plan ServerResourceModel,
	state ServerResourceModel,
) (attachmentUpdatePlan, diag.Diagnostics) {
	var result attachmentUpdatePlan
	var diags diag.Diagnostics
	if privateIPsChanged(plan, state) {
		privatePlan, privateDiags := r.buildPrivateIPUpdatePlan(ctx, plan.PrivateIPs, state.PrivateIPs)
		diags.Append(privateDiags...)
		if diags.HasError() {
			return result, diags
		}
		result.private = &privatePlan
	}
	if elasticIPsChanged(plan, state) {
		elasticPlan, elasticDiags := r.buildElasticIPUpdatePlan(ctx, serverID, plan.ElasticIPs, state.ElasticIPs)
		diags.Append(elasticDiags...)
		if diags.HasError() {
			return result, diags
		}
		result.elastic = &elasticPlan
	}
	bootPlan, bootDiags := r.buildBootVolumeResizePlan(ctx, serverID, plan, state)
	diags.Append(bootDiags...)
	if diags.HasError() {
		return result, diags
	}
	result.boot = bootPlan
	dataVolumePlan, dataVolumeDiags := r.buildDataVolumeUpdatePlan(ctx, plan, state)
	diags.Append(dataVolumeDiags...)
	if diags.HasError() {
		return result, diags
	}
	result.dataVolume = dataVolumePlan
	return result, diags
}

func (r *ServerResource) updatePrivateIPs(
	ctx context.Context,
	serverID core.UUID,
	plannedValue types.List,
	priorValue types.List,
) diag.Diagnostics {
	plan, diags := r.buildPrivateIPUpdatePlan(ctx, plannedValue, priorValue)
	if diags.HasError() {
		return diags
	}
	return r.applyPrivateIPUpdate(ctx, serverID, plan)
}

func (r *ServerResource) buildPrivateIPUpdatePlan(
	ctx context.Context,
	plannedValue types.List,
	priorValue types.List,
) (privateIPUpdatePlan, diag.Diagnostics) {
	planned, prior, diags := decodePrivateIPChanges(ctx, plannedValue, priorValue)
	plan := privateIPUpdatePlan{
		expectedCount: len(planned),
		desiredIDs:    make(map[core.UUID]struct{}, len(planned)),
		removedIDs:    make(map[core.UUID]struct{}, len(prior)),
		existingIDs:   make([]core.UUID, 0, len(planned)),
		subnetIDs:     make([]core.UUID, 0, len(planned)),
	}
	if diags.HasError() {
		return plan, diags
	}
	matches, usedPrior := matchPrivateIPConfigs(planned, prior)
	subnetFinder := networklookup.NewSubnetFinder(r.networkClient, r.projectID)
	for i := range planned {
		if matches[i] >= 0 {
			id, idDiags := parse.UUIDString(planned[i].ID, fmt.Sprintf("private_ips[%d].id", i))
			diags.Append(idDiags...)
			if !idDiags.HasError() {
				plan.desiredIDs[id] = struct{}{}
			}
			continue
		}
		switch normalizedString(planned[i].Kind) {
		case "ip":
			id, idDiags := parse.UUIDString(planned[i].ID, fmt.Sprintf("private_ips[%d].id", i))
			diags.Append(idDiags...)
			if !idDiags.HasError() {
				plan.desiredIDs[id] = struct{}{}
				plan.existingIDs = append(plan.existingIDs, id)
			}
		case "subnet":
			if !planned[i].ID.IsNull() && !planned[i].ID.IsUnknown() {
				diags.AddError("Invalid private IP update", fmt.Sprintf("private_ips[%d].id must be omitted when changing a subnet attachment.", i))
				continue
			}
			subnetID, subnetDiags := resolvePrivateIPSubnet(ctx, planned[i], i, subnetFinder.Resolve)
			diags.Append(subnetDiags...)
			if !subnetDiags.HasError() {
				plan.subnetIDs = append(plan.subnetIDs, subnetID)
			}
		default:
			diags.AddError("Invalid private IP kind", fmt.Sprintf("private_ips[%d].kind must be subnet or ip.", i))
		}
	}
	for i, priorIP := range prior {
		if usedPrior[i] || priorIP.ID.IsNull() || priorIP.ID.IsUnknown() {
			continue
		}
		id, idDiags := parse.UUIDString(priorIP.ID, fmt.Sprintf("prior private_ips[%d].id", i))
		diags.Append(idDiags...)
		if idDiags.HasError() {
			continue
		}
		plan.removedIDs[id] = struct{}{}
		// Delete provider-created subnet IPs only when delete_on_termination
		// requests cleanup. Otherwise detaching deliberately preserves the IP.
		if normalizedString(priorIP.Kind) == "subnet" && boolValueOrDefault(priorIP.DeleteOnTermination, false) {
			plan.deletedIDs = append(plan.deletedIDs, id)
		}
	}
	return plan, diags
}

func (r *ServerResource) applyPrivateIPUpdate(
	ctx context.Context,
	serverID core.UUID,
	plan privateIPUpdatePlan,
) diag.Diagnostics {
	var diags diag.Diagnostics
	for id := range plan.removedIDs {
		if err := r.client.DetachPrivateIp(
			ctx,
			serverID,
			serversdk.DetachPrivateIpParams{ProjectID: r.projectID},
			serversdk.ServerDetachPrivateIPSchema{PrivateIpId: id},
		); err != nil {
			diags.AddError("Error detaching server private IP", fmt.Sprintf("Unable to detach private IP %s: %s", id, err))
			return diags
		}
	}

	for _, id := range plan.existingIDs {
		if err := r.client.AttachPrivateIp(
			ctx,
			serverID,
			serversdk.AttachPrivateIpParams{ProjectID: r.projectID},
			serversdk.ServerAttachPrivateIPSchema{PrivateIpId: id},
		); err != nil {
			diags.AddError("Error attaching server private IP", fmt.Sprintf("Unable to attach private IP %s: %s", id, err))
			return diags
		}
	}
	for _, subnetID := range plan.subnetIDs {
		id, attachDiags := r.attachNewPrivateIP(ctx, serverID, subnetID)
		diags.Append(attachDiags...)
		if diags.HasError() {
			return diags
		}
		plan.desiredIDs[id] = struct{}{}
	}
	if err := r.waitUntilPrivateIPs(ctx, serverID, plan.expectedCount, plan.desiredIDs, plan.removedIDs); err != nil {
		diags.AddError("Error waiting for server private IP update", err.Error())
		return diags
	}
	diags.Append(r.deletePrivateIPs(ctx, plan.deletedIDs)...)
	return diags
}

// attachNewPrivateIP allocates a private IP in the subnet and attaches it.
// Allocating explicitly means a failed attach cannot lose the ID of a private
// IP created inside AttachServerSubnet. The attachment keeps kind=subnet and
// its configured deletion policy in Terraform state.
func (r *ServerResource) attachNewPrivateIP(ctx context.Context, serverID, subnetID core.UUID) (core.UUID, diag.Diagnostics) {
	var diags diag.Diagnostics
	created, err := r.networkClient.CreatePrivateIp(ctx, networksdk.CreatePrivateIpParams{ProjectID: r.projectID},
		networksdk.PrivateIPCreateSchema{SubnetId: subnetID})
	if err != nil {
		diags.AddError("Error allocating server private IP", fmt.Sprintf("Unable to allocate a private IP in subnet %s: %s", subnetID, err))
		return core.NilUUID, diags
	}
	if created == nil {
		diags.AddError("Error allocating server private IP", "The API returned an empty private IP response.")
		return core.NilUUID, diags
	}
	err = r.requestServerOperation(ctx, serverID, func(ctx context.Context) error {
		return r.client.AttachPrivateIp(ctx, serverID, serversdk.AttachPrivateIpParams{ProjectID: r.projectID},
			serversdk.ServerAttachPrivateIPSchema{PrivateIpId: created.Id})
	})
	if err != nil {
		diags.AddError("Error attaching server private IP", fmt.Sprintf("Unable to attach allocated private IP %s in subnet %s: %s", created.Id, subnetID, err))
		diags.Append(r.deletePrivateIPs(ctx, []core.UUID{created.Id})...)
		return created.Id, diags
	}
	return created.Id, diags
}

// deletePrivateIPs deletes the private IPs that the provider created and waits
// until the backend has really removed each one. DeletePrivateIp only accepts
// the request. Returning while an address still exists lets Terraform go on to
// destroy the subnet that holds it. The backend refuses to delete a subnet that
// still has private IPs in it.
func (r *ServerResource) deletePrivateIPs(ctx context.Context, ids []core.UUID) diag.Diagnostics {
	var diags diag.Diagnostics
	for _, id := range ids {
		if err := r.networkClient.DeletePrivateIp(
			ctx,
			id,
			networksdk.DeletePrivateIpParams{ProjectID: r.projectID},
		); err != nil && !errors.Is(err, networksdk.ErrNotFound) {
			diags.AddError("Error deleting server private IP", fmt.Sprintf("Unable to delete private IP %s: %s", id, err))
			continue
		}
		if err := r.waitUntilPrivateIPDeleted(ctx, id); err != nil {
			diags.AddError("Error waiting for server private IP deletion", fmt.Sprintf("Unable to confirm private IP %s was deleted: %s", id, err))
		}
	}
	return diags
}

func (r *ServerResource) deletePrivateIPWithRetry(ctx context.Context, id core.UUID) error {
	w := wait.New[struct{}](privateIPDeleteInterval, privateIPDeleteTimeout)
	_, err := w.WaitFor(ctx, func(ctx context.Context) (struct{}, error) {
		err := r.networkClient.DeletePrivateIp(
			ctx,
			id,
			networksdk.DeletePrivateIpParams{ProjectID: r.projectID},
		)
		switch {
		case err == nil || errors.Is(err, networksdk.ErrNotFound):
			return struct{}{}, nil
		case isPrivateIPAttachedError(err):
			return struct{}{}, fmt.Errorf("private IP %s is not ready to delete: %s: %w", id, err, wait.ErrNotReady)
		default:
			return struct{}{}, err
		}
	})
	return err
}

func isPrivateIPAttachedError(err error) bool {
	if errors.Is(err, networksdk.ErrConflict) {
		return true
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "attached") || strings.Contains(s, "in use") || strings.Contains(s, "in-use") || strings.Contains(s, "dependent")
}

func (r *ServerResource) waitUntilPrivateIPDeleted(ctx context.Context, id core.UUID) error {
	w := wait.New[struct{}](privateIPDeleteInterval, privateIPDeleteTimeout)
	_, err := w.WaitFor(ctx, func(ctx context.Context) (struct{}, error) {
		privateIP, err := r.networkClient.GetPrivateIp(ctx, id, networksdk.GetPrivateIpParams{ProjectID: r.projectID})
		if errors.Is(err, networksdk.ErrNotFound) || err == nil && privateIP == nil {
			return struct{}{}, nil
		}
		if err != nil {
			return struct{}{}, err
		}
		return struct{}{}, fmt.Errorf("private IP %s still exists: %w", id, wait.ErrNotReady)
	})
	return err
}

func (r *ServerResource) buildElasticIPUpdatePlan(
	ctx context.Context,
	serverID core.UUID,
	plannedValue types.List,
	priorValue types.List,
) (elasticIPUpdatePlan, diag.Diagnostics) {
	planned, prior, diags := decodeElasticIPChanges(ctx, plannedValue, priorValue)
	plan := elasticIPUpdatePlan{
		expectedCount: len(planned),
		desiredIDs:    make(map[core.UUID]struct{}, len(planned)),
		removedIDs:    make(map[core.UUID]struct{}, len(prior)),
		existingIDs:   make([]core.UUID, 0, len(planned)),
		newConfigs:    make([]ElasticIPInputModel, 0, len(planned)),
	}
	if diags.HasError() {
		return plan, diags
	}
	matches, usedPrior := matchElasticIPConfigs(planned, prior)
	for i := range planned {
		if matches[i] >= 0 {
			id, idDiags := parse.UUIDString(planned[i].ID, fmt.Sprintf("elastic_ips[%d].id", i))
			diags.Append(idDiags...)
			if !idDiags.HasError() {
				plan.desiredIDs[id] = struct{}{}
			}
			continue
		}
		switch normalizedString(planned[i].Kind) {
		case "existing":
			id, idDiags := parse.UUIDString(planned[i].ID, fmt.Sprintf("elastic_ips[%d].id", i))
			diags.Append(idDiags...)
			if !idDiags.HasError() {
				plan.desiredIDs[id] = struct{}{}
				plan.existingIDs = append(plan.existingIDs, id)
			}
		case "new":
			if !planned[i].ID.IsNull() && !planned[i].ID.IsUnknown() {
				diags.AddError("Invalid elastic IP update", fmt.Sprintf("elastic_ips[%d].id must be omitted when changing a new elastic IP attachment.", i))
				continue
			}
			if planned[i].EnableIPv4.IsUnknown() || planned[i].EnableIPv6.IsUnknown() {
				diags.AddError("Unknown new elastic IP address family", fmt.Sprintf("elastic_ips[%d].enable_ipv4 and enable_ipv6 must be known when configured.", i))
				continue
			}
			if !boolValueOrDefault(planned[i].EnableIPv4, true) && !boolValueOrDefault(planned[i].EnableIPv6, false) {
				diags.AddError("Invalid new elastic IP", fmt.Sprintf("elastic_ips[%d] must enable IPv4, IPv6, or both.", i))
				continue
			}
			plan.newConfigs = append(plan.newConfigs, planned[i])
		default:
			diags.AddError("Invalid elastic IP kind", fmt.Sprintf("elastic_ips[%d].kind must be existing or new.", i))
		}
	}
	for i, priorIP := range prior {
		if usedPrior[i] || priorIP.ID.IsNull() || priorIP.ID.IsUnknown() {
			continue
		}
		id, idDiags := parse.UUIDString(priorIP.ID, fmt.Sprintf("prior elastic_ips[%d].id", i))
		diags.Append(idDiags...)
		if idDiags.HasError() {
			continue
		}
		plan.removedIDs[id] = struct{}{}
		// Delete provider-created elastic IPs only when delete_on_termination
		// requests cleanup. Otherwise detaching deliberately preserves the IP.
		if normalizedString(priorIP.Kind) == "new" && boolValueOrDefault(priorIP.DeleteOnTermination, false) {
			plan.deletedIDs = append(plan.deletedIDs, id)
		}
	}
	if len(plan.newConfigs) > 0 {
		resolvedRegionID, regionDiags := r.serverRegionID(ctx, serverID)
		diags.Append(regionDiags...)
		plan.regionID = resolvedRegionID
	}
	return plan, diags
}

func (r *ServerResource) applyElasticIPUpdate(
	ctx context.Context,
	serverID core.UUID,
	plan elasticIPUpdatePlan,
) diag.Diagnostics {
	var diags diag.Diagnostics
	for id := range plan.removedIDs {
		if err := r.client.DetachServerEip(
			ctx,
			serverID,
			serversdk.DetachServerEipParams{ProjectID: r.projectID},
			serversdk.ServerElasticIPAttachSchema{EipId: id},
		); err != nil {
			diags.AddError("Error detaching server elastic IP", fmt.Sprintf("Unable to detach elastic IP %s: %s", id, err))
			return diags
		}
	}

	for _, id := range plan.existingIDs {
		if err := r.attachElasticIP(ctx, serverID, id); err != nil {
			diags.AddError("Error attaching server elastic IP", fmt.Sprintf("Unable to attach elastic IP %s: %s", id, err))
			return diags
		}
	}
	for _, plannedIP := range plan.newConfigs {
		enableIPv4 := boolValueOrDefault(plannedIP.EnableIPv4, true)
		enableIPv6 := boolValueOrDefault(plannedIP.EnableIPv6, false)
		created, err := r.networkClient.CreateElasticIp(
			ctx,
			networksdk.CreateElasticIpParams{ProjectID: r.projectID},
			networksdk.ElasticIPCreateSchema{
				RegionId:   plan.regionID,
				EnableIpv4: &enableIPv4,
				EnableIpv6: &enableIPv6,
			},
		)
		if err != nil {
			diags.AddError("Error creating server elastic IP", err.Error())
			return diags
		}
		if created == nil {
			diags.AddError("Error creating server elastic IP", "The API returned an empty elastic IP response.")
			return diags
		}
		if err := r.attachCreatedElasticIP(ctx, serverID, created.Id, enableIPv4, enableIPv6); err != nil {
			cleanupErr := r.networkClient.DeleteElasticIp(ctx, created.Id, networksdk.DeleteElasticIpParams{ProjectID: r.projectID})
			if cleanupErr != nil && !errors.Is(cleanupErr, networksdk.ErrNotFound) {
				diags.AddError(
					"Error attaching server elastic IP",
					fmt.Sprintf("Unable to attach elastic IP %s: %s. Cleanup also failed: %s", created.Id, err, cleanupErr),
				)
			} else {
				diags.AddError("Error attaching server elastic IP", fmt.Sprintf("Unable to attach elastic IP %s: %s", created.Id, err))
			}
			return diags
		}
		plan.desiredIDs[created.Id] = struct{}{}
	}
	if err := r.waitUntilElasticIPs(ctx, serverID, plan.expectedCount, plan.desiredIDs, plan.removedIDs); err != nil {
		diags.AddError("Error waiting for server elastic IP update", err.Error())
		return diags
	}
	for _, id := range plan.deletedIDs {
		if err := r.networkClient.DeleteElasticIp(
			ctx,
			id,
			networksdk.DeleteElasticIpParams{ProjectID: r.projectID},
		); err != nil && !errors.Is(err, networksdk.ErrNotFound) {
			diags.AddError("Error deleting detached server elastic IP", fmt.Sprintf("Unable to delete elastic IP %s: %s", id, err))
		}
	}
	return diags
}

// attachCreatedElasticIP attaches an elastic IP that this provider just
// created. The network API returns the elastic IP as soon as it is recorded,
// but its address is allocated upstream a moment later. Attaching one that
// early is what the backend rejects with a transient provider error. Waiting
// for the address first keeps a request that only needed a moment from failing
// the apply, and the standalone elastic IP resource waits for the same signal.
func (r *ServerResource) attachCreatedElasticIP(
	ctx context.Context,
	serverID, elasticIPID core.UUID,
	enableIPv4, enableIPv6 bool,
) error {
	if err := r.waitUntilElasticIPAllocated(ctx, elasticIPID, enableIPv4, enableIPv6); err != nil {
		return err
	}
	return r.attachElasticIP(ctx, serverID, elasticIPID)
}

// waitUntilElasticIPAllocated waits until the elastic IP carries an address for
// every family it was created with.
func (r *ServerResource) waitUntilElasticIPAllocated(
	ctx context.Context,
	elasticIPID core.UUID,
	enableIPv4, enableIPv6 bool,
) error {
	w := wait.New[struct{}](serverPollInterval, serverOperationTimeout)
	_, err := w.WaitFor(ctx, func(ctx context.Context) (struct{}, error) {
		elasticIP, err := r.networkClient.GetElasticIp(ctx, elasticIPID, networksdk.GetElasticIpParams{ProjectID: r.projectID})
		switch {
		case errors.Is(err, networksdk.ErrNotFound):
			// A create the backend has not finished recording reads as missing.
			return struct{}{}, fmt.Errorf("not found yet: %w", wait.ErrNotReady)
		case err != nil:
			return struct{}{}, err
		case elasticIP == nil:
			return struct{}{}, fmt.Errorf("the API returned an empty response: %w", wait.ErrNotReady)
		case enableIPv4 && (elasticIP.IpAddress == nil || *elasticIP.IpAddress == ""):
			return struct{}{}, fmt.Errorf("no IPv4 assigned yet: %w", wait.ErrNotReady)
		case enableIPv6 && (elasticIP.Ipv6Address == nil || *elasticIP.Ipv6Address == ""):
			return struct{}{}, fmt.Errorf("no IPv6 assigned yet: %w", wait.ErrNotReady)
		}
		return struct{}{}, nil
	})
	if err != nil {
		return fmt.Errorf("waiting for elastic IP %s to be allocated: %w", elasticIPID, err)
	}
	return nil
}

// attachElasticIP replays an attach that the backend could not handle. The SDK
// transport leaves a POST alone because replaying one is not generally safe.
// This request only associates one elastic IP with one server, so a
// server-side refusal is worth repeating instead of failing the whole apply. A
// refusal that hid a successful attach shows up as the elastic IP already
// being on the server, which counts as done. A refusal for any other reason is
// returned as-is so the practitioner sees why the request was rejected.
func (r *ServerResource) attachElasticIP(ctx context.Context, serverID, elasticIPID core.UUID) error {
	w := wait.New[struct{}](serverPollInterval, elasticIPAttachTimeout)
	_, err := w.WaitFor(ctx, func(ctx context.Context) (struct{}, error) {
		attachErr := r.client.AttachServerEip(
			ctx,
			serverID,
			serversdk.AttachServerEipParams{ProjectID: r.projectID},
			serversdk.ServerElasticIPAttachSchema{EipId: elasticIPID},
		)
		if attachErr == nil || !isTransientBackendError(attachErr) {
			return struct{}{}, attachErr
		}
		if attached, checkErr := r.serverHasElasticIP(ctx, serverID, elasticIPID); checkErr == nil && attached {
			return struct{}{}, nil
		}
		return struct{}{}, fmt.Errorf("the backend could not handle the attach: %s: %w", attachErr, wait.ErrNotReady)
	})
	return err
}

func (r *ServerResource) serverHasElasticIP(ctx context.Context, serverID, elasticIPID core.UUID) (bool, error) {
	server, err := r.get(ctx, serverID)
	if err != nil || server == nil {
		return false, err
	}
	for _, elasticIP := range server.ElasticIps {
		if elasticIP.Id == elasticIPID {
			return true, nil
		}
	}
	return false, nil
}

func (r *ServerResource) serverRegionID(ctx context.Context, serverID core.UUID) (core.UUID, diag.Diagnostics) {
	var diags diag.Diagnostics
	server, err := r.get(ctx, serverID)
	if err != nil {
		diags.AddError("Error reading server zone", err.Error())
		return core.UUID{}, diags
	}
	zoneName := server.Zone.Name
	zone, err := projectlookup.NewZoneFinder(r.projectClient, r.projectID).Resolve(ctx, projectlookup.ZoneFilter{Name: &zoneName})
	if err != nil {
		diags.AddError("Unable to resolve server zone", err.Error())
		return core.UUID{}, diags
	}
	return zone.Region.Id, diags
}

func (r *ServerResource) waitUntilPrivateIPs(
	ctx context.Context,
	serverID core.UUID,
	expectedCount int,
	expectedIDs map[core.UUID]struct{},
	removedIDs map[core.UUID]struct{},
) error {
	return r.waitForServer(ctx, serverID, "private IP update", func(server *serversdk.ServerDetailSchema) (bool, error) {
		if len(server.PrivateIps) != expectedCount {
			return false, nil
		}
		actual := make(map[core.UUID]struct{}, len(server.PrivateIps))
		for _, privateIP := range server.PrivateIps {
			actual[privateIP.Id] = struct{}{}
		}
		// The backend lists private IPs before assigning their addresses.
		// Check every entry, including subnet IPs whose IDs were unknown in
		// the plan, so downstream resources receive allocated addresses.
		return privateIPAddressesAssigned(server.PrivateIps) &&
			containsAttachmentIDs(actual, expectedIDs) &&
			excludesAttachmentIDs(actual, removedIDs) &&
			!serverIsBusy(server), nil
	})
}

func (r *ServerResource) waitUntilElasticIPs(
	ctx context.Context,
	serverID core.UUID,
	expectedCount int,
	expectedIDs map[core.UUID]struct{},
	removedIDs map[core.UUID]struct{},
) error {
	return r.waitForServer(ctx, serverID, "elastic IP update", func(server *serversdk.ServerDetailSchema) (bool, error) {
		if len(server.ElasticIps) != expectedCount {
			return false, nil
		}
		actual := make(map[core.UUID]struct{}, len(server.ElasticIps))
		for _, elasticIP := range server.ElasticIps {
			actual[elasticIP.Id] = struct{}{}
		}
		// A freshly attached elastic IP can appear before address allocation.
		// Wait for at least one IPv4 or IPv6 address; IPv6-only IPs may keep
		// ip_address null.
		return elasticIPAddressesAssigned(server.ElasticIps) &&
			containsAttachmentIDs(actual, expectedIDs) &&
			excludesAttachmentIDs(actual, removedIDs) &&
			!serverIsBusy(server), nil
	})
}

// --- Boot volume plan modifiers ---

func (bootIOPSStatePlanModifier) Description(context.Context) string {
	return "Preserves allocated boot IOPS while the boot volume size and type stay unchanged."
}

func (m bootIOPSStatePlanModifier) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

// PlanModifyInt64 works like UseStateForUnknown, except that a resize or a
// retype leaves unconfigured IOPS unknown, because the backend recalculates it.
func (bootIOPSStatePlanModifier) PlanModifyInt64(ctx context.Context, req planmodifier.Int64Request, resp *planmodifier.Int64Response) {
	if req.StateValue.IsNull() || !req.PlanValue.IsUnknown() || req.ConfigValue.IsUnknown() {
		return
	}
	parent := req.Path.ParentPath()
	var plannedSize, priorSize types.Int64
	var plannedType, priorType types.Object
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, parent.AtName("volume_size"), &plannedSize)...)
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, parent.AtName("volume_size"), &priorSize)...)
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, parent.AtName("volume_type"), &plannedType)...)
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, parent.AtName("volume_type"), &priorType)...)
	// The selector ID is computed and can be unknown on any update, so only
	// the criteria decide whether the volume type changed.
	if resp.Diagnostics.HasError() || !plannedSize.Equal(priorSize) || !selector.Equal(plannedType, priorType) {
		return
	}
	resp.PlanValue = req.StateValue
}

// --- Attachment list plan modifiers ---

func (dataVolumeStatePlanModifier) Description(context.Context) string {
	return "Preserves computed values by data-volume identity."
}

func (m dataVolumeStatePlanModifier) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (dataVolumeStatePlanModifier) PlanModifyList(ctx context.Context, req planmodifier.ListRequest, resp *planmodifier.ListResponse) {
	if preserveUnconfiguredAttachmentList(req, resp) || req.PlanValue.IsNull() || req.PlanValue.IsUnknown() {
		return
	}
	if req.StateValue.IsNull() || req.StateValue.IsUnknown() {
		planCreatedDataVolumeTypes(ctx, req, resp)
		return
	}
	var planned, configured, prior []DataVolumeInputModel
	resp.Diagnostics.Append(req.PlanValue.ElementsAs(ctx, &planned, false)...)
	resp.Diagnostics.Append(req.ConfigValue.ElementsAs(ctx, &configured, false)...)
	resp.Diagnostics.Append(req.StateValue.ElementsAs(ctx, &prior, false)...)
	if resp.Diagnostics.HasError() {
		return
	}
	matches, _, matchDiags := matchDataVolumeConfigs(configured, prior)
	resp.Diagnostics.Append(matchDiags...)
	if resp.Diagnostics.HasError() {
		return
	}
	for i := range planned {
		if i >= len(configured) {
			continue
		}
		planned[i].Attached = types.BoolValue(true)
		if matches[i] < 0 {
			if dataVolumeKind(configured[i]) == "new" {
				planned[i].ID = types.StringUnknown()
				planned[i].VolumeType = selector.WithID(configured[i].VolumeType, types.StringUnknown())
			}
			if configured[i].IOPS.IsNull() {
				planned[i].IOPS = types.Int64Unknown()
			}
			if configured[i].DeleteOnTermination.IsNull() {
				planned[i].DeleteOnTermination = types.BoolUnknown()
			}
			if configured[i].VolumeType.IsNull() {
				planned[i].VolumeType = types.ObjectUnknown(blockstorageselector.VolumeTypeAttributeTypes())
			}
			if configured[i].VolumeSize.IsNull() {
				planned[i].VolumeSize = types.Int64Unknown()
			}
			continue
		}
		old := prior[matches[i]]
		if configured[i].ID.IsNull() || configured[i].ID.IsUnknown() {
			planned[i].ID = old.ID
		} else {
			planned[i].ID = configured[i].ID
		}
		if configured[i].VolumeType.IsNull() {
			planned[i].VolumeType = old.VolumeType
		} else if selector.Equal(configured[i].VolumeType, old.VolumeType) {
			planned[i].VolumeType = selector.WithID(configured[i].VolumeType, old.VolumeType.Attributes()["id"].(types.String))
		} else {
			planned[i].VolumeType = selector.WithID(configured[i].VolumeType, types.StringUnknown())
		}
		if configured[i].VolumeSize.IsNull() {
			planned[i].VolumeSize = old.VolumeSize
		}
		if configured[i].IOPS.IsNull() {
			// Resize and retype can change backend-assigned IOPS.
			if (selector.Equal(configured[i].VolumeType, old.VolumeType) && planned[i].VolumeSize.Equal(old.VolumeSize)) || dataVolumeKind(configured[i]) == "existing" {
				planned[i].IOPS = old.IOPS
			} else {
				planned[i].IOPS = types.Int64Unknown()
			}
		}
		if configured[i].DeleteOnTermination.IsNull() {
			if dataVolumeKind(configured[i]) == dataVolumeKind(old) {
				planned[i].DeleteOnTermination = old.DeleteOnTermination
			} else {
				planned[i].DeleteOnTermination = types.BoolValue(dataVolumeKind(configured[i]) == "new")
			}
		}
	}
	value, diags := types.ListValueFrom(ctx, req.PlanValue.ElementType(ctx), planned)
	resp.Diagnostics.Append(diags...)
	if !diags.HasError() {
		resp.PlanValue = value
	}
}

// planCreatedDataVolumeTypes plans each configured volume type selector as
// configured. Its criteria are Computed only so that an omitted selector keeps
// its prior value. Without this, the framework plans every unconfigured
// criterion unknown on create, and apply cannot resolve the selector.
func planCreatedDataVolumeTypes(ctx context.Context, req planmodifier.ListRequest, resp *planmodifier.ListResponse) {
	var planned, configured []DataVolumeInputModel
	resp.Diagnostics.Append(req.PlanValue.ElementsAs(ctx, &planned, false)...)
	resp.Diagnostics.Append(req.ConfigValue.ElementsAs(ctx, &configured, false)...)
	if resp.Diagnostics.HasError() {
		return
	}
	for i := range min(len(planned), len(configured)) {
		if !configured[i].VolumeType.IsNull() {
			planned[i].VolumeType = selector.WithID(configured[i].VolumeType, types.StringUnknown())
		}
	}
	value, diags := types.ListValueFrom(ctx, req.PlanValue.ElementType(ctx), planned)
	resp.Diagnostics.Append(diags...)
	if !diags.HasError() {
		resp.PlanValue = value
	}
}

func (privateIPStatePlanModifier) Description(context.Context) string {
	return "Preserves computed private IP values only while the attachment configuration is unchanged."
}

func (privateIPStatePlanModifier) MarkdownDescription(context.Context) string {
	return "Preserves computed private IP values only while the attachment configuration is unchanged."
}

func (privateIPStatePlanModifier) PlanModifyList(ctx context.Context, req planmodifier.ListRequest, resp *planmodifier.ListResponse) {
	if preserveUnconfiguredAttachmentList(req, resp) || req.PlanValue.IsNull() || req.PlanValue.IsUnknown() || req.ConfigValue.IsNull() {
		return
	}

	var planned []PrivateIPInputModel
	var configured []PrivateIPInputModel
	resp.Diagnostics.Append(req.PlanValue.ElementsAs(ctx, &planned, false)...)
	resp.Diagnostics.Append(req.ConfigValue.ElementsAs(ctx, &configured, false)...)
	if resp.Diagnostics.HasError() {
		return
	}
	planPrivateIPInputs(planned, configured)
	if !req.StateValue.IsNull() && !req.StateValue.IsUnknown() {
		var prior []PrivateIPInputModel
		resp.Diagnostics.Append(req.StateValue.ElementsAs(ctx, &prior, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
		preservePrivateIPComputedValues(planned, configured, prior)
	}
	value, diags := types.ListValueFrom(ctx, req.PlanValue.ElementType(ctx), planned)
	resp.Diagnostics.Append(diags...)
	if !diags.HasError() {
		resp.PlanValue = value
	}
}

func (elasticIPStatePlanModifier) Description(context.Context) string {
	return "Preserves computed elastic IP values only while the attachment configuration is unchanged."
}

func (elasticIPStatePlanModifier) MarkdownDescription(context.Context) string {
	return "Preserves computed elastic IP values only while the attachment configuration is unchanged."
}

func (elasticIPStatePlanModifier) PlanModifyList(ctx context.Context, req planmodifier.ListRequest, resp *planmodifier.ListResponse) {
	if preserveUnconfiguredAttachmentList(req, resp) || req.PlanValue.IsNull() || req.PlanValue.IsUnknown() || req.ConfigValue.IsNull() {
		return
	}

	var planned []ElasticIPInputModel
	var configured []ElasticIPInputModel
	resp.Diagnostics.Append(req.PlanValue.ElementsAs(ctx, &planned, false)...)
	resp.Diagnostics.Append(req.ConfigValue.ElementsAs(ctx, &configured, false)...)
	if resp.Diagnostics.HasError() {
		return
	}
	planElasticIPInputs(planned, configured)
	if !req.StateValue.IsNull() && !req.StateValue.IsUnknown() {
		var prior []ElasticIPInputModel
		resp.Diagnostics.Append(req.StateValue.ElementsAs(ctx, &prior, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
		preserveElasticIPComputedValues(planned, configured, prior)
	}
	value, diags := types.ListValueFrom(ctx, req.PlanValue.ElementType(ctx), planned)
	resp.Diagnostics.Append(diags...)
	if !diags.HasError() {
		resp.PlanValue = value
	}
}

// --- Server request and diff helpers ---

func emptyServerResponse(id core.UUID) string {
	return fmt.Sprintf("The API returned an empty response for server %s.", id)
}

func deletionErrorDetail(err, stopErr error) string {
	if stopErr == nil {
		return err.Error()
	}
	return fmt.Sprintf("%s\n\nStopping the server before deletion also failed: %s", err, stopErr)
}

func serverIsShutdown(server *serversdk.ServerDetailSchema) bool {
	return server.PowerState == serversdk.ServerPowerStateShutdown ||
		server.Status == serversdk.ServerStatusShutoff || server.Status == serversdk.ServerStatusStopped
}

func isTransientEnumError(err error) bool {
	var apiErr *serversdk.APIError
	return errors.As(err, &apiErr) && apiErr.Code() == "enum"
}

// isTransientBackendError reports whether the backend refused the request for
// a reason that is worth repeating: a server-side failure, rate limiting, or
// provider_error from a busy upstream. That code is matched independently
// because the backend does not always pair it with a 5xx status. Gating the
// replay on the status class alone would let the very refusal this exists for
// fail the apply on the first attempt.
func isTransientBackendError(err error) bool {
	if errors.Is(err, serversdk.ErrInternal) || errors.Is(err, serversdk.ErrRateLimited) {
		return true
	}
	var apiErr *serversdk.APIError
	return errors.As(err, &apiErr) && apiErr.Code() == transientBackendErrorCode
}

func buildServerUpdateBody(plan, state ServerResourceModel) (serversdk.ServerPartialUpdateSchema, bool, diag.Diagnostics) {
	var body serversdk.ServerPartialUpdateSchema
	var diags diag.Diagnostics
	if plan.Name.IsNull() || plan.Name.IsUnknown() || strings.TrimSpace(plan.Name.ValueString()) == "" {
		diags.AddError("Missing server name", "name must be configured and known.")
		return body, false, diags
	}
	name := strings.TrimSpace(plan.Name.ValueString())
	if state.Name.IsNull() || state.Name.IsUnknown() || strings.TrimSpace(state.Name.ValueString()) != name {
		body.Name = &name
	}
	if !plan.Description.IsNull() && !plan.Description.IsUnknown() &&
		(state.Description.IsNull() || state.Description.IsUnknown() || !plan.Description.Equal(state.Description)) {
		description := plan.Description.ValueString()
		body.Description = &description
	}
	return body, body.Name != nil || body.Description != nil, diags
}

func bandwidthChanged(plan, state ServerResourceModel) bool {
	return !plan.Bandwidth.IsNull() && !plan.Bandwidth.IsUnknown() &&
		(state.Bandwidth.IsNull() || state.Bandwidth.IsUnknown() || !plan.Bandwidth.Equal(state.Bandwidth))
}

// bootVolumeLabel and dataVolumeLabel name a volume in resize diagnostics.
// The ID is what identifies it: an update plan keeps only the volumes whose
// size changed, so a position in that plan would not match the data_volumes
// index a practitioner reads in their configuration.
func bootVolumeLabel(id core.UUID) string {
	return fmt.Sprintf("boot volume %s", id)
}

func dataVolumeLabel(id core.UUID) string {
	return fmt.Sprintf("data volume %s", id)
}

// increasedVolumeSize rejects unknown, removed, or shrinking size changes.
func increasedVolumeSize(planned, prior types.Int64, name string, diags *diag.Diagnostics) (int, bool) {
	if planned.IsNull() || planned.IsUnknown() || prior.IsNull() || prior.IsUnknown() {
		diags.AddError("Invalid server volume size update", fmt.Sprintf("%s must be known in both the plan and prior state.", name))
		return 0, false
	}
	if planned.ValueInt64() <= prior.ValueInt64() {
		diags.AddError("Invalid server volume size update", fmt.Sprintf("%s can only increase from %d GiB; planned value is %d GiB.", name, prior.ValueInt64(), planned.ValueInt64()))
		return 0, false
	}
	return int(planned.ValueInt64()), true
}

// serverVolumeStatusReady identifies statuses that can finish an extend.
func serverVolumeStatusReady(status blockstoragesdk.VolumeStatus) bool {
	return status == blockstoragesdk.VolumeStatusAvailable || status == blockstoragesdk.VolumeStatusInUse
}

// serverVolumeStatusFailed identifies terminal block-storage failures.
func serverVolumeStatusFailed(status blockstoragesdk.VolumeStatus) bool {
	switch status {
	case blockstoragesdk.VolumeStatusAttachFailed,
		blockstoragesdk.VolumeStatusBackupFailed,
		blockstoragesdk.VolumeStatusDetachFailed,
		blockstoragesdk.VolumeStatusExtendFailed,
		blockstoragesdk.VolumeStatusFailed,
		blockstoragesdk.VolumeStatusRestoreFailed:
		return true
	default:
		return false
	}
}

func serverCascadeTargets(ctx context.Context, state ServerResourceModel) ([]core.UUID, []core.UUID, diag.Diagnostics) {
	var diags diag.Diagnostics
	privateIPIDs := make([]core.UUID, 0)
	elasticIPIDs := make([]core.UUID, 0)
	if !state.PrivateIPs.IsNull() && !state.PrivateIPs.IsUnknown() {
		var values []PrivateIPInputModel
		diags.Append(state.PrivateIPs.ElementsAs(ctx, &values, false)...)
		for i, value := range values {
			if value.DeleteOnTermination.IsNull() || value.DeleteOnTermination.IsUnknown() || !value.DeleteOnTermination.ValueBool() {
				continue
			}
			if value.ID.IsNull() || value.ID.IsUnknown() || strings.TrimSpace(value.ID.ValueString()) == "" {
				continue
			}
			id, valueDiags := parse.UUIDString(value.ID, fmt.Sprintf("private_ips[%d].id", i))
			diags.Append(valueDiags...)
			if !valueDiags.HasError() {
				privateIPIDs = append(privateIPIDs, id)
			}
		}
	}
	if !state.ElasticIPs.IsNull() && !state.ElasticIPs.IsUnknown() {
		var values []ElasticIPInputModel
		diags.Append(state.ElasticIPs.ElementsAs(ctx, &values, false)...)
		for i, value := range values {
			if value.DeleteOnTermination.IsNull() || value.DeleteOnTermination.IsUnknown() || !value.DeleteOnTermination.ValueBool() {
				continue
			}
			if value.ID.IsNull() || value.ID.IsUnknown() || strings.TrimSpace(value.ID.ValueString()) == "" {
				continue
			}
			id, valueDiags := parse.UUIDString(value.ID, fmt.Sprintf("elastic_ips[%d].id", i))
			diags.Append(valueDiags...)
			if !valueDiags.HasError() {
				elasticIPIDs = append(elasticIPIDs, id)
			}
		}
	}
	return privateIPIDs, elasticIPIDs, diags
}

// serverVolumeCascadeTargets lists the volumes to delete once the server is
// gone. Data volumes carry their own stable ID in state, so a configured
// volume is matched against the reported attachments by that ID rather than
// by position, which the server does not guarantee to preserve. The
// auto-created boot volume has no such ID, so it is resolved from the
// reported attachments directly. Either kind survives when the server does
// not report it, having been detached outside Terraform.
func serverVolumeCascadeTargets(
	ctx context.Context,
	state ServerResourceModel,
	server *serversdk.ServerDetailSchema,
) ([]core.UUID, diag.Diagnostics) {
	var diags diag.Diagnostics
	ids := make([]core.UUID, 0)
	if boot, ok := decodeConfigObject[BootInputModel](ctx, state.Boot, &diags); ok {
		rootVolumeID, hasRootVolume := serverRootVolumeID(server)
		fallback := bootVolumeDeletedByDefault(normalizedString(boot.BootType))
		if hasRootVolume && boolValueOrDefault(boot.DeleteOnTermination, fallback) {
			ids = append(ids, rootVolumeID)
		}
	}
	if volumes, ok := decodeConfigList[DataVolumeInputModel](ctx, state.DataVolumes, &diags); ok {
		attached := make(map[string]struct{}, len(server.Volumes))
		for _, id := range dataVolumeIDs(server) {
			attached[id] = struct{}{}
		}
		for i, volume := range volumes {
			if !boolValueOrDefault(volume.DeleteOnTermination, dataVolumeKind(volume) == "new") {
				continue
			}
			if volume.ID.IsNull() || volume.ID.IsUnknown() || strings.TrimSpace(volume.ID.ValueString()) == "" {
				continue
			}
			id, idDiags := parse.UUIDString(volume.ID, fmt.Sprintf("data_volumes[%d].id", i))
			diags.Append(idDiags...)
			if idDiags.HasError() {
				continue
			}
			if _, ok := attached[id.String()]; ok || dataVolumeKind(volume) == "new" {
				ids = append(ids, id)
			}
		}
	}
	return ids, diags
}

// --- Update decision helpers ---

func resizeRequiresShutdown(err error) bool {
	if err == nil {
		return false
	}
	var apiErr *serversdk.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	for _, detail := range apiErr.Errors {
		message := strings.ToLower(strings.TrimSpace(detail.Message))
		if detail.Code == "invalid_input" && strings.Contains(message, "cannot resize server with power state running") {
			return true
		}
	}
	return false
}

func serverFlavorMatches(actual serversdk.NestedFlavorSchema, expected serversdk.ServerFlavor) (bool, error) {
	discriminator, err := expected.Discriminator()
	if err != nil {
		return false, fmt.Errorf("read resize flavor discriminator: %w", err)
	}
	switch discriminator {
	case "predefined":
		predefined, err := expected.AsPredefinedServerFlavor()
		if err != nil {
			return false, fmt.Errorf("decode predefined resize flavor: %w", err)
		}
		return actual.Id == predefined.Id, nil
	case "custom":
		custom, err := expected.AsCustomServerFlavor()
		if err != nil {
			return false, fmt.Errorf("decode custom resize flavor: %w", err)
		}
		return actual.Vcpus == custom.Vcpus && actual.Ram == custom.Ram, nil
	default:
		return false, fmt.Errorf("unsupported resize flavor discriminator %q", discriminator)
	}
}

func buildServerRebuildBody(
	ctx context.Context,
	bootValue types.Object,
	projectID core.UUID,
	resolveImage serverlookup.ImageResolveFunc,
) (serversdk.ServerRebuildSchema, core.UUID, diag.Diagnostics) {
	body := serversdk.ServerRebuildSchema{ProjectId: projectID}
	var diags diag.Diagnostics
	if bootValue.IsNull() || bootValue.IsUnknown() {
		diags.AddError("Missing boot configuration", "boot must be configured and known.")
		return body, core.UUID{}, diags
	}
	var boot BootInputModel
	diags.Append(bootValue.As(ctx, &boot, basetypes.ObjectAsOptions{})...)
	if diags.HasError() || boot.BootType.IsNull() || boot.BootType.IsUnknown() {
		return body, core.UUID{}, diags
	}
	switch strings.TrimSpace(boot.BootType.ValueString()) {
	case "image", "local_disk":
		image, imageDiags := serverselector.ResolveImage(ctx, boot.Image, "boot.image", resolveImage)
		diags.Append(imageDiags...)
		if !imageDiags.HasError() {
			body.ImageId = &image.Id
			return body, image.Id, diags
		}
	case "custom_image":
		imageID, imageDiags := parse.UUIDString(reference.Get(boot.CustomImage, reference.ID), "boot.custom_image.id")
		diags.Append(imageDiags...)
		if !imageDiags.HasError() {
			body.CustomImageId = &imageID
			return body, imageID, diags
		}
	default:
		diags.AddError("Unsupported server rebuild", "The configured boot type cannot be rebuilt in place.")
	}
	return body, core.UUID{}, diags
}

func buildSecurityGroupUpdateBody(value types.Set) (serversdk.ServerUpdateSecurityGroupSchema, diag.Diagnostics) {
	body := serversdk.ServerUpdateSecurityGroupSchema{SecurityGroups: make([]serversdk.ServerSecurityGroup, 0, len(value.Elements()))}
	var diags diag.Diagnostics
	if value.IsNull() || value.IsUnknown() {
		return body, diags
	}
	body.SecurityGroups = append(body.SecurityGroups, securityGroupReferences(value, &diags)...)
	return body, diags
}

func flavorChanged(ctx context.Context, plan, state ServerResourceModel) (bool, diag.Diagnostics) {
	var diags diag.Diagnostics
	if plan.Flavor.IsNull() || plan.Flavor.IsUnknown() {
		return false, diags
	}
	if state.Flavor.IsNull() || state.Flavor.IsUnknown() {
		return true, diags
	}
	var planned FlavorInputModel
	var prior FlavorInputModel
	diags.Append(plan.Flavor.As(ctx, &planned, basetypes.ObjectAsOptions{})...)
	diags.Append(state.Flavor.As(ctx, &prior, basetypes.ObjectAsOptions{})...)
	if diags.HasError() {
		return false, diags
	}
	if normalizedString(planned.Kind) != normalizedString(prior.Kind) {
		return true, diags
	}
	switch normalizedString(planned.Kind) {
	case "predefined":
		return normalizedString(planned.Name) != normalizedString(prior.Name), diags
	case "custom":
		familyChanged := normalizedString(prior.Family) != "" && normalizedString(planned.Family) != normalizedString(prior.Family)
		return familyChanged || !planned.VCPUs.Equal(prior.VCPUs) || !planned.RAM.Equal(prior.RAM), diags
	default:
		return !plan.Flavor.Equal(state.Flavor), diags
	}
}

func bootSourceChanged(ctx context.Context, plan, state ServerResourceModel) (bool, diag.Diagnostics) {
	var diags diag.Diagnostics
	if plan.Boot.IsNull() || plan.Boot.IsUnknown() || state.Boot.IsNull() || state.Boot.IsUnknown() {
		return false, diags
	}
	var plannedBoot BootInputModel
	var priorBoot BootInputModel
	diags.Append(plan.Boot.As(ctx, &plannedBoot, basetypes.ObjectAsOptions{})...)
	diags.Append(state.Boot.As(ctx, &priorBoot, basetypes.ObjectAsOptions{})...)
	if diags.HasError() || plannedBoot.BootType.IsNull() || plannedBoot.BootType.IsUnknown() ||
		priorBoot.BootType.IsNull() || priorBoot.BootType.IsUnknown() ||
		strings.TrimSpace(plannedBoot.BootType.ValueString()) != strings.TrimSpace(priorBoot.BootType.ValueString()) {
		return false, diags
	}
	switch strings.TrimSpace(plannedBoot.BootType.ValueString()) {
	case "image", "local_disk":
		return selector.Pending(plannedBoot.Image), diags
	case "custom_image":
		return referenceCriterion(plannedBoot.CustomImage, reference.ID) != referenceCriterion(priorBoot.CustomImage, reference.ID), diags
	default:
		return false, diags
	}
}

func securityGroupsChanged(plan, state ServerResourceModel) bool {
	return !plan.SecurityGroups.IsNull() && !plan.SecurityGroups.IsUnknown() &&
		!plan.SecurityGroups.Equal(state.SecurityGroups)
}

func validateDesiredPowerState(value types.String) diag.Diagnostics {
	var diags diag.Diagnostics
	if value.IsNull() || value.IsUnknown() {
		return diags
	}
	desired := strings.TrimSpace(value.ValueString())
	if desired != string(serversdk.ServerPowerStateRunning) && desired != string(serversdk.ServerPowerStateShutdown) {
		diags.AddError("Invalid server power state", `power_state must be "running" or "shutdown".`)
	}
	return diags
}

func isDesiredPowerState(value types.String, expected serversdk.ServerPowerState) bool {
	return !value.IsNull() && !value.IsUnknown() && strings.TrimSpace(value.ValueString()) == string(expected)
}

func powerStateIsRunning(value types.String) bool {
	if value.IsNull() || value.IsUnknown() {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(value.ValueString()), string(serversdk.ServerPowerStateRunning))
}

func powerStateIsShutdown(value types.String) bool {
	if value.IsNull() || value.IsUnknown() {
		return false
	}
	s := strings.ToLower(strings.TrimSpace(value.ValueString()))
	return s == string(serversdk.ServerPowerStateShutdown) || s == "shutoff" || s == "stopped"
}

func powerStateChanged(plan, state ServerResourceModel) bool {
	return !plan.PowerState.IsNull() && !plan.PowerState.IsUnknown() &&
		normalizedString(plan.PowerState) != normalizedString(state.PowerState)
}

func serverOperationInProgress(err error) bool {
	var apiErr *serversdk.APIError
	if !errors.Is(err, serversdk.ErrConflict) || !errors.As(err, &apiErr) {
		return false
	}
	for _, detail := range apiErr.Errors {
		if detail.Code == "conflict" && strings.Contains(strings.ToLower(detail.Message), "another operation running on this server") {
			return true
		}
	}
	return false
}

func serverIsBusy(server *serversdk.ServerDetailSchema) bool {
	return server.Status == serversdk.ServerStatusBuilding || server.PowerState == serversdk.ServerPowerStatePending
}

// --- Attachment matching and settling helpers ---

func resolvePrivateIPSubnet(
	ctx context.Context,
	config PrivateIPInputModel,
	index int,
	resolve func(context.Context, networklookup.SubnetFilter) (networksdk.SubnetSchema, error),
) (core.UUID, diag.Diagnostics) {
	return privateIPSubnetID(ctx, config, index, func(ctx context.Context, cidr string) (networksdk.SubnetSchema, error) {
		return resolve(ctx, networklookup.SubnetFilter{CIDR: &cidr})
	})
}

// privateIPSubnetID returns the subnet that a kind subnet private IP selects,
// by its configured ID or by resolving its configured CIDR.
func privateIPSubnetID(
	ctx context.Context,
	config PrivateIPInputModel,
	index int,
	resolveSubnet subnetResolveFunc,
) (core.UUID, diag.Diagnostics) {
	var diags diag.Diagnostics
	subnetID := reference.Get(config.Subnet, reference.ID)
	cidr := reference.Get(config.Subnet, subnetCIDR)
	hasID := normalizedString(subnetID) != ""
	hasCIDR := normalizedString(cidr) != ""
	if hasID == hasCIDR {
		diags.AddError("Invalid subnet reference", fmt.Sprintf("private_ips[%d].subnet must configure exactly one of id or cidr.", index))
		return core.UUID{}, diags
	}
	if hasID {
		id, idDiags := parse.UUIDString(subnetID, fmt.Sprintf("private_ips[%d].subnet.id", index))
		diags.Append(idDiags...)
		return id, diags
	}
	subnet, subnetDiags := resolveConfiguredName(
		ctx,
		cidr,
		fmt.Sprintf("private_ips[%d].subnet.cidr", index),
		"subnet",
		resolveSubnet,
	)
	diags.Append(subnetDiags...)
	return subnet.Id, diags
}

func decodePrivateIPChanges(ctx context.Context, plannedValue, priorValue types.List) ([]PrivateIPInputModel, []PrivateIPInputModel, diag.Diagnostics) {
	var planned []PrivateIPInputModel
	var prior []PrivateIPInputModel
	var diags diag.Diagnostics
	diags.Append(plannedValue.ElementsAs(ctx, &planned, false)...)
	if !priorValue.IsNull() && !priorValue.IsUnknown() {
		diags.Append(priorValue.ElementsAs(ctx, &prior, false)...)
	}
	return planned, prior, diags
}

func decodeElasticIPChanges(ctx context.Context, plannedValue, priorValue types.List) ([]ElasticIPInputModel, []ElasticIPInputModel, diag.Diagnostics) {
	var planned []ElasticIPInputModel
	var prior []ElasticIPInputModel
	var diags diag.Diagnostics
	diags.Append(plannedValue.ElementsAs(ctx, &planned, false)...)
	if !priorValue.IsNull() && !priorValue.IsUnknown() {
		diags.Append(priorValue.ElementsAs(ctx, &prior, false)...)
	}
	return planned, prior, diags
}

func matchPrivateIPConfigs(planned, prior []PrivateIPInputModel) ([]int, []bool) {
	matches := make([]int, len(planned))
	usedPrior := make([]bool, len(prior))
	for i := range matches {
		matches[i] = -1
	}
	for plannedIndex := range planned {
		plannedIP := &planned[plannedIndex]
		for j := range prior {
			if !usedPrior[j] && privateIPPlanIdentityEqual(*plannedIP, prior[j]) {
				plannedIP.ID = prior[j].ID
				matches[plannedIndex] = j
				usedPrior[j] = true
				break
			}
		}
	}
	return matches, usedPrior
}

func matchElasticIPConfigs(planned, prior []ElasticIPInputModel) ([]int, []bool) {
	matches := make([]int, len(planned))
	usedPrior := make([]bool, len(prior))
	for i := range matches {
		matches[i] = -1
	}
	for plannedIndex := range planned {
		plannedIP := &planned[plannedIndex]
		for j := range prior {
			if !usedPrior[j] && elasticIPPlanIdentityEqual(*plannedIP, prior[j]) {
				plannedIP.ID = prior[j].ID
				matches[plannedIndex] = j
				usedPrior[j] = true
				break
			}
		}
	}
	return matches, usedPrior
}

func normalizedString(value types.String) string {
	if value.IsNull() || value.IsUnknown() {
		return ""
	}
	return strings.TrimSpace(value.ValueString())
}

// referenceCriterion returns one criterion of a reference, trimmed, or an empty
// string when the reference or the criterion is null or unknown.
func referenceCriterion(value types.Object, criterion string) string {
	return normalizedString(reference.Get(value, criterion))
}

func boolValueOrDefault(value types.Bool, fallback bool) bool {
	if value.IsNull() || value.IsUnknown() {
		return fallback
	}
	return value.ValueBool()
}

// privateIPAddressesAssigned reports whether every attachment already carries
// its address. The backend lists an attachment before its address is allocated,
// and refreshing that response into state records a null ip_address for an
// attachment that does have one moments later.
func privateIPAddressesAssigned(privateIPs []serversdk.NestedPrivateIPSchema) bool {
	for _, privateIP := range privateIPs {
		if privateIP.IpAddress == nil {
			return false
		}
	}
	return true
}

// elasticIPAddressesAssigned is the counterpart to privateIPAddressesAssigned.
// Either address family satisfies it because an elastic IP can be IPv6 only.
func elasticIPAddressesAssigned(elasticIPs []serversdk.NestedElasticIPSchema) bool {
	for _, elasticIP := range elasticIPs {
		if elasticIP.IpAddress == nil && elasticIP.Ipv6Address == nil {
			return false
		}
	}
	return true
}

func containsAttachmentIDs(actual, expected map[core.UUID]struct{}) bool {
	for id := range expected {
		if _, ok := actual[id]; !ok {
			return false
		}
	}
	return true
}

func excludesAttachmentIDs(actual, excluded map[core.UUID]struct{}) bool {
	for id := range excluded {
		if _, ok := actual[id]; ok {
			return false
		}
	}
	return true
}

func privateIPsChanged(plan, state ServerResourceModel) bool {
	return !plan.PrivateIPs.IsNull() && !plan.PrivateIPs.IsUnknown() && !plan.PrivateIPs.Equal(state.PrivateIPs)
}

func elasticIPsChanged(plan, state ServerResourceModel) bool {
	return !plan.ElasticIPs.IsNull() && !plan.ElasticIPs.IsUnknown() && !plan.ElasticIPs.Equal(state.ElasticIPs)
}

// --- Attachment plan-modifier helpers ---

func preservePrivateIPState() planmodifier.List {
	return privateIPStatePlanModifier{}
}

func preserveElasticIPState() planmodifier.List {
	return elasticIPStatePlanModifier{}
}

func preserveUnconfiguredAttachmentList(req planmodifier.ListRequest, resp *planmodifier.ListResponse) bool {
	if !req.PlanValue.IsUnknown() {
		return false
	}
	if req.ConfigValue.IsUnknown() {
		return true
	}
	if req.ConfigValue.IsNull() && !req.StateValue.IsNull() && !req.StateValue.IsUnknown() {
		resp.PlanValue = req.StateValue
	}
	return true
}

// planPrivateIPInputs plans every input as configured. The inputs are Computed
// only so that an omitted list keeps its prior value, so a configured element
// must not inherit an input from the prior element at the same index.
func planPrivateIPInputs(planned, configured []PrivateIPInputModel) {
	for i := range planned {
		if i < len(configured) {
			planned[i].Kind = configured[i].Kind
			planned[i].Subnet = configured[i].Subnet
		}
	}
}

// planElasticIPInputs plans every input as configured, for the reason given on
// planPrivateIPInputs.
func planElasticIPInputs(planned, configured []ElasticIPInputModel) {
	for i := range planned {
		if i < len(configured) {
			planned[i].Kind = configured[i].Kind
			planned[i].EnableIPv4 = configured[i].EnableIPv4
			planned[i].EnableIPv6 = configured[i].EnableIPv6
		}
	}
}

func preservePrivateIPComputedValues(planned, configured, prior []PrivateIPInputModel) {
	used := make([]bool, len(prior))
	for i := range planned {
		matched := false
		for j := range prior {
			if used[j] || i >= len(configured) || !privateIPPlanIdentityEqual(configured[i], prior[j]) {
				continue
			}
			planned[i].ID = prior[j].ID
			planned[i].IPAddress = prior[j].IPAddress
			planned[i].MACAddress = prior[j].MACAddress
			if configured[i].DeleteOnTermination.IsNull() {
				planned[i].DeleteOnTermination = prior[j].DeleteOnTermination
			}
			used[j] = true
			matched = true
			break
		}
		if matched {
			continue
		}
		if i >= len(configured) || configured[i].ID.IsNull() || configured[i].ID.IsUnknown() {
			planned[i].ID = types.StringUnknown()
		}
		if i >= len(configured) || configured[i].DeleteOnTermination.IsNull() || configured[i].DeleteOnTermination.IsUnknown() {
			planned[i].DeleteOnTermination = types.BoolUnknown()
		}
		planned[i].IPAddress = types.StringUnknown()
		planned[i].MACAddress = types.StringUnknown()
	}
}

func preserveElasticIPComputedValues(planned, configured, prior []ElasticIPInputModel) {
	used := make([]bool, len(prior))
	matchedCount := 0
	for i := range planned {
		matched := false
		for j := range prior {
			if used[j] || i >= len(configured) || !elasticIPPlanIdentityEqual(configured[i], prior[j]) {
				continue
			}
			planned[i].ID = prior[j].ID
			planned[i].IPAddress = prior[j].IPAddress
			planned[i].IPv6Address = prior[j].IPv6Address
			planned[i].Status = prior[j].Status
			if configured[i].DeleteOnTermination.IsNull() {
				planned[i].DeleteOnTermination = prior[j].DeleteOnTermination
			}
			used[j] = true
			matched = true
			matchedCount++
			break
		}
		if matched {
			continue
		}
		if i >= len(configured) || configured[i].ID.IsNull() || configured[i].ID.IsUnknown() {
			planned[i].ID = types.StringUnknown()
		}
		if i >= len(configured) || configured[i].DeleteOnTermination.IsNull() || configured[i].DeleteOnTermination.IsUnknown() {
			planned[i].DeleteOnTermination = types.BoolUnknown()
		}
		planned[i].IPAddress = types.StringUnknown()
		planned[i].IPv6Address = types.StringUnknown()
		planned[i].Status = types.StringUnknown()
	}
	if matchedCount != len(planned) || len(planned) != len(prior) {
		for i := range planned {
			planned[i].Status = types.StringUnknown()
		}
	}
}

func privateIPPlanIdentityEqual(left, right PrivateIPInputModel) bool {
	if normalizedString(left.Kind) != normalizedString(right.Kind) {
		return false
	}
	if normalizedString(left.Kind) == "subnet" {
		return referenceCriterion(left.Subnet, reference.ID) == referenceCriterion(right.Subnet, reference.ID) &&
			referenceCriterion(left.Subnet, subnetCIDR) == referenceCriterion(right.Subnet, subnetCIDR)
	}
	return normalizedString(left.ID) != "" && normalizedString(left.ID) == normalizedString(right.ID)
}

func elasticIPPlanIdentityEqual(left, right ElasticIPInputModel) bool {
	if normalizedString(left.Kind) != normalizedString(right.Kind) {
		return false
	}
	if normalizedString(left.Kind) == "new" {
		if left.EnableIPv4.IsUnknown() || left.EnableIPv6.IsUnknown() {
			return false
		}
		return boolValueOrDefault(left.EnableIPv4, true) == boolValueOrDefault(right.EnableIPv4, true) &&
			boolValueOrDefault(left.EnableIPv6, false) == boolValueOrDefault(right.EnableIPv6, false)
	}
	return normalizedString(left.ID) != "" && normalizedString(left.ID) == normalizedString(right.ID)
}

// --- Create request construction ---

// recordServerSelectorIDs carries resolved create identities into pending state.
func recordServerSelectorIDs(ctx context.Context, body serversdk.ServerCreateSchema, plan *ServerResourceModel) diag.Diagnostics {
	var diags diag.Diagnostics
	var boot BootInputModel
	diags.Append(plan.Boot.As(ctx, &boot, basetypes.ObjectAsOptions{})...)
	if diags.HasError() {
		return diags
	}
	value, err := body.Boot.ValueByDiscriminator()
	if err != nil {
		diags.AddError("Unable to read resolved server boot", err.Error())
		return diags
	}
	var imageID, volumeTypeID *core.UUID
	switch value := value.(type) {
	case serversdk.ServerBootFromImage:
		imageID, volumeTypeID = &value.ImageId, &value.VolumeTypeId
	case serversdk.ServerBootFromLocalDisk:
		imageID = &value.ImageId
	case serversdk.ServerBootFromCustomImage:
		volumeTypeID = &value.VolumeTypeId
	}
	if imageID != nil {
		boot.Image = selector.WithID(boot.Image, types.StringValue(imageID.String()))
	}
	if volumeTypeID != nil {
		boot.VolumeType = selector.WithID(boot.VolumeType, types.StringValue(volumeTypeID.String()))
	}
	var valueDiags diag.Diagnostics
	plan.Boot, valueDiags = types.ObjectValueFrom(ctx, bootResourceAttributeTypes(), boot)
	diags.Append(valueDiags...)
	if body.DataVolumes != nil {
		var volumes []DataVolumeInputModel
		diags.Append(plan.DataVolumes.ElementsAs(ctx, &volumes, false)...)
		if diags.HasError() {
			return diags
		}
		created := 0
		for i := range volumes {
			if dataVolumeKind(volumes[i]) != "new" {
				continue
			}
			if created >= len(*body.DataVolumes) {
				diags.AddError("Unable to read resolved data volumes", "The resolved volume count differs from the plan.")
				return diags
			}
			volumes[i].VolumeType = selector.WithID(volumes[i].VolumeType, types.StringValue((*body.DataVolumes)[created].VolumeTypeId.String()))
			created++
		}
		plan.DataVolumes, valueDiags = types.ListValueFrom(ctx, types.ObjectType{AttrTypes: dataVolumeResourceAttributeTypes()}, volumes)
		diags.Append(valueDiags...)
	}
	return diags
}

func dataVolumeKind(config DataVolumeInputModel) string {
	if config.Kind.IsNull() {
		return "new"
	}
	return normalizedString(config.Kind)
}

// matchDataVolumeConfigs requires a unique identity before any volume mutation.
// An unchanged list keeps its IDs, including otherwise identical new volumes.
func matchDataVolumeConfigs(planned, prior []DataVolumeInputModel) ([]int, []bool, diag.Diagnostics) {
	var diags diag.Diagnostics
	matches := make([]int, len(planned))
	used := make([]bool, len(prior))
	for i := range matches {
		matches[i] = -1
	}
	bind := func(i, j int) {
		matches[i] = j
		used[j] = true
		if planned[i].ID.IsNull() || planned[i].ID.IsUnknown() {
			planned[i].ID = prior[j].ID
		}
	}
	// Explicit IDs reserve their volumes before content matching can use them.
	for i, config := range planned {
		if config.ID.IsNull() || config.ID.IsUnknown() {
			continue
		}
		id, err := core.ParseUUID(config.ID.ValueString())
		if err != nil {
			diags.AddAttributeError(path.Root("data_volumes").AtListIndex(i).AtName("id"), "Invalid data volume ID", err.Error())
			continue
		}
		for j := range prior {
			oldID, oldErr := core.ParseUUID(prior[j].ID.ValueString())
			if oldErr == nil && id == oldID {
				if used[j] {
					diags.AddError("Duplicate data volume ID", fmt.Sprintf("data_volumes[%d] refers to a volume that another entry already uses.", i))
				} else {
					bind(i, j)
				}
				break
			}
		}
		if matches[i] < 0 && dataVolumeKind(config) == "new" {
			diags.AddAttributeError(path.Root("data_volumes").AtListIndex(i).AtName("id"), "Unknown server-owned data volume", "For kind new, id must identify a prior data volume of this server. To attach another volume, use kind existing.")
		}
	}
	if diags.HasError() {
		return nil, nil, diags
	}
	unchanged := len(planned) == len(prior)
	for i := range planned {
		if !unchanged {
			break
		}
		if matches[i] >= 0 {
			unchanged = matches[i] == i
		} else {
			unchanged = dataVolumeKind(planned[i]) == "new" && dataVolumeKind(prior[i]) == "new" && dataVolumeContentEqual(planned[i], prior[i], true)
		}
	}
	if unchanged {
		for i := range planned {
			bind(i, i)
		}
		return matches, used, diags
	}
	// Exact matches reserve unchanged volumes before size or property updates.
	for pass := range 3 {
		for i := range planned {
			if matches[i] >= 0 || dataVolumeKind(planned[i]) != "new" {
				continue
			}
			var candidates []int
			for j := range prior {
				if used[j] || dataVolumeKind(prior[j]) != "new" {
					continue
				}
				if pass == 2 || dataVolumeContentEqual(planned[i], prior[j], pass == 0) {
					candidates = append(candidates, j)
				}
			}
			if len(candidates) > 1 {
				diags.AddAttributeError(path.Root("data_volumes").AtListIndex(i), "Ambiguous data volume identity", fmt.Sprintf("data_volumes[%d] matches %d prior volumes. Set id to the intended volume ID from state before changing or removing attachments.", i, len(candidates)))
				return nil, nil, diags
			}
			if len(candidates) == 1 {
				bind(i, candidates[0])
			}
		}
	}
	return matches, used, diags
}

func dataVolumeContentEqual(config, prior DataVolumeInputModel, exactSize bool) bool {
	return !config.VolumeType.IsNull() && !config.VolumeType.IsUnknown() &&
		selector.Equal(config.VolumeType, prior.VolumeType) &&
		(config.IOPS.IsNull() || config.IOPS.IsUnknown() || config.IOPS.Equal(prior.IOPS)) &&
		(!exactSize || config.VolumeSize.Equal(prior.VolumeSize))
}

func validateServerDataVolumes(ctx context.Context, value types.List, diags *diag.Diagnostics) {
	configs, ok := decodeConfigList[DataVolumeInputModel](ctx, value, diags)
	if !ok {
		return
	}
	seen := make(map[string]int)
	for i, config := range configs {
		element := path.Root("data_volumes").AtListIndex(i)
		switch dataVolumeKind(config) {
		case "new":
			validateServerUUID(config.ID, element.AtName("id"), fmt.Sprintf("data_volumes[%d].id", i), diags)
			requireConfiguredSelector(config.VolumeType, element.AtName("volume_type"), fmt.Sprintf("data_volumes[%d].volume_type", i), "kind is new", diags)
			requireConfiguredInt64(config.VolumeSize, element.AtName("volume_size"), fmt.Sprintf("data_volumes[%d].volume_size", i), "kind is new", diags)
			requirePositiveInt64(config.VolumeSize, element.AtName("volume_size"), fmt.Sprintf("data_volumes[%d].volume_size", i), diags)
			requirePositiveInt64(config.IOPS, element.AtName("iops"), fmt.Sprintf("data_volumes[%d].iops", i), diags)
		case "existing":
			requireConfigured(config.ID, element.AtName("id"), fmt.Sprintf("data_volumes[%d].id", i), "kind is existing", diags)
			validateServerUUID(config.ID, element.AtName("id"), fmt.Sprintf("data_volumes[%d].id", i), diags)
			rejectConfigured(config.VolumeType, element.AtName("volume_type"), fmt.Sprintf("data_volumes[%d].volume_type", i), "kind is existing", diags)
			rejectConfiguredInt64(config.VolumeSize, element.AtName("volume_size"), fmt.Sprintf("data_volumes[%d].volume_size", i), "kind is existing", diags)
			rejectConfiguredInt64(config.IOPS, element.AtName("iops"), fmt.Sprintf("data_volumes[%d].iops", i), "kind is existing", diags)
		case "":
			if !config.Kind.IsUnknown() {
				diags.AddAttributeError(element.AtName("kind"), "Invalid data volume kind", "kind must be new or existing.")
			}
		default:
			diags.AddAttributeError(element.AtName("kind"), "Invalid data volume kind", "kind must be new or existing.")
		}
		duplicateID := config.ID
		if id, err := core.ParseUUID(config.ID.ValueString()); err == nil {
			duplicateID = types.StringValue(id.String())
		}
		rejectDuplicateID(duplicateID, element.AtName("id"), "data_volumes", i, seen, diags)
	}
}

func buildServerCreateBody(
	ctx context.Context,
	plan ServerResourceModel,
	resolveZone zoneResolveFunc,
	resolveFlavor serverlookup.FlavorResolveFunc,
	resolveImage serverlookup.ImageResolveFunc,
	resolveVolumeType blockstoragelookup.VolumeTypeResolveFunc,
	resolveSubnet subnetResolveFunc,
) (serversdk.ServerCreateSchema, diag.Diagnostics) {
	var body serversdk.ServerCreateSchema
	var diags diag.Diagnostics
	if plan.Name.IsNull() || plan.Name.IsUnknown() || strings.TrimSpace(plan.Name.ValueString()) == "" {
		diags.AddError("Missing server name", "name must be configured and known.")
	} else {
		body.Name = strings.TrimSpace(plan.Name.ValueString())
	}
	if !plan.Description.IsNull() && !plan.Description.IsUnknown() {
		description := plan.Description.ValueString()
		body.Description = &description
	}
	if !plan.UserData.IsNull() && !plan.UserData.IsUnknown() {
		userData := plan.UserData.ValueString()
		body.UserData = &userData
	}
	if !plan.Bandwidth.IsNull() && !plan.Bandwidth.IsUnknown() {
		bandwidth := int(plan.Bandwidth.ValueInt64())
		body.Bandwidth = &bandwidth
	}
	quantity := 1
	if !plan.Quantity.IsNull() && !plan.Quantity.IsUnknown() {
		quantity = int(plan.Quantity.ValueInt64())
	}
	if quantity != 1 {
		diags.AddError("Unsupported server quantity", "quantity must be 1 because each Terraform resource manages one server.")
	}
	body.Quantity = &quantity

	if zoneName := reference.Get(plan.Zone, reference.Name); !zoneName.IsNull() && !zoneName.IsUnknown() {
		zone, zoneDiags := resolveConfiguredName(ctx, zoneName, "zone.name", "zone", resolveZone)
		diags.Append(zoneDiags...)
		if !zoneDiags.HasError() {
			body.ZoneId = &zone.Id
		}
	}
	if keyPair := reference.Get(plan.KeyPair, reference.ID); !keyPair.IsNull() && !keyPair.IsUnknown() {
		keyPairID, idDiags := parse.UUIDString(keyPair, "key_pair.id")
		diags.Append(idDiags...)
		if !idDiags.HasError() {
			body.KeyPairId = &keyPairID
		}
	}
	if placementGroup := reference.Get(plan.PlacementGroup, reference.ID); !placementGroup.IsNull() && !placementGroup.IsUnknown() {
		placementGroupID, idDiags := parse.UUIDString(placementGroup, "placement_group.id")
		diags.Append(idDiags...)
		if !idDiags.HasError() {
			body.PlacementGroupId = &placementGroupID
		}
	}
	body.Flavor = buildFlavor(ctx, plan.Flavor, body.ZoneId, resolveFlavor, &diags)

	var boot BootInputModel
	if plan.Boot.IsNull() || plan.Boot.IsUnknown() {
		diags.AddError("Missing boot configuration", "boot must be configured and known.")
	} else {
		bootDiags := plan.Boot.As(ctx, &boot, basetypes.ObjectAsOptions{})
		diags.Append(bootDiags...)
		if !bootDiags.HasError() {
			bootBody, valueDiags := buildBoot(ctx, boot, body.ZoneId, resolveImage, resolveVolumeType)
			diags.Append(valueDiags...)
			body.Boot = bootBody
		}
	}

	buildPrivateIPs(ctx, plan.PrivateIPs, &body, resolveSubnet, &diags)
	buildDataVolumes(ctx, plan.DataVolumes, &body, body.ZoneId, resolveVolumeType, &diags)
	buildElasticIPs(ctx, plan.ElasticIPs, &body, &diags)
	buildSecurityGroups(plan.SecurityGroups, &body, &diags)
	return body, diags
}

func buildFlavor(
	ctx context.Context,
	value types.Object,
	zoneID *core.UUID,
	resolve serverlookup.FlavorResolveFunc,
	diags *diag.Diagnostics,
) serversdk.ServerFlavor {
	var result serversdk.ServerFlavor
	if value.IsNull() || value.IsUnknown() {
		diags.AddError("Missing flavor", "flavor must be configured and known.")
		return result
	}
	var flavor FlavorInputModel
	valueDiags := value.As(ctx, &flavor, basetypes.ObjectAsOptions{})
	diags.Append(valueDiags...)
	if valueDiags.HasError() || flavor.Kind.IsNull() || flavor.Kind.IsUnknown() {
		if flavor.Kind.IsNull() || flavor.Kind.IsUnknown() {
			diags.AddError("Missing flavor kind", "flavor.kind must be configured and known.")
		}
		return result
	}

	switch strings.TrimSpace(flavor.Kind.ValueString()) {
	case "predefined":
		resolved, resolveDiags := resolveConfiguredName(
			ctx,
			flavor.Name,
			"flavor.name",
			"predefined flavor",
			func(ctx context.Context, name string) (serversdk.FlavorSchema, error) {
				return resolve(ctx, serverlookup.FlavorFilter{Name: &name, ZoneID: zoneID})
			},
		)
		diags.Append(resolveDiags...)
		if !resolveDiags.HasError() {
			appendUnionDiagnostic(diags, result.FromPredefinedServerFlavor(serversdk.PredefinedServerFlavor{Id: resolved.Id}), "flavor")
		}
	case "custom":
		if flavor.Family.IsNull() || flavor.Family.IsUnknown() || flavor.VCPUs.IsNull() || flavor.VCPUs.IsUnknown() ||
			flavor.RAM.IsNull() || flavor.RAM.IsUnknown() {
			diags.AddError("Incomplete custom flavor", "flavor.family, flavor.vcpus, and flavor.ram must be configured and known.")
			return result
		}
		if flavor.VCPUs.ValueInt64() < 1 || flavor.RAM.ValueInt64() < 1 {
			diags.AddError("Invalid custom flavor", "flavor.vcpus and flavor.ram must be greater than zero.")
			return result
		}
		appendUnionDiagnostic(diags, result.FromCustomServerFlavor(serversdk.CustomServerFlavor{
			Family: serversdk.FlavorFamily(strings.TrimSpace(flavor.Family.ValueString())),
			Vcpus:  int(flavor.VCPUs.ValueInt64()),
			Ram:    int(flavor.RAM.ValueInt64()),
		}), "flavor")
	default:
		diags.AddError("Invalid flavor kind", `flavor.kind must be "predefined" or "custom".`)
	}
	return result
}

func buildBoot(
	ctx context.Context,
	boot BootInputModel,
	zoneID *core.UUID,
	resolveImage serverlookup.ImageResolveFunc,
	resolveVolumeType blockstoragelookup.VolumeTypeResolveFunc,
) (serversdk.ServerBootOptions, diag.Diagnostics) {
	var result serversdk.ServerBootOptions
	var diags diag.Diagnostics
	if boot.BootType.IsNull() || boot.BootType.IsUnknown() {
		diags.AddError("Missing boot type", "boot.boot_type must be configured and known.")
		return result, diags
	}

	switch strings.TrimSpace(boot.BootType.ValueString()) {
	case "image":
		volumeType, ok := resolveBootVolumeType(ctx, boot, zoneID, resolveVolumeType, &diags)
		if !ok {
			return result, diags
		}
		image, imageDiags := serverselector.ResolveImage(ctx, boot.Image, "boot.image", resolveImage)
		diags.Append(imageDiags...)
		if imageDiags.HasError() {
			return result, diags
		}
		value := serversdk.ServerBootFromImage{
			ImageId: image.Id, VolumeTypeId: volumeType.Id, VolumeSize: int(boot.VolumeSize.ValueInt64()),
		}
		if !boot.IOPS.IsNull() && !boot.IOPS.IsUnknown() {
			iops := int(boot.IOPS.ValueInt64())
			value.Iops = &iops
		}
		appendUnionDiagnostic(&diags, result.FromServerBootFromImage(value), "boot")
	case "custom_image":
		customImageID, idDiags := parse.UUIDString(reference.Get(boot.CustomImage, reference.ID), "boot.custom_image.id")
		diags.Append(idDiags...)
		volumeType, ok := resolveBootVolumeType(ctx, boot, zoneID, resolveVolumeType, &diags)
		if idDiags.HasError() || !ok {
			return result, diags
		}
		value := serversdk.ServerBootFromCustomImage{
			CustomImageId: customImageID, VolumeTypeId: volumeType.Id, VolumeSize: int(boot.VolumeSize.ValueInt64()),
		}
		if !boot.IOPS.IsNull() && !boot.IOPS.IsUnknown() {
			iops := int(boot.IOPS.ValueInt64())
			value.Iops = &iops
		}
		appendUnionDiagnostic(&diags, result.FromServerBootFromCustomImage(value), "boot")
	case "local_disk":
		image, imageDiags := serverselector.ResolveImage(ctx, boot.Image, "boot.image", resolveImage)
		diags.Append(imageDiags...)
		if !imageDiags.HasError() {
			appendUnionDiagnostic(&diags, result.FromServerBootFromLocalDisk(serversdk.ServerBootFromLocalDisk{ImageId: image.Id}), "boot")
		}
	case "volume":
		volumeID, idDiags := parse.UUIDString(reference.Get(boot.Volume, reference.ID), "boot.volume.id")
		diags.Append(idDiags...)
		if !idDiags.HasError() {
			appendUnionDiagnostic(&diags, result.FromServerBootFromVolume(serversdk.ServerBootFromVolume{VolumeId: volumeID}), "boot")
		}
	default:
		diags.AddError("Invalid boot type", "boot.boot_type must be image, custom_image, local_disk, or volume.")
	}
	return result, diags
}

func resolveBootVolumeType(
	ctx context.Context,
	boot BootInputModel,
	zoneID *core.UUID,
	resolve blockstoragelookup.VolumeTypeResolveFunc,
	diags *diag.Diagnostics,
) (blockstoragesdk.VolumeTypeSchema, bool) {
	if boot.VolumeSize.IsNull() || boot.VolumeSize.IsUnknown() || boot.VolumeSize.ValueInt64() < 1 {
		diags.AddError("Invalid boot volume size", "boot.volume_size must be known and greater than zero.")
		return blockstoragesdk.VolumeTypeSchema{}, false
	}
	volumeType, valueDiags := blockstorageselector.ResolveVolumeType(ctx, boot.VolumeType, "boot.volume_type", zoneID, int(boot.VolumeSize.ValueInt64()), resolve)
	diags.Append(valueDiags...)
	return volumeType, !valueDiags.HasError()
}

func buildPrivateIPs(
	ctx context.Context,
	value types.List,
	body *serversdk.ServerCreateSchema,
	resolveSubnet subnetResolveFunc,
	diags *diag.Diagnostics,
) {
	if value.IsNull() || value.IsUnknown() {
		return
	}
	var configs []PrivateIPInputModel
	valueDiags := value.ElementsAs(ctx, &configs, false)
	diags.Append(valueDiags...)
	if valueDiags.HasError() || len(configs) == 0 {
		return
	}
	items := make([]serversdk.ServerPrivateIP, 0, len(configs))
	for i, config := range configs {
		if config.Kind.IsNull() || config.Kind.IsUnknown() {
			diags.AddError("Missing private IP kind", fmt.Sprintf("private_ips[%d].kind must be configured and known.", i))
			continue
		}
		var item serversdk.ServerPrivateIP
		switch strings.TrimSpace(config.Kind.ValueString()) {
		case "subnet":
			if normalizedString(config.ID) != "" {
				diags.AddError("Invalid subnet private IP", fmt.Sprintf("private_ips[%d].id must not be configured when kind is subnet.", i))
				continue
			}
			subnetID, subnetDiags := privateIPSubnetID(ctx, config, i, resolveSubnet)
			diags.Append(subnetDiags...)
			if subnetDiags.HasError() {
				continue
			}
			if err := item.FromNewServerPrivateIP(serversdk.NewServerPrivateIP{SubnetId: subnetID}); err != nil {
				appendUnionDiagnostic(diags, err, fmt.Sprintf("private_ips[%d]", i))
				continue
			}
		case "ip":
			if referenceCriterion(config.Subnet, reference.ID) != "" || referenceCriterion(config.Subnet, subnetCIDR) != "" {
				diags.AddError("Invalid private IP reference", fmt.Sprintf("private_ips[%d] must not configure subnet when kind is ip.", i))
				continue
			}
			id, idDiags := parse.UUIDString(config.ID, fmt.Sprintf("private_ips[%d].id", i))
			diags.Append(idDiags...)
			if idDiags.HasError() {
				continue
			}
			if err := item.FromExistingServerPrivateIP(serversdk.ExistingServerPrivateIP{Id: id}); err != nil {
				appendUnionDiagnostic(diags, err, fmt.Sprintf("private_ips[%d]", i))
				continue
			}
		default:
			diags.AddError("Invalid private IP kind", fmt.Sprintf("private_ips[%d].kind must be subnet or ip.", i))
			continue
		}
		items = append(items, item)
	}
	body.PrivateIps = &items
}

func buildDataVolumes(
	ctx context.Context,
	value types.List,
	body *serversdk.ServerCreateSchema,
	zoneID *core.UUID,
	resolve blockstoragelookup.VolumeTypeResolveFunc,
	diags *diag.Diagnostics,
) {
	if value.IsNull() || value.IsUnknown() {
		return
	}
	var configs []DataVolumeInputModel
	valueDiags := value.ElementsAs(ctx, &configs, false)
	diags.Append(valueDiags...)
	if valueDiags.HasError() || len(configs) == 0 {
		return
	}
	items := make([]serversdk.ServerDataVolume, 0, len(configs))
	for i, config := range configs {
		if dataVolumeKind(config) == "existing" {
			if _, errors := parse.UUIDString(config.ID, fmt.Sprintf("data_volumes[%d].id", i)); errors.HasError() {
				diags.Append(errors...)
			}
			continue
		}
		if dataVolumeKind(config) != "new" {
			diags.AddError("Invalid data volume kind", "data_volumes.kind must be new or existing.")
			continue
		}
		if !config.ID.IsNull() && !config.ID.IsUnknown() {
			diags.AddError("Invalid new data volume ID on create", fmt.Sprintf("Omit data_volumes[%d].id when creating a server with kind new. To attach an existing volume, use kind existing.", i))
			continue
		}
		if config.VolumeSize.IsNull() || config.VolumeSize.IsUnknown() || config.VolumeSize.ValueInt64() < 1 {
			diags.AddError("Invalid data volume size", fmt.Sprintf("data_volumes[%d].volume_size must be known and greater than zero.", i))
			continue
		}
		volumeType, typeDiags := blockstorageselector.ResolveVolumeType(ctx, config.VolumeType, fmt.Sprintf("data_volumes[%d].volume_type", i), zoneID, int(config.VolumeSize.ValueInt64()), resolve)
		diags.Append(typeDiags...)
		if typeDiags.HasError() {
			continue
		}
		item := serversdk.ServerDataVolume{VolumeTypeId: volumeType.Id, VolumeSize: int(config.VolumeSize.ValueInt64())}
		if !config.IOPS.IsNull() && !config.IOPS.IsUnknown() {
			iops := int(config.IOPS.ValueInt64())
			item.Iops = &iops
		}
		items = append(items, item)
	}
	body.DataVolumes = &items
}

func buildElasticIPs(ctx context.Context, value types.List, body *serversdk.ServerCreateSchema, diags *diag.Diagnostics) {
	if value.IsNull() || value.IsUnknown() {
		return
	}
	var configs []ElasticIPInputModel
	valueDiags := value.ElementsAs(ctx, &configs, false)
	diags.Append(valueDiags...)
	if valueDiags.HasError() || len(configs) == 0 {
		return
	}
	items := make([]serversdk.ServerElasticIP, 0, len(configs))
	for i, config := range configs {
		if config.Kind.IsNull() || config.Kind.IsUnknown() {
			diags.AddError("Missing elastic IP kind", fmt.Sprintf("elastic_ips[%d].kind must be configured and known.", i))
			continue
		}
		var item serversdk.ServerElasticIP
		switch strings.TrimSpace(config.Kind.ValueString()) {
		case "existing":
			id, idDiags := parse.UUIDString(config.ID, fmt.Sprintf("elastic_ips[%d].id", i))
			diags.Append(idDiags...)
			if idDiags.HasError() {
				continue
			}
			if err := item.FromExistingServerElasticIP(serversdk.ExistingServerElasticIP{Id: id}); err != nil {
				appendUnionDiagnostic(diags, err, fmt.Sprintf("elastic_ips[%d]", i))
				continue
			}
		case "new":
			if normalizedString(config.ID) != "" {
				diags.AddError("Invalid new elastic IP", fmt.Sprintf("elastic_ips[%d].id must not be configured when kind is new.", i))
				continue
			}
			if config.EnableIPv4.IsUnknown() || config.EnableIPv6.IsUnknown() {
				diags.AddError("Unknown new elastic IP address family", fmt.Sprintf("elastic_ips[%d].enable_ipv4 and enable_ipv6 must be known when configured.", i))
				continue
			}
			enableIPv4 := config.EnableIPv4.IsNull() || config.EnableIPv4.IsUnknown() || config.EnableIPv4.ValueBool()
			enableIPv6 := !config.EnableIPv6.IsNull() && !config.EnableIPv6.IsUnknown() && config.EnableIPv6.ValueBool()
			if err := item.FromNewServerElasticIP(serversdk.NewServerElasticIP{EnableIpv4: enableIPv4, EnableIpv6: enableIPv6}); err != nil {
				appendUnionDiagnostic(diags, err, fmt.Sprintf("elastic_ips[%d]", i))
				continue
			}
		default:
			diags.AddError("Invalid elastic IP kind", fmt.Sprintf("elastic_ips[%d].kind must be existing or new.", i))
			continue
		}
		items = append(items, item)
	}
	body.ElasticIps = &items
}

func buildSecurityGroups(value types.Set, body *serversdk.ServerCreateSchema, diags *diag.Diagnostics) {
	if value.IsNull() || value.IsUnknown() {
		return
	}
	securityGroups := securityGroupReferences(value, diags)
	body.SecurityGroups = &securityGroups
}

// securityGroupReferences parses the ID of every configured security group.
// The caller must check that the set is known.
func securityGroupReferences(value types.Set, diags *diag.Diagnostics) []serversdk.ServerSecurityGroup {
	configuredIDs := reference.Strings(value, reference.ID)
	securityGroups := make([]serversdk.ServerSecurityGroup, 0, len(configuredIDs))
	for i, configuredID := range configuredIDs {
		id, idDiags := parse.UUIDString(configuredID, fmt.Sprintf("security_groups[%d].id", i))
		diags.Append(idDiags...)
		if !idDiags.HasError() {
			securityGroups = append(securityGroups, serversdk.ServerSecurityGroup{Id: id})
		}
	}
	return securityGroups
}

func resolveConfiguredName[T any](
	ctx context.Context,
	value types.String,
	attributePath string,
	resourceName string,
	resolve func(context.Context, string) (T, error),
) (T, diag.Diagnostics) {
	var zero T
	var diags diag.Diagnostics
	name := normalizedString(value)
	if name == "" {
		diags.AddError("Missing "+resourceName, attributePath+" must be configured and known.")
		return zero, diags
	}
	resolved, err := resolve(ctx, name)
	if err != nil {
		diags.AddError("Unable to resolve "+resourceName, fmt.Sprintf("%s: %s", attributePath, err))
		return zero, diags
	}
	return resolved, diags
}

func appendUnionDiagnostic(diags *diag.Diagnostics, err error, field string) {
	if err != nil {
		diags.AddError("Invalid "+field+" configuration", err.Error())
	}
}

// --- Configuration validation helpers ---

func validateServerQuantity(value types.Int64, diags *diag.Diagnostics) {
	if !value.IsNull() && !value.IsUnknown() && value.ValueInt64() != 1 {
		diags.AddAttributeError(
			path.Root("quantity"),
			"Unsupported server quantity",
			"quantity must be 1 because each Terraform resource manages one server. Use count or for_each to manage several servers.",
		)
	}
}

func validateServerPowerState(value types.String, diags *diag.Diagnostics) {
	if value.IsNull() || value.IsUnknown() {
		return
	}
	desired := normalizedString(value)
	if desired != string(serversdk.ServerPowerStateRunning) && desired != string(serversdk.ServerPowerStateShutdown) {
		diags.AddAttributeError(
			path.Root("power_state"),
			"Invalid server power state",
			`power_state must be "running" or "shutdown".`,
		)
	}
}

func validateServerFlavor(ctx context.Context, value types.Object, diags *diag.Diagnostics) {
	flavor, ok := decodeConfigObject[FlavorInputModel](ctx, value, diags)
	if !ok || flavor.Kind.IsNull() || flavor.Kind.IsUnknown() {
		return
	}
	root := path.Root("flavor")
	switch normalizedString(flavor.Kind) {
	case "predefined":
		requireConfigured(flavor.Name, root.AtName("name"), "flavor.name", `flavor.kind is "predefined"`, diags)
		rejectConfigured(flavor.Family, root.AtName("family"), "flavor.family", `flavor.kind is "predefined"`, diags)
		rejectConfiguredInt64(flavor.VCPUs, root.AtName("vcpus"), "flavor.vcpus", `flavor.kind is "predefined"`, diags)
		rejectConfiguredInt64(flavor.RAM, root.AtName("ram"), "flavor.ram", `flavor.kind is "predefined"`, diags)
	case "custom":
		requireConfigured(flavor.Family, root.AtName("family"), "flavor.family", `flavor.kind is "custom"`, diags)
		requireConfiguredInt64(flavor.VCPUs, root.AtName("vcpus"), "flavor.vcpus", `flavor.kind is "custom"`, diags)
		requireConfiguredInt64(flavor.RAM, root.AtName("ram"), "flavor.ram", `flavor.kind is "custom"`, diags)
		rejectConfigured(flavor.Name, root.AtName("name"), "flavor.name", `flavor.kind is "custom"`, diags)
		requirePositiveInt64(flavor.VCPUs, root.AtName("vcpus"), "flavor.vcpus", diags)
		requirePositiveInt64(flavor.RAM, root.AtName("ram"), "flavor.ram", diags)
		if !flavor.Family.IsNull() && !flavor.Family.IsUnknown() &&
			!serversdk.FlavorFamily(normalizedString(flavor.Family)).Valid() {
			diags.AddAttributeError(
				root.AtName("family"),
				"Invalid custom flavor family",
				"flavor.family must be basic, premium, enterprise, gpu, or spot.",
			)
		}
	default:
		diags.AddAttributeError(
			root.AtName("kind"),
			"Invalid flavor kind",
			`flavor.kind must be "predefined" or "custom".`,
		)
	}
}

func validateServerBoot(ctx context.Context, value types.Object, diags *diag.Diagnostics) {
	boot, ok := decodeConfigObject[BootInputModel](ctx, value, diags)
	if !ok || boot.BootType.IsNull() || boot.BootType.IsUnknown() {
		return
	}
	root := path.Root("boot")
	bootType := normalizedString(boot.BootType)
	because := fmt.Sprintf("boot.boot_type is %q", bootType)
	switch bootType {
	case "image", "custom_image":
		if bootType == "image" {
			requireConfiguredSelector(boot.Image, root.AtName("image"), "boot.image", because, diags)
			rejectConfigured(boot.CustomImage, root.AtName("custom_image"), "boot.custom_image", because, diags)
		} else {
			requireReference(boot.CustomImage, reference.ID, root.AtName("custom_image"), "boot.custom_image", because, diags)
			rejectConfigured(boot.Image, root.AtName("image"), "boot.image", because, diags)
		}
		requireConfiguredSelector(boot.VolumeType, root.AtName("volume_type"), "boot.volume_type", because, diags)
		requireConfiguredInt64(boot.VolumeSize, root.AtName("volume_size"), "boot.volume_size", because, diags)
		requirePositiveInt64(boot.VolumeSize, root.AtName("volume_size"), "boot.volume_size", diags)
		requirePositiveInt64(boot.IOPS, root.AtName("iops"), "boot.iops", diags)
		rejectConfigured(boot.Volume, root.AtName("volume"), "boot.volume", because, diags)
	case "local_disk":
		requireConfiguredSelector(boot.Image, root.AtName("image"), "boot.image", because, diags)
		rejectConfigured(boot.CustomImage, root.AtName("custom_image"), "boot.custom_image", because, diags)
		rejectConfigured(boot.Volume, root.AtName("volume"), "boot.volume", because, diags)
		rejectConfigured(boot.VolumeType, root.AtName("volume_type"), "boot.volume_type", because, diags)
		rejectConfiguredInt64(boot.VolumeSize, root.AtName("volume_size"), "boot.volume_size", because, diags)
		rejectConfiguredInt64(boot.IOPS, root.AtName("iops"), "boot.iops", because, diags)
		rejectConfiguredBool(
			boot.DeleteOnTermination,
			root.AtName("delete_on_termination"),
			"boot.delete_on_termination",
			because,
			diags,
		)
	case "volume":
		requireReference(boot.Volume, reference.ID, root.AtName("volume"), "boot.volume", because, diags)
		rejectConfigured(boot.Image, root.AtName("image"), "boot.image", because, diags)
		rejectConfigured(boot.CustomImage, root.AtName("custom_image"), "boot.custom_image", because, diags)
		rejectConfigured(boot.VolumeType, root.AtName("volume_type"), "boot.volume_type", because, diags)
		rejectConfiguredInt64(boot.VolumeSize, root.AtName("volume_size"), "boot.volume_size", because, diags)
		rejectConfiguredInt64(boot.IOPS, root.AtName("iops"), "boot.iops", because, diags)
	default:
		diags.AddAttributeError(
			root.AtName("boot_type"),
			"Invalid boot type",
			"boot.boot_type must be image, custom_image, local_disk, or volume.",
		)
	}
}

func validateServerPrivateIPs(ctx context.Context, value types.List, diags *diag.Diagnostics) {
	configs, ok := decodeConfigList[PrivateIPInputModel](ctx, value, diags)
	if !ok {
		return
	}
	seen := make(map[string]int, len(configs))
	for i, config := range configs {
		element := path.Root("private_ips").AtListIndex(i)
		if config.Kind.IsNull() {
			requireConfigured(config.Kind, element.AtName("kind"), fmt.Sprintf("private_ips[%d].kind", i), "every private IP attachment needs a kind", diags)
			continue
		}
		if config.Kind.IsUnknown() {
			continue
		}
		switch normalizedString(config.Kind) {
		case "subnet":
			rejectConfigured(config.ID, element.AtName("id"), fmt.Sprintf("private_ips[%d].id", i), `kind is "subnet"`, diags)
			validatePrivateIPSubnet(config.Subnet, element.AtName("subnet"), i, diags)
		case "ip":
			requireConfigured(config.ID, element.AtName("id"), fmt.Sprintf("private_ips[%d].id", i), `kind is "ip"`, diags)
			rejectConfigured(config.Subnet, element.AtName("subnet"), fmt.Sprintf("private_ips[%d].subnet", i), `kind is "ip"`, diags)
			rejectDuplicateID(config.ID, element.AtName("id"), "private_ips", i, seen, diags)
		default:
			diags.AddAttributeError(
				element.AtName("kind"),
				"Invalid private IP kind",
				fmt.Sprintf("private_ips[%d].kind must be subnet or ip.", i),
			)
		}
	}
}

// validatePrivateIPSubnet requires a kind subnet private IP to select its
// subnet by exactly one of id or cidr. An unknown subnet is left to apply.
func validatePrivateIPSubnet(value types.Object, attribute path.Path, index int, diags *diag.Diagnostics) {
	if value.IsUnknown() {
		return
	}
	if value.IsNull() {
		diags.AddAttributeError(
			attribute,
			fmt.Sprintf("Missing private_ips[%d].subnet", index),
			fmt.Sprintf(`private_ips[%d].subnet must be configured because kind is "subnet".`, index),
		)
		return
	}
	if reference.Get(value, reference.ID).IsNull() == reference.Get(value, subnetCIDR).IsNull() {
		diags.AddAttributeError(
			attribute,
			"Invalid subnet reference",
			fmt.Sprintf("private_ips[%d].subnet must configure exactly one of id or cidr.", index),
		)
	}
}

func validateServerElasticIPs(ctx context.Context, value types.List, diags *diag.Diagnostics) {
	configs, ok := decodeConfigList[ElasticIPInputModel](ctx, value, diags)
	if !ok {
		return
	}
	seen := make(map[string]int, len(configs))
	for i, config := range configs {
		element := path.Root("elastic_ips").AtListIndex(i)
		if config.Kind.IsNull() {
			requireConfigured(config.Kind, element.AtName("kind"), fmt.Sprintf("elastic_ips[%d].kind", i), "every elastic IP attachment needs a kind", diags)
			continue
		}
		if config.Kind.IsUnknown() {
			continue
		}
		switch normalizedString(config.Kind) {
		case "existing":
			requireConfigured(config.ID, element.AtName("id"), fmt.Sprintf("elastic_ips[%d].id", i), `kind is "existing"`, diags)
			rejectConfiguredBool(config.EnableIPv4, element.AtName("enable_ipv4"), fmt.Sprintf("elastic_ips[%d].enable_ipv4", i), `kind is "existing"`, diags)
			rejectConfiguredBool(config.EnableIPv6, element.AtName("enable_ipv6"), fmt.Sprintf("elastic_ips[%d].enable_ipv6", i), `kind is "existing"`, diags)
			rejectDuplicateID(config.ID, element.AtName("id"), "elastic_ips", i, seen, diags)
		case "new":
			rejectConfigured(config.ID, element.AtName("id"), fmt.Sprintf("elastic_ips[%d].id", i), `kind is "new"`, diags)
			if !boolValueOrDefault(config.EnableIPv4, true) && !boolValueOrDefault(config.EnableIPv6, false) &&
				!config.EnableIPv4.IsUnknown() && !config.EnableIPv6.IsUnknown() {
				diags.AddAttributeError(
					element,
					"Invalid new elastic IP",
					fmt.Sprintf("elastic_ips[%d] must enable IPv4, IPv6, or both.", i),
				)
			}
		default:
			diags.AddAttributeError(
				element.AtName("kind"),
				"Invalid elastic IP kind",
				fmt.Sprintf("elastic_ips[%d].kind must be existing or new.", i),
			)
		}
	}
}

func decodeConfigObject[T any](ctx context.Context, value types.Object, diags *diag.Diagnostics) (T, bool) {
	var decoded T
	if value.IsNull() || value.IsUnknown() {
		return decoded, false
	}
	valueDiags := value.As(ctx, &decoded, basetypes.ObjectAsOptions{})
	diags.Append(valueDiags...)
	return decoded, !valueDiags.HasError()
}

func decodeConfigList[T any](ctx context.Context, value types.List, diags *diag.Diagnostics) ([]T, bool) {
	if value.IsNull() || value.IsUnknown() {
		return nil, false
	}
	var decoded []T
	valueDiags := value.ElementsAs(ctx, &decoded, false)
	diags.Append(valueDiags...)
	return decoded, !valueDiags.HasError()
}

// requireConfigured treats an unknown value as configured because Terraform
// resolves it before apply.
func requireConfigured(value attr.Value, attribute path.Path, name, because string, diags *diag.Diagnostics) {
	text, isText := value.(types.String)
	if value.IsNull() || (isText && !value.IsUnknown() && strings.TrimSpace(text.ValueString()) == "") {
		diags.AddAttributeError(
			attribute,
			"Missing "+name,
			fmt.Sprintf("%s must be configured with a non-empty value because %s.", name, because),
		)
	}
}

// requireReference requires a reference with a non-empty criterion. An
// unknown reference or criterion counts as configured, as in
// requireConfigured.
func requireReference(value types.Object, criterion string, attribute path.Path, name, because string, diags *diag.Diagnostics) {
	if value.IsNull() {
		requireConfigured(types.StringNull(), attribute, name, because, diags)
		return
	}
	requireConfigured(reference.Get(value, criterion), attribute.AtName(criterion), name+"."+criterion, because, diags)
}

func rejectConfigured(value attr.Value, attribute path.Path, name, because string, diags *diag.Diagnostics) {
	if !value.IsNull() {
		diags.AddAttributeError(
			attribute,
			"Unsupported "+name,
			fmt.Sprintf("%s must not be configured because %s.", name, because),
		)
	}
}

func requireConfiguredInt64(value types.Int64, attribute path.Path, name, because string, diags *diag.Diagnostics) {
	if value.IsNull() {
		diags.AddAttributeError(
			attribute,
			"Missing "+name,
			fmt.Sprintf("%s must be configured because %s.", name, because),
		)
	}
}

func rejectConfiguredInt64(value types.Int64, attribute path.Path, name, because string, diags *diag.Diagnostics) {
	if !value.IsNull() {
		diags.AddAttributeError(
			attribute,
			"Unsupported "+name,
			fmt.Sprintf("%s must not be configured because %s.", name, because),
		)
	}
}

func rejectConfiguredBool(value types.Bool, attribute path.Path, name, because string, diags *diag.Diagnostics) {
	if !value.IsNull() {
		diags.AddAttributeError(
			attribute,
			"Unsupported "+name,
			fmt.Sprintf("%s must not be configured because %s.", name, because),
		)
	}
}

func requirePositiveInt64(value types.Int64, attribute path.Path, name string, diags *diag.Diagnostics) {
	if !value.IsNull() && !value.IsUnknown() && value.ValueInt64() < 1 {
		diags.AddAttributeError(
			attribute,
			"Invalid "+name,
			fmt.Sprintf("%s must be greater than zero, got %d.", name, value.ValueInt64()),
		)
	}
}

func rejectDuplicateID(value types.String, attribute path.Path, list string, index int, seen map[string]int, diags *diag.Diagnostics) {
	if value.IsNull() || value.IsUnknown() {
		return
	}
	id := normalizedString(value)
	if first, ok := seen[id]; ok {
		diags.AddAttributeError(
			attribute,
			"Duplicate "+list+" entry",
			fmt.Sprintf("%s[%d] and %s[%d] reference the same ID %s.", list, first, list, index, id),
		)
		return
	}
	seen[id] = index
}

func validateServerUUID(value types.String, attribute path.Path, name string, diags *diag.Diagnostics) {
	if value.IsNull() || value.IsUnknown() {
		return
	}
	if _, idDiags := parse.UUIDString(value, name); idDiags.HasError() {
		diags.AddAttributeError(
			attribute,
			"Invalid "+name,
			fmt.Sprintf("%s must be a valid UUID.", name),
		)
	}
}

func requireConfiguredSelector(value types.Object, attribute path.Path, name, because string, diags *diag.Diagnostics) {
	requireConfigured(value, attribute, name, because, diags)
	if !value.IsNull() {
		diags.Append(selector.Validate(value, name, false)...)
	}
}
