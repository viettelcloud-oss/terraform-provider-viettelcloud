package loadbalancer

import (
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
	loadbalancersdk "github.com/viettelcloud-oss/sdks/go/loadbalancer"
)

type ServerGroupSessionPersistenceModel struct {
	Type       types.String `tfsdk:"type"`
	CookieName types.String `tfsdk:"cookie_name"`
}

type ServerGroupHealthCheckModel struct {
	MonitorType    types.String `tfsdk:"monitor_type"`
	Delay          types.Int64  `tfsdk:"delay"`
	Timeout        types.Int64  `tfsdk:"timeout"`
	MaxRetries     types.Int64  `tfsdk:"max_retries"`
	MaxRetriesDown types.Int64  `tfsdk:"max_retries_down"`
	Path           types.String `tfsdk:"path"`
	HTTPMethod     types.String `tfsdk:"http_method"`
	ExpectedCodes  types.String `tfsdk:"expected_codes"`
}

type ServerGroupModel struct {
	ID                 types.String `tfsdk:"id"`
	Name               types.String `tfsdk:"name"`
	Protocol           types.String `tfsdk:"protocol"`
	Algorithm          types.String `tfsdk:"algorithm"`
	Description        types.String `tfsdk:"description"`
	EnableH2           types.Bool   `tfsdk:"enable_h2"`
	SessionPersistence types.Object `tfsdk:"session_persistence"`
	HealthCheck        types.Object `tfsdk:"health_check"`
	ProvisioningStatus types.String `tfsdk:"provisioning_status"`
	Status             types.String `tfsdk:"status"`
	MemberCount        types.Int64  `tfsdk:"member_count"`
	CreatedAt          types.String `tfsdk:"created_at"`
	UpdatedAt          types.String `tfsdk:"updated_at"`
}

// ServerGroupResourceModel references its load balancer as an object, while
// the data source keeps the ID as a plain lookup argument.
type ServerGroupResourceModel struct {
	ServerGroupModel
	LoadBalancer types.Object `tfsdk:"load_balancer"`
}

type ServerGroupDataSourceModel struct {
	ServerGroupModel
	LoadBalancerID types.String `tfsdk:"load_balancer_id"`
}

func serverGroupSessionPersistenceAttrTypes() map[string]attr.Type {
	return map[string]attr.Type{
		"type":        types.StringType,
		"cookie_name": types.StringType,
	}
}

func serverGroupHealthCheckAttrTypes() map[string]attr.Type {
	return map[string]attr.Type{
		"monitor_type":     types.StringType,
		"delay":            types.Int64Type,
		"timeout":          types.Int64Type,
		"max_retries":      types.Int64Type,
		"max_retries_down": types.Int64Type,
		"path":             types.StringType,
		"http_method":      types.StringType,
		"expected_codes":   types.StringType,
	}
}

// populateServerGroupModel maps the API response to the shared model.
func populateServerGroupModel(api *loadbalancersdk.ServerGroupDetailSchema, m *ServerGroupModel) {
	if api == nil {
		return
	}

	m.ID = types.StringValue(api.Id.String())
	m.Name = types.StringValue(api.Name)
	m.Protocol = types.StringValue(string(api.Protocol))
	m.Algorithm = types.StringValue(string(api.Algorithm))
	m.Description = types.StringPointerValue(core.NullableToPtr(api.Description))
	m.EnableH2 = types.BoolPointerValue(core.NullableToPtr(api.EnableH2))
	m.ProvisioningStatus = types.StringValue(string(api.ProvisioningStatus))
	m.Status = types.StringValue(string(api.Status))
	m.MemberCount = types.Int64Value(int64(api.MemberCount))
	m.CreatedAt = types.StringValue(api.CreatedAt.Format(time.RFC3339))
	m.UpdatedAt = types.StringValue(api.UpdatedAt.Format(time.RFC3339))

	if persistenceType := core.NullableToPtr(api.SessionPersistenceType); persistenceType != nil {
		m.SessionPersistence = types.ObjectValueMust(serverGroupSessionPersistenceAttrTypes(), map[string]attr.Value{
			"type":        types.StringValue(string(*persistenceType)),
			"cookie_name": types.StringPointerValue(core.NullableToPtr(api.SessionPersistenceCookieName)),
		})
	} else {
		m.SessionPersistence = types.ObjectNull(serverGroupSessionPersistenceAttrTypes())
	}

	if monitorType := core.NullableToPtr(api.MonitorType); api.EnableHealthCheck && monitorType != nil {
		var delayVal, timeoutVal, maxRetriesVal, maxRetriesDownVal attr.Value
		if delay := core.NullableToPtr(api.MonitorDelay); delay != nil {
			delayVal = types.Int64Value(int64(*delay))
		} else {
			delayVal = types.Int64Null()
		}
		if timeout := core.NullableToPtr(api.MonitorTimeout); timeout != nil {
			timeoutVal = types.Int64Value(int64(*timeout))
		} else {
			timeoutVal = types.Int64Null()
		}
		if maxRetries := core.NullableToPtr(api.MonitorMaxRetries); maxRetries != nil {
			maxRetriesVal = types.Int64Value(int64(*maxRetries))
		} else {
			maxRetriesVal = types.Int64Null()
		}
		if maxRetriesDown := core.NullableToPtr(api.MonitorMaxRetriesDown); maxRetriesDown != nil {
			maxRetriesDownVal = types.Int64Value(int64(*maxRetriesDown))
		} else {
			maxRetriesDownVal = types.Int64Null()
		}

		var pathVal, methodVal, expectedCodesVal attr.Value = types.StringNull(), types.StringNull(), types.StringNull()
		if *monitorType == loadbalancersdk.ServerGroupMonitorTypeHTTP || *monitorType == loadbalancersdk.ServerGroupMonitorTypeHTTPS {
			pathVal = types.StringPointerValue(core.NullableToPtr(api.MonitorPath))
			if method := core.NullableToPtr(api.MonitorHttpMethod); method != nil {
				methodVal = types.StringValue(string(*method))
			}
			expectedCodesVal = types.StringPointerValue(core.NullableToPtr(api.MonitorExpectedCodes))
		}

		m.HealthCheck = types.ObjectValueMust(serverGroupHealthCheckAttrTypes(), map[string]attr.Value{
			"monitor_type":     types.StringValue(string(*monitorType)),
			"delay":            delayVal,
			"timeout":          timeoutVal,
			"max_retries":      maxRetriesVal,
			"max_retries_down": maxRetriesDownVal,
			"path":             pathVal,
			"http_method":      methodVal,
			"expected_codes":   expectedCodesVal,
		})
	} else {
		m.HealthCheck = types.ObjectNull(serverGroupHealthCheckAttrTypes())
	}
}

