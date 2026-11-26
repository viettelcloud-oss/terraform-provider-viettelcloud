package network

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	datasourceschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
	networksdk "github.com/viettelcloud-oss/sdks/go/network"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
)

func TestVPCPeeringDataSourceModelMatchesSchema(t *testing.T) {
	t.Parallel()

	state := tfsdk.State{Schema: vpcPeeringDataSourceSchema(t)}
	if diags := state.Set(context.Background(), &VPCPeeringDataSourceModel{}); diags.HasError() {
		t.Fatalf("data source model does not match schema: %v", diags)
	}
}

func TestVPCPeeringDataSourceConstructorAndMetadata(t *testing.T) {
	t.Parallel()

	var resp datasource.MetadataResponse
	NewVPCPeeringDataSource().Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "viettelcloud"}, &resp)
	if resp.TypeName != "viettelcloud_vpc_peering" {
		t.Fatalf("TypeName = %q, want viettelcloud_vpc_peering", resp.TypeName)
	}
}

func TestVPCPeeringDataSourceConfigure(t *testing.T) {
	t.Parallel()

	d := &VPCPeeringDataSource{}
	var nilResponse datasource.ConfigureResponse
	d.Configure(context.Background(), datasource.ConfigureRequest{}, &nilResponse)
	if nilResponse.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics on nil ProviderData: %v", nilResponse.Diagnostics)
	}

	var invalidResponse datasource.ConfigureResponse
	d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: 123}, &invalidResponse)
	if !invalidResponse.Diagnostics.HasError() {
		t.Fatal("expected a diagnostic for an invalid provider data type")
	}

	client, err := networksdk.NewClient("http://localhost", networksdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("create network client: %v", err)
	}
	var validResponse datasource.ConfigureResponse
	d.Configure(context.Background(), datasource.ConfigureRequest{
		ProviderData: &providerdata.Configured{Network: client, ProjectID: vpcPeeringProjectID},
	}, &validResponse)
	if validResponse.Diagnostics.HasError() || d.client != client || d.projectID != vpcPeeringProjectID {
		t.Fatalf("configure: diagnostics=%v client=%v projectID=%v", validResponse.Diagnostics, d.client, d.projectID)
	}
}

func TestVPCPeeringDataSourceFilterSchema(t *testing.T) {
	t.Parallel()

	dsSchema := vpcPeeringDataSourceSchema(t)
	for _, name := range []string{"id", "name", "requester_vpc_id", "accepter_vpc_id", "status"} {
		attribute, ok := dsSchema.Attributes[name].(datasourceschema.StringAttribute)
		if !ok || !attribute.Optional || !attribute.Computed || attribute.Required {
			t.Fatalf("%s = %#v, want Optional and Computed", name, dsSchema.Attributes[name])
		}
	}
	for _, name := range []string{"description", "vpc_peering_type", "accepter_project_id", "created_at", "updated_at"} {
		attribute, ok := dsSchema.Attributes[name].(datasourceschema.StringAttribute)
		if !ok || attribute.Optional || !attribute.Computed || attribute.Required {
			t.Fatalf("%s = %#v, want Computed only", name, dsSchema.Attributes[name])
		}
	}
}

func TestVPCPeeringDataSourceRejectsInvalidCriteria(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		config  VPCPeeringDataSourceModel
		summary string
	}{
		"no criteria": {
			summary: "Missing VPC Peering lookup criteria",
		},
		"blank name": {
			config:  VPCPeeringDataSourceModel{VPCPeeringModel: VPCPeeringModel{Name: types.StringValue("  ")}},
			summary: "Invalid VPC peering name filter",
		},
		"blank status": {
			config:  VPCPeeringDataSourceModel{VPCPeeringModel: VPCPeeringModel{Status: types.StringValue("")}},
			summary: "Invalid VPC peering status filter",
		},
		"blank id": {
			config:  VPCPeeringDataSourceModel{VPCPeeringModel: VPCPeeringModel{ID: types.StringValue("  ")}},
			summary: "id must be a valid UUID",
		},
		"invalid id": {
			config:  VPCPeeringDataSourceModel{VPCPeeringModel: VPCPeeringModel{ID: types.StringValue("not-a-uuid")}},
			summary: "id must be a valid UUID",
		},
		"invalid requester VPC ID": {
			config:  VPCPeeringDataSourceModel{RequesterVPCID: types.StringValue("not-a-uuid")},
			summary: "requester_vpc_id must be a valid UUID",
		},
		"invalid accepter VPC ID": {
			config: VPCPeeringDataSourceModel{
				RequesterVPCID: types.StringValue(vpcPeeringRequesterVPCID.String()),
				AccepterVPCID:  types.StringValue("not-a-uuid"),
			},
			summary: "accepter_vpc_id must be a valid UUID",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			response := readVPCPeeringDataSource(t, &VPCPeeringDataSource{}, test.config)
			if !response.Diagnostics.HasError() {
				t.Fatal("expected a diagnostic")
			}
			first := response.Diagnostics.Errors()[0]
			if got := first.Summary() + ": " + first.Detail(); !strings.Contains(got, test.summary) {
				t.Fatalf("diagnostic = %q, want it to contain %q", got, test.summary)
			}
		})
	}
}

