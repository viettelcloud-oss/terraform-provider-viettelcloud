package server

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/compare"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/parse"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/replace"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/viettelcloud-oss/sdks/go/core"
	serversdk "github.com/viettelcloud-oss/sdks/go/server"
)

var (
	_ resource.Resource                = &KeyPairResource{}
	_ resource.ResourceWithConfigure   = &KeyPairResource{}
	_ resource.ResourceWithImportState = &KeyPairResource{}
)

type KeyPairResource struct {
	client    *serversdk.Client
	projectID core.UUID
}

func NewKeyPairResource() resource.Resource {
	return &KeyPairResource{}
}

func (r *KeyPairResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_key_pair"
}

func (r *KeyPairResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manage a Viettel Cloud SSH key pair. A generated private key is returned only during creation and stored in Terraform state. Protect the state because marking private_key as sensitive does not encrypt it. The backend does not return the private key during refresh or import, so it cannot be recovered if state is lost.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Key pair ID (UUID).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Name of the key pair.",
			},
			"public_key": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Public key material to upload. On create, omit it to generate a new key pair. Changing a configured value replaces the key pair, but adding or removing leading or trailing whitespace does not; removing it from configuration preserves the existing public key.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
					replace.StringIfConfigured(compare.SameTrimmedString),
				},
			},
			"private_key": schema.StringAttribute{
				Computed:    true,
				Sensitive:   true,
				Description: "Private key returned only when the platform generates the pair during creation. It is stored in Terraform state. The backend does not return it during refresh or import, so it cannot be recovered if state is lost.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"fingerprint": schema.StringAttribute{
				Computed:    true,
				Description: "Fingerprint of the key pair.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"type": schema.StringAttribute{
				Computed:    true,
				Description: "Type of the key pair (e.g. rsa).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"created_at": schema.StringAttribute{
				Computed:    true,
				Description: "Timestamp when the key pair was created (RFC3339).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"updated_at": schema.StringAttribute{
				Computed:    true,
				Description: "Timestamp when the key pair was last updated (RFC3339).",
			},
		},
	}
}

func (r *KeyPairResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	data, ok := req.ProviderData.(*providerdata.Configured)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected provider data type",
			fmt.Sprintf("Expected *providerdata.Configured, got %T", req.ProviderData),
		)
		return
	}
	r.client = data.Server
	r.projectID = data.ProjectID
}

func (r *KeyPairResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan KeyPairResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	body, diags := buildKeyPairCreateBody(plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	keyPair, err := r.client.CreateKeyPair(ctx, serversdk.CreateKeyPairParams{
		ProjectID: r.projectID,
	}, body)
	if err != nil {
		resp.Diagnostics.AddError("Error creating key pair", err.Error())
		return
	}

	populateKeyPairCreatedResourceState(keyPair, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *KeyPairResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state KeyPairResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	keyPairID, idDiags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(idDiags...)
	if resp.Diagnostics.HasError() {
		return
	}

	keyPair, err := r.client.GetKeyPair(ctx, keyPairID, serversdk.GetKeyPairParams{
		ProjectID: r.projectID,
	})
	if err != nil {
		if errors.Is(err, serversdk.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading key pair", err.Error())
		return
	}

	populateKeyPairResourceState(keyPair, &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *KeyPairResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan KeyPairResourceModel
	var state KeyPairResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	keyPairID, idDiags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(idDiags...)
	if resp.Diagnostics.HasError() {
		return
	}

	body, diags := buildKeyPairUpdateBody(plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	keyPair, err := r.client.PartialUpdateKeyPair(ctx, keyPairID, serversdk.PartialUpdateKeyPairParams{
		ProjectID: r.projectID,
	}, body)
	if err != nil {
		resp.Diagnostics.AddError("Error updating key pair", err.Error())
		return
	}

	newState := plan
	newState.PrivateKey = state.PrivateKey
	populateKeyPairResourceState(keyPair, &newState)
	resp.Diagnostics.Append(resp.State.Set(ctx, &newState)...)
}

func (r *KeyPairResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state KeyPairResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	keyPairID, idDiags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(idDiags...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.DeleteKeyPair(ctx, keyPairID, serversdk.DeleteKeyPairParams{
		ProjectID: r.projectID,
	})
	if err != nil {
		if errors.Is(err, serversdk.ErrNotFound) {
			return
		}
		resp.Diagnostics.AddError("Error deleting key pair", err.Error())
		return
	}
}

func (r *KeyPairResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func buildKeyPairCreateBody(plan KeyPairResourceModel) (serversdk.KeyPairCreateSchema, diag.Diagnostics) {
	var diags diag.Diagnostics
	name := strings.TrimSpace(plan.Name.ValueString())
	if name == "" {
		diags.AddAttributeError(
			path.Root("name"),
			"Missing key pair name",
			"name must be configured and not contain only whitespace.",
		)
		return serversdk.KeyPairCreateSchema{}, diags
	}

	body := serversdk.KeyPairCreateSchema{
		Name: name,
	}
	if !plan.PublicKey.IsNull() && !plan.PublicKey.IsUnknown() {
		pubKey := strings.TrimSpace(plan.PublicKey.ValueString())
		if pubKey == "" {
			diags.AddAttributeError(
				path.Root("public_key"),
				"Invalid public key",
				"public_key must not be empty or contain only whitespace when configured. Omit this attribute to generate a new key pair.",
			)
			return serversdk.KeyPairCreateSchema{}, diags
		}
		body.PublicKey = &pubKey
	}
	return body, diags
}

func buildKeyPairUpdateBody(plan KeyPairResourceModel) (serversdk.KeyPairPartialUpdateSchema, diag.Diagnostics) {
	var diags diag.Diagnostics
	name := strings.TrimSpace(plan.Name.ValueString())
	if name == "" {
		diags.AddAttributeError(
			path.Root("name"),
			"Missing key pair name",
			"name must be configured and not contain only whitespace.",
		)
		return serversdk.KeyPairPartialUpdateSchema{}, diags
	}
	return serversdk.KeyPairPartialUpdateSchema{
		Name: &name,
	}, diags
}
