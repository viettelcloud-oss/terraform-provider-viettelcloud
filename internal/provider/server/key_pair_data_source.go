package server

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/compare"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/parse"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
	serverlookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/server/lookup"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
	serversdk "github.com/viettelcloud-oss/sdks/go/server"
)

var _ datasource.DataSource = &KeyPairDataSource{}

type KeyPairDataSource struct {
	client    *serversdk.Client
	projectID core.UUID
}

func NewKeyPairDataSource() datasource.DataSource {
	return &KeyPairDataSource{}
}

func (d *KeyPairDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_key_pair"
}

func (d *KeyPairDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Get a Viettel Cloud SSH key pair by ID, name, or fingerprint.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Key pair ID (UUID). Exactly one key pair must match.",
			},
			"name": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Name of the key pair to filter by.",
			},
			"public_key": schema.StringAttribute{
				Computed:    true,
				Description: "Public key material.",
			},
			"fingerprint": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Fingerprint of the key pair to filter by.",
			},
			"type": schema.StringAttribute{
				Computed:    true,
				Description: "Type of the key pair (e.g. rsa).",
			},
			"created_at": schema.StringAttribute{
				Computed:    true,
				Description: "Timestamp when the key pair was created (RFC3339).",
			},
			"updated_at": schema.StringAttribute{
				Computed:    true,
				Description: "Timestamp when the key pair was last updated (RFC3339).",
			},
		},
	}
}

func (d *KeyPairDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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
	d.client = data.Server
	d.projectID = data.ProjectID
}

func (d *KeyPairDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config KeyPairDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	kp, diags := d.getKeyPair(ctx, config)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() || kp == nil {
		return
	}

	state := config
	populateKeyPairDataSourceState(kp, &state)
	preserveConfiguredValues(kp, config, &state)

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (d *KeyPairDataSource) getKeyPair(ctx context.Context, config KeyPairDataSourceModel) (*serversdk.KeyPairSchema, diag.Diagnostics) {
	var diags diag.Diagnostics

	hasID := !config.ID.IsNull() && !config.ID.IsUnknown()
	hasName := !config.Name.IsNull() && !config.Name.IsUnknown()
	hasFingerprint := !config.Fingerprint.IsNull() && !config.Fingerprint.IsUnknown()

	if !hasID && !hasName && !hasFingerprint {
		diags.AddError(
			"Missing key pair lookup criteria",
			"Specify at least one of id, name, or fingerprint to look up a key pair.",
		)
		return nil, diags
	}
	if hasName && strings.TrimSpace(config.Name.ValueString()) == "" {
		diags.AddError("Invalid key pair name filter", "name must not be empty.")
		return nil, diags
	}
	if hasFingerprint && strings.TrimSpace(config.Fingerprint.ValueString()) == "" {
		diags.AddError("Invalid key pair fingerprint filter", "fingerprint must not be empty.")
		return nil, diags
	}

	if hasID && !hasName && !hasFingerprint {
		return d.getKeyPairByID(ctx, config.ID)
	}

	return d.getKeyPairByFilter(ctx, config)
}

func (d *KeyPairDataSource) getKeyPairByID(ctx context.Context, idAttr types.String) (*serversdk.KeyPairSchema, diag.Diagnostics) {
	var diags diag.Diagnostics

	keyPairID, parseDiags := parse.UUIDString(idAttr, "id")
	diags.Append(parseDiags...)
	if diags.HasError() {
		return nil, diags
	}

	kp, err := d.client.GetKeyPair(ctx, keyPairID, serversdk.GetKeyPairParams{
		ProjectID: d.projectID,
	})
	if err != nil {
		if errors.Is(err, serversdk.ErrNotFound) {
			diags.AddError("Key pair not found", fmt.Sprintf("No key pair found with id %s", idAttr.ValueString()))
			return nil, diags
		}
		diags.AddError("Error reading key pair", err.Error())
		return nil, diags
	}
	return kp, diags
}

func (d *KeyPairDataSource) getKeyPairByFilter(ctx context.Context, config KeyPairDataSourceModel) (*serversdk.KeyPairSchema, diag.Diagnostics) {
	var diags diag.Diagnostics
	filter := serverlookup.KeyPairFilter{}

	if !config.ID.IsNull() && !config.ID.IsUnknown() {
		keyPairID, parseDiags := parse.UUIDString(config.ID, "id")
		diags.Append(parseDiags...)
		if diags.HasError() {
			return nil, diags
		}
		filter.ID = &keyPairID
	}
	if !config.Name.IsNull() && !config.Name.IsUnknown() {
		name := config.Name.ValueString()
		filter.Name = &name
	}
	if !config.Fingerprint.IsNull() && !config.Fingerprint.IsUnknown() {
		fingerprint := config.Fingerprint.ValueString()
		filter.Fingerprint = &fingerprint
	}

	finder := serverlookup.NewKeyPairFinder(d.client, d.projectID)
	kp, err := finder.Resolve(ctx, filter)
	if err != nil {
		diags.AddError("Error finding key pair", err.Error())
		return nil, diags
	}
	return &kp, diags
}

func preserveConfiguredValues(kp *serversdk.KeyPairSchema, config KeyPairDataSourceModel, state *KeyPairDataSourceModel) {
	if kp == nil {
		return
	}
	if compare.SameUUID(config.ID, kp.Id) {
		state.ID = config.ID
	}
	configuredName := config.Name
	if !configuredName.IsNull() && !configuredName.IsUnknown() && strings.TrimSpace(configuredName.ValueString()) == kp.Name {
		state.Name = configuredName
	}
	configuredFingerprint := config.Fingerprint
	if !configuredFingerprint.IsNull() && !configuredFingerprint.IsUnknown() &&
		strings.EqualFold(strings.TrimSpace(configuredFingerprint.ValueString()), kp.Fingerprint) {
		state.Fingerprint = configuredFingerprint
	}
}