func TestVPCPeeringDataSourceReadByID(t *testing.T) {
	t.Parallel()

	peering := mockVPCPeering(new("app to db"), networksdk.VPCPeeringStatusActive)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet || req.URL.Path != vpcPeeringPath {
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		writeVPCPeeringResponse(t, w, peering)
	}))
	defer server.Close()

	configuredID := " " + strings.ToUpper(vpcPeeringTestID.String()) + "\n"
	response := readVPCPeeringDataSource(
		t,
		&VPCPeeringDataSource{client: newVPCPeeringTestClient(t, server), projectID: vpcPeeringProjectID},
		VPCPeeringDataSourceModel{VPCPeeringModel: VPCPeeringModel{ID: types.StringValue(configuredID)}},
	)
	if response.Diagnostics.HasError() {
		t.Fatalf("read diagnostics: %v", response.Diagnostics)
	}

	var state VPCPeeringDataSourceModel
	response.State.Get(context.Background(), &state)
	want := VPCPeeringDataSourceModel{
		VPCPeeringModel: VPCPeeringModel{
			ID:                types.StringValue(configuredID),
			Name:              types.StringValue(peering.Name),
			Description:       types.StringValue("app to db"),
			Status:            types.StringValue("active"),
			VPCPeeringType:    types.StringValue("intra_region"),
			AccepterProjectID: types.StringValue(vpcPeeringAccepterProject.String()),
			CreatedAt:         types.StringValue("2026-01-01T00:00:00Z"),
			UpdatedAt:         types.StringValue("2026-01-02T00:00:00Z"),
		},
		RequesterVPCID: types.StringValue(vpcPeeringRequesterVPCID.String()),
		AccepterVPCID:  types.StringValue(vpcPeeringAccepterVPCID.String()),
	}
	if state != want {
		t.Fatalf("state = %#v, want %#v", state, want)
	}
}

func TestVPCPeeringDataSourceReadByIDErrors(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		status  int
		summary string
	}{
		"not found": {status: http.StatusNotFound, summary: "VPC peering not found"},
		"API error": {status: http.StatusInternalServerError, summary: "Error reading VPC peering"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, `{"detail":"error"}`, test.status)
			}))
			defer server.Close()

			response := readVPCPeeringDataSource(
				t,
				&VPCPeeringDataSource{client: newVPCPeeringTestClient(t, server), projectID: vpcPeeringProjectID},
				VPCPeeringDataSourceModel{VPCPeeringModel: VPCPeeringModel{ID: types.StringValue(vpcPeeringTestID.String())}},
			)
			if !response.Diagnostics.HasError() || response.Diagnostics.Errors()[0].Summary() != test.summary {
				t.Fatalf("diagnostics = %v, want %q", response.Diagnostics, test.summary)
			}
		})
	}
}

func TestVPCPeeringDataSourceReadByPairSkipsRejectedAndFailedPeerings(t *testing.T) {
	t.Parallel()

	active := *mockVPCPeering(new(""), networksdk.VPCPeeringStatusActive)
	rejected := *mockVPCPeering(new(""), networksdk.VPCPeeringStatusRejected)
	rejected.Id = core.UUID{0xb1}
	failed := *mockVPCPeering(new(""), networksdk.VPCPeeringStatusError)
	failed.Id = core.UUID{0xb2}
	var query url.Values
	server := vpcPeeringListServer(t, &query, rejected, active, failed)
	defer server.Close()

	configuredRequester := " " + strings.ToUpper(vpcPeeringRequesterVPCID.String()) + " "
	configuredAccepter := "\t" + vpcPeeringAccepterVPCID.String()
	configuredStatus := " active "
	d := &VPCPeeringDataSource{client: newVPCPeeringTestClient(t, server), projectID: vpcPeeringProjectID}
	for name, config := range map[string]VPCPeeringDataSourceModel{
		"pair": {
			RequesterVPCID: types.StringValue(configuredRequester),
			AccepterVPCID:  types.StringValue(configuredAccepter),
		},
		"pair and active status": {
			VPCPeeringModel: VPCPeeringModel{Status: types.StringValue(configuredStatus)},
			RequesterVPCID:  types.StringValue(configuredRequester),
			AccepterVPCID:   types.StringValue(configuredAccepter),
		},
	} {
		response := readVPCPeeringDataSource(t, d, config)
		if response.Diagnostics.HasError() {
			t.Fatalf("%s: read diagnostics: %v", name, response.Diagnostics)
		}
		var state VPCPeeringDataSourceModel
		response.State.Get(context.Background(), &state)
		if state.ID.ValueString() != active.Id.String() || state.RequesterVPCID.ValueString() != configuredRequester ||
			state.AccepterVPCID.ValueString() != configuredAccepter {
			t.Fatalf("%s: state = %#v, want the active peering with the configured VPC IDs", name, state)
		}
		if query.Get("requester_vpc_id") != vpcPeeringRequesterVPCID.String() ||
			query.Get("accepter_vpc_id") != vpcPeeringAccepterVPCID.String() {
			t.Fatalf("%s: unexpected query %v", name, query)
		}
		if config.Status.IsNull() {
			if query.Has("status") {
				t.Fatalf("%s: query %v sends a status filter", name, query)
			}
		} else if query.Get("status") != "active" || state.Status.ValueString() != configuredStatus {
			t.Fatalf("%s: query %v or status %s does not keep the configured status", name, query, state.Status)
		}
	}
}

