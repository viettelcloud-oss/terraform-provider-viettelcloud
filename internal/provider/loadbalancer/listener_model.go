package loadbalancer

import (
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
	loadbalancersdk "github.com/viettelcloud-oss/sdks/go/loadbalancer"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/compare"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
)

type ListenerModel struct {
	ID                      types.String `tfsdk:"id"`
	Name                    types.String `tfsdk:"name"`
	Description             types.String `tfsdk:"description"`
	Protocol                types.String `tfsdk:"protocol"`
	ProtocolPort            types.Int64  `tfsdk:"protocol_port"`
	AllowedCIDRs            types.Set    `tfsdk:"allowed_cidrs"`
	ListenerTimeout         types.Int64  `tfsdk:"listener_timeout"`
	ServerTimeout           types.Int64  `tfsdk:"server_timeout"`
	ServerConnectingTimeout types.Int64  `tfsdk:"server_connecting_timeout"`
	EnableH2                types.Bool   `tfsdk:"enable_h2"`
	SetXForwardedFor        types.Bool   `tfsdk:"set_x_forwarded_for"`
	SetXForwardedPort       types.Bool   `tfsdk:"set_x_forwarded_port"`
	SetXForwardedProto      types.Bool   `tfsdk:"set_x_forwarded_proto"`
	ProvisioningStatus      types.String `tfsdk:"provisioning_status"`
	Status                  types.String `tfsdk:"status"`
	CreatedAt               types.String `tfsdk:"created_at"`
	UpdatedAt               types.String `tfsdk:"updated_at"`
}

// ListenerResourceModel references the load balancer, server group, and TLS
// certificates as objects, while the data source keeps their IDs as plain
// strings.
type ListenerResourceModel struct {
	ListenerModel
	LoadBalancer   types.Object `tfsdk:"load_balancer"`
	ServerGroup    types.Object `tfsdk:"server_group"`
	DefaultTLSCert types.Object `tfsdk:"default_tls_cert"`
	SNITLSCerts    types.Set    `tfsdk:"sni_tls_certs"`
}

type ListenerDataSourceModel struct {
	ListenerModel
	LoadBalancerID   types.String `tfsdk:"load_balancer_id"`
	ServerGroupID    types.String `tfsdk:"server_group_id"`
	DefaultTLSCertID types.String `tfsdk:"default_tls_cert_id"`
	SNITLSCertIDs    types.Set    `tfsdk:"sni_tls_cert_ids"`
}

// populateListenerModel maps the API response to the shared model.
func populateListenerModel(listener *loadbalancersdk.ListenerDetailSchema, m *ListenerModel) {
	if listener == nil {
		return
	}

	m.ID = types.StringValue(listener.Id.String())
	m.Name = types.StringValue(listener.Name)
	m.Protocol = types.StringValue(string(listener.Protocol))
	m.ProtocolPort = types.Int64Value(int64(listener.ProtocolPort))
	m.ListenerTimeout = listenerTimeoutState(listener.ListenerTimeout)
	m.ServerTimeout = listenerTimeoutState(listener.ServerTimeout)
	m.ServerConnectingTimeout = listenerTimeoutState(listener.ServerConnectingTimeout)
	m.ProvisioningStatus = types.StringValue(string(listener.ProvisioningStatus))
	m.Status = types.StringValue(string(listener.Status))
	m.CreatedAt = types.StringValue(listener.CreatedAt.Format(time.RFC3339))
	m.UpdatedAt = types.StringValue(listener.UpdatedAt.Format(time.RFC3339))

	m.EnableH2 = types.BoolPointerValue(core.NullableToPtr(listener.EnableH2))
	m.SetXForwardedFor = types.BoolPointerValue(core.NullableToPtr(listener.SetXForwardedFor))
	m.SetXForwardedPort = types.BoolPointerValue(core.NullableToPtr(listener.SetXForwardedPort))
	m.SetXForwardedProto = types.BoolPointerValue(core.NullableToPtr(listener.SetXForwardedProto))

	if len(listener.AllowedCidrs) > 0 {
		values := make([]attr.Value, 0, len(listener.AllowedCidrs))
		for _, cidr := range listener.AllowedCidrs {
			values = append(values, types.StringValue(cidr))
		}
		m.AllowedCIDRs = types.SetValueMust(types.StringType, values)
	} else {
		m.AllowedCIDRs = types.SetNull(types.StringType)
	}

	// ListenerDetailSchema has no description field, so a planned unknown
	// description becomes null instead of staying unknown in state.
	if m.Description.IsUnknown() {
		m.Description = types.StringNull()
	}
}