// populateServerGroupResourceState maps the API response into resource state.
// The response has no load balancer ID, so state keeps the load_balancer
// reference that the configuration or the import ID supplied.
func populateServerGroupResourceState(api *loadbalancersdk.ServerGroupDetailSchema, state *ServerGroupResourceModel) {
	if api == nil {
		return
	}

	configuredName := state.Name
	configuredDesc := state.Description
	configuredProtocol := state.Protocol
	configuredAlgorithm := state.Algorithm
	configuredPersistence := state.SessionPersistence
	configuredHealthCheck := state.HealthCheck

	populateServerGroupModel(api, &state.ServerGroupModel)
	if !configuredName.IsNull() && !configuredName.IsUnknown() && strings.TrimSpace(configuredName.ValueString()) == api.Name {
		state.Name = configuredName
	}
	if description := core.NullableToPtr(api.Description); !configuredDesc.IsNull() && !configuredDesc.IsUnknown() && description != nil && strings.TrimSpace(configuredDesc.ValueString()) == *description {
		state.Description = configuredDesc
	}
	if !configuredProtocol.IsNull() && !configuredProtocol.IsUnknown() && strings.TrimSpace(configuredProtocol.ValueString()) == string(api.Protocol) {
		state.Protocol = configuredProtocol
	}
	if !configuredAlgorithm.IsNull() && !configuredAlgorithm.IsUnknown() && strings.TrimSpace(configuredAlgorithm.ValueString()) == string(api.Algorithm) {
		state.Algorithm = configuredAlgorithm
	}
	state.SessionPersistence = preserveServerGroupNestedStrings(configuredPersistence, state.SessionPersistence, serverGroupSessionPersistenceAttrTypes())
	state.HealthCheck = preserveServerGroupNestedStrings(configuredHealthCheck, state.HealthCheck, serverGroupHealthCheckAttrTypes())
}

// preserveServerGroupNestedStrings retains configured strings only when the
// backend returns an equivalent trimmed value. Null response fields stay null.
func preserveServerGroupNestedStrings(configured, actual types.Object, attributeTypes map[string]attr.Type) types.Object {
	if configured.IsNull() || configured.IsUnknown() || actual.IsNull() || actual.IsUnknown() {
		return actual
	}
	values := actual.Attributes()
	for name, value := range configured.Attributes() {
		prior, ok := value.(types.String)
		if !ok || prior.IsNull() || prior.IsUnknown() {
			continue
		}
		returned, ok := values[name].(types.String)
		if ok && !returned.IsNull() && !returned.IsUnknown() && strings.TrimSpace(prior.ValueString()) == returned.ValueString() {
			values[name] = prior
		}
	}
	return types.ObjectValueMust(attributeTypes, values)
}

// populateServerGroupDataSourceState maps the API response into data-source state.
func populateServerGroupDataSourceState(api *loadbalancersdk.ServerGroupDetailSchema, state *ServerGroupDataSourceModel) {
	if api == nil {
		return
	}
	populateServerGroupModel(api, &state.ServerGroupModel)
}