func TestVPCPeeringDataSourceReadByNameFindsRejectedPeering(t *testing.T) {
	t.Parallel()

	rejected := *mockVPCPeering(new(""), networksdk.VPCPeeringStatusRejected)
	var query url.Values
	server := vpcPeeringListServer(t, &query, rejected)
	defer server.Close()

	configuredName := " " + rejected.Name + " "
	response := readVPCPeeringDataSource(
		t,
		&VPCPeeringDataSource{client: newVPCPeeringTestClient(t, server), projectID: vpcPeeringProjectID},
		VPCPeeringDataSourceModel{VPCPeeringModel: VPCPeeringModel{Name: types.StringValue(configuredName)}},
	)
	if response.Diagnostics.HasError() {
		t.Fatalf("read diagnostics: %v", response.Diagnostics)
	}
	var state VPCPeeringDataSourceModel
	response.State.Get(context.Background(), &state)
	if state.ID.ValueString() != rejected.Id.String() || state.Status.ValueString() != "rejected" || state.Name.ValueString() != configuredName {
		t.Fatalf("unexpected state: %#v", state)
	}
	if query.Get("name") != rejected.Name {
		t.Fatalf("query name = %q, want %q", query.Get("name"), rejected.Name)
	}
}

func TestVPCPeeringDataSourceReadFilterCardinalityErrors(t *testing.T) {
	t.Parallel()

	second := *mockVPCPeering(new(""), networksdk.VPCPeeringStatusPending)
	second.Id = core.UUID{0xb3}
	tests := map[string]struct {
		peerings []networksdk.VPCPeeringSchema
		want     string
	}{
		"zero matches":     {want: "was not found"},
		"multiple matches": {peerings: []networksdk.VPCPeeringSchema{*mockVPCPeering(new(""), networksdk.VPCPeeringStatusActive), second}, want: "is ambiguous"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var query url.Values
			server := vpcPeeringListServer(t, &query, test.peerings...)
			defer server.Close()

			response := readVPCPeeringDataSource(
				t,
				&VPCPeeringDataSource{client: newVPCPeeringTestClient(t, server), projectID: vpcPeeringProjectID},
				VPCPeeringDataSourceModel{RequesterVPCID: types.StringValue(vpcPeeringRequesterVPCID.String())},
			)
			if !response.Diagnostics.HasError() {
				t.Fatal("expected a diagnostic")
			}
			if detail := response.Diagnostics.Errors()[0].Detail(); !strings.Contains(detail, test.want) {
				t.Fatalf("detail = %q, want it to contain %q", detail, test.want)
			}
		})
	}
}

func vpcPeeringDataSourceSchema(t *testing.T) datasourceschema.Schema {
	t.Helper()
	var response datasource.SchemaResponse
	(&VPCPeeringDataSource{}).Schema(context.Background(), datasource.SchemaRequest{}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("data source schema diagnostics: %v", response.Diagnostics)
	}
	return response.Schema
}

// readVPCPeeringDataSource reads d with config, where every attribute that
// config leaves as the zero value is null.
func readVPCPeeringDataSource(t *testing.T, d *VPCPeeringDataSource, config VPCPeeringDataSourceModel) datasource.ReadResponse {
	t.Helper()
	dsSchema := vpcPeeringDataSourceSchema(t)
	configState := tfsdk.State{Schema: dsSchema}
	if diags := configState.Set(context.Background(), &config); diags.HasError() {
		t.Fatalf("set data source config: %v", diags)
	}
	response := datasource.ReadResponse{State: tfsdk.State{Schema: dsSchema}}
	d.Read(context.Background(), datasource.ReadRequest{Config: tfsdk.Config{Raw: configState.Raw, Schema: dsSchema}}, &response)
	return response
}

// vpcPeeringListServer answers every list request with one page of peerings
// and records the query of the last request in query.
func vpcPeeringListServer(t *testing.T, query *url.Values, peerings ...networksdk.VPCPeeringSchema) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet || req.URL.Path != "/v2/network/vpc-peerings/" {
			t.Errorf("unexpected list request: %s %s", req.Method, req.URL.Path)
		}
		*query = req.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(networksdk.PagedVPCPeeringSchema{
			Count: len(peerings), Results: peerings,
		}); err != nil {
			t.Errorf("encode VPC peering page: %v", err)
		}
	}))
}