// populateListenerResourceState maps the API response into resource state,
// preserving configured representations when semantically identical.
func populateListenerResourceState(listener *loadbalancersdk.ListenerDetailSchema, state *ListenerResourceModel) {
	if listener == nil {
		return
	}

	prior := *state
	populateListenerModel(listener, &state.ListenerModel)

	if !prior.Name.IsNull() && !prior.Name.IsUnknown() && strings.TrimSpace(prior.Name.ValueString()) == listener.Name {
		state.Name = prior.Name
	}
	if !prior.Protocol.IsNull() && !prior.Protocol.IsUnknown() && strings.TrimSpace(prior.Protocol.ValueString()) == string(listener.Protocol) {
		state.Protocol = prior.Protocol
	}
	// ListenerDetailSchema has no description field, so a refresh keeps the
	// description that the practitioner configured.
	if !prior.Description.IsNull() && !prior.Description.IsUnknown() {
		state.Description = prior.Description
	}
	if listenerSameStringSet(prior.AllowedCIDRs, state.AllowedCIDRs) {
		state.AllowedCIDRs = prior.AllowedCIDRs
	} else if !prior.AllowedCIDRs.IsNull() && !prior.AllowedCIDRs.IsUnknown() &&
		len(prior.AllowedCIDRs.Elements()) == 0 && len(listener.AllowedCidrs) == 0 {
		state.AllowedCIDRs = prior.AllowedCIDRs
	}

	if !compare.SameUUID(reference.Get(prior.LoadBalancer, reference.ID), listener.LoadBalancer.Id) {
		state.LoadBalancer = reference.Value(reference.ID, types.StringValue(listener.LoadBalancer.Id.String()))
	}

	state.ServerGroup = types.ObjectNull(reference.AttributeTypes(reference.ID))
	if serverGroup := core.NullableToPtr(listener.ServerGroup); serverGroup != nil {
		state.ServerGroup = listenerReference(prior.ServerGroup, serverGroup.Id)
	}

	state.DefaultTLSCert = types.ObjectNull(reference.AttributeTypes(reference.ID))
	if certID := core.NullableToPtr(listener.DefaultTlsCertId); certID != nil {
		state.DefaultTLSCert = listenerReference(prior.DefaultTLSCert, *certID)
	}

	state.SNITLSCerts = types.SetNull(reference.ObjectType(reference.ID))
	if len(listener.SniTlsCerts) > 0 {
		ids := make([]string, 0, len(listener.SniTlsCerts))
		for _, cert := range listener.SniTlsCerts {
			ids = append(ids, cert.Id.String())
		}
		state.SNITLSCerts = reference.Set(reference.ID, ids)
	}
	if compare.SameUUIDSet(listenerReferenceIDSet(prior.SNITLSCerts), listenerReferenceIDSet(state.SNITLSCerts)) {
		state.SNITLSCerts = prior.SNITLSCerts
	} else if !prior.SNITLSCerts.IsNull() && !prior.SNITLSCerts.IsUnknown() &&
		len(prior.SNITLSCerts.Elements()) == 0 && len(listener.SniTlsCerts) == 0 {
		state.SNITLSCerts = prior.SNITLSCerts
	}
}

// listenerTimeoutState maps a nullable timeout of the listener response to
// state, keeping an absent or null timeout null.
func listenerTimeoutState(timeout core.Nullable[int]) types.Int64 {
	value := core.NullableToPtr(timeout)
	if value == nil {
		return types.Int64Null()
	}
	return types.Int64Value(int64(*value))
}

// listenerReference keeps the prior reference when it selects the object
// with the given ID, so the configured spelling of the ID survives a refresh.
func listenerReference(prior types.Object, id core.UUID) types.Object {
	if compare.SameUUID(reference.Get(prior, reference.ID), id) {
		return prior
	}
	return reference.Value(reference.ID, types.StringValue(id.String()))
}

// listenerReferenceIDSet returns the IDs of a reference set as a set of
// strings, keeping a null or unknown set null or unknown.
func listenerReferenceIDSet(references types.Set) types.Set {
	if references.IsNull() {
		return types.SetNull(types.StringType)
	}
	if references.IsUnknown() {
		return types.SetUnknown(types.StringType)
	}
	ids := reference.Strings(references, reference.ID)
	values := make([]attr.Value, 0, len(ids))
	for _, id := range ids {
		values = append(values, id)
	}
	return types.SetValueMust(types.StringType, values)
}

// populateListenerDataSourceState maps the API response into data-source state.
func populateListenerDataSourceState(listener *loadbalancersdk.ListenerDetailSchema, state *ListenerDataSourceModel) {
	if listener == nil {
		return
	}

	populateListenerModel(listener, &state.ListenerModel)
	state.LoadBalancerID = types.StringValue(listener.LoadBalancer.Id.String())

	state.ServerGroupID = types.StringNull()
	if serverGroup := core.NullableToPtr(listener.ServerGroup); serverGroup != nil {
		state.ServerGroupID = types.StringValue(serverGroup.Id.String())
	}

	state.DefaultTLSCertID = types.StringNull()
	if certID := core.NullableToPtr(listener.DefaultTlsCertId); certID != nil {
		state.DefaultTLSCertID = types.StringValue(certID.String())
	}

	state.SNITLSCertIDs = types.SetNull(types.StringType)
	if len(listener.SniTlsCerts) > 0 {
		values := make([]attr.Value, 0, len(listener.SniTlsCerts))
		for _, cert := range listener.SniTlsCerts {
			values = append(values, types.StringValue(cert.Id.String()))
		}
		state.SNITLSCertIDs = types.SetValueMust(types.StringType, values)
	}
}

func preserveListenerConfiguredValues(detail *loadbalancersdk.ListenerDetailSchema, config ListenerDataSourceModel, state *ListenerDataSourceModel) {
	if detail == nil {
		return
	}
	if compare.SameUUID(config.LoadBalancerID, detail.LoadBalancer.Id) {
		state.LoadBalancerID = config.LoadBalancerID
	}
	if compare.SameUUID(config.ID, detail.Id) {
		state.ID = config.ID
	}
	if !config.Name.IsNull() && !config.Name.IsUnknown() && strings.TrimSpace(config.Name.ValueString()) == detail.Name {
		state.Name = config.Name
	}
}

func listenerSameStringSet(v, w types.Set) bool {
	if v.IsNull() || v.IsUnknown() || w.IsNull() || w.IsUnknown() || len(v.Elements()) != len(w.Elements()) {
		return false
	}
	items := make(map[string]struct{}, len(v.Elements()))
	for _, e := range v.Elements() {
		s, ok := e.(types.String)
		if !ok || s.IsNull() || s.IsUnknown() {
			return false
		}
		items[strings.TrimSpace(s.ValueString())] = struct{}{}
	}
	for _, e := range w.Elements() {
		s, ok := e.(types.String)
		if !ok || s.IsNull() || s.IsUnknown() {
			return false
		}
		if _, ok := items[strings.TrimSpace(s.ValueString())]; !ok {
			return false
		}
	}
	return true
}
