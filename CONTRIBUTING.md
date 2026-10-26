# Contributing to Viettel Cloud Terraform Provider

## Contents

**Getting started** — [Prerequisites](#prerequisites) ·
[Setup](#setup) · [Verification](#verification) ·
[PR Checklist](#pr-checklist)

**Writing code**

- [Code Structure](#code-structure) —
  [Architecture](#architecture),
  [Provider Package Layout](#provider-package-layout),
  [Terraform-Specific Helpers](#terraform-specific-helpers),
  [Importing the SDK](#importing-the-sdk),
  [Injecting the Client](#injecting-the-client-into-resources)
- [Naming Conventions](#naming-conventions)
- [Comments](#comments)
- [Code Style](#code-style)

**Design rules** (normative)

- [Provider Design Decisions](#provider-design-decisions) — attribute
  configurability, plan modifiers
- [References and Lookups](#references-and-lookups) — reference objects,
  human-readable references, the finder pattern, data-source filtering
- [Request and Lifecycle Semantics](#request-and-lifecycle-semantics) —
  create/update bodies, [lifecycle waiters](#lifecycle-waiters)
- [Resource Implementation Practices](#resource-implementation-practices)

**Recipes**

- [Adding a New Resource](#adding-a-new-resource)
- [Adding a New Data Source](#adding-a-new-data-source)
- [Test Conventions](#test-conventions) —
  [Unit Tests](#unit-tests),
  [Real-API Integration Tests](#real-api-integration-tests),
  [Measuring Coverage](#measuring-coverage)

## Prerequisites

- Go >= 1.26
- Terraform >= 1.16
- pre-commit (optional but recommended)
- golangci-lint (installed via `make tools`)

## Setup

```bash
# Install developer tools
make tools
```

Run the [verification](#verification) commands after setup and before
submitting a change. See [Importing the SDK](#importing-the-sdk) to upgrade
the SDK.

## Code Structure

### Architecture

```
github.com/viettelcloud-oss/sdks/go ← SDK module (generated clients per service)
        ↓
internal/provider/     ← Configures and passes public SDK clients directly
        ↓
internal/provider/<service>/ ← Resources/data sources grouped by SDK service
```

### Provider Package Layout

```
internal/
├── compare/                       # Semantic equality
├── parse/                         # Value conversion
├── wait/                          # Asynchronous polling
├── finder/                        # Shared finder mechanics
├── reference/                     # Reference object schemas and values
├── selector/                      # Catalog selector logic
└── provider/
    ├── provider.go                # Configuration and registration
    ├── providerdata/              # SDK clients and project context
    ├── project/
    │   └── lookup/                # Region, zone, and project finders
    └── <service>/
        ├── lookup/                # Finders for service-owned entities
        ├── selector/              # Catalog selectors for service-owned entities
        ├── <entity>_model.go      # Models and response-to-state mapping
        ├── <entity>_resource.go   # Managed resource
        └── <entity>_data_source.go
```

Code under `provider/` is grouped by the public SDK service that owns it,
such as `network`, `server`, or `blockstorage`. `providerdata` carries
configured SDK clients and project context to those packages.

#### Lookups and cross-service references

Put each finder in the `lookup` package of the SDK service that owns the
entity. This includes region, zone, and project finders, which belong in
`project/lookup`.

A service may import another service's `lookup` package. A `lookup` package
may import `internal/finder`, but no other package from this repository. This
keeps lookups independent of Terraform code and prevents import cycles.
`make lint` enforces the rule.

In a service package, alias every lookup and selector import by service, even
in a file that imports only one:

```go
import (
	networklookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/network/lookup"
	serverlookup  "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/server/lookup"
)
```

Go scopes import names per file, so a bare `lookup` names whichever package
that one file imported. Two files in the same service package can then read
`lookup.` and mean different things — `lookup.NewSubnetFinder` against
`network/lookup` in one, `lookup.NewRegionFinder` against `project/lookup` in
the next. Aliasing by service gives every prefix one meaning across the
package, and leaves nothing to change when a file later needs a second lookup.
`provider.go` is not a service package and imports only `project/lookup`, so
it uses the plain package name.

Resolve any additional service data in the caller; lookup packages must not
depend on one another.

#### Reusable helpers

Put service-independent helpers directly under `internal/`, with one package
per purpose. Add a package only after a second entity needs the behavior; until
then, keep the helper with its caller.

Choose exported names that read naturally with the package name:

- Avoid stuttering: use `parse.UUIDString`, not `parse.ParseUUIDString`.
- Use `New` when the package has one main type, as in `wait.New`.
- Name packages for their purpose; do not add `util`, `common`, `core`,
  `helpers`, or `misc` packages.

Every helper package needs a package doc comment. Keep `internal/finder` free
of SDK and Terraform imports.

#### Three files per entity

Keep an entity in these three production files:

| File | Holds |
| --- | --- |
| `<entity>_model.go` | Models, `attr.Type` maps, and all response-to-state mapping |
| `<entity>_resource.go` | The complete managed resource |
| `<entity>_data_source.go` | The complete data source |

Use matching `_test.go` files. Put fixtures shared by at least two test files
in `test_helpers_test.go`.

Within resource and data-source files, keep Framework entry points in this
order:

```
interface assertions, constants, types
constructor
Metadata → Schema → Configure → ValidateConfig → Create → Read → Update → Delete → ImportState
private receiver helpers and waiters
pure helpers
```

For a data source, the sequence ends at `Read`. `make lint` checks the order.
File length alone is not a reason to split an entity.

#### Non-standard files

Create a separate file only for a cohesive sub-domain that spans operations.
Name it after that sub-domain, for example
`server_private_ip_attachment.go`. Keep its domain logic and any custom plan
modifier together.

Add a comment after the package and imports. Start it with the filename and say
what the file owns:

```go
package server

// server_private_ip_attachment.go owns private-IP attachment matching and
// reconciliation.
```

Prefer another file in the service package to a new package. Create a package
only when multiple entities need the boundary. `make lint` checks that every
non-standard file includes an ownership comment.

### Terraform-Specific Helpers

Before implementing Framework-dependent logic in a resource or data source,
check the Framework API and the helper packages under `internal/`
(`compare`, `parse`, `reference`, `wait`) for an existing solution. This
is especially important for:

- checking Framework value states such as `null` and `unknown`;
- parsing or validating common representations such as UUIDs;
- converting between Framework values and Go or SDK values; and
- logic that already appears in another resource or data source.

Prefer Framework-provided APIs over local wrappers. Add a helper package only
when the Framework has no equivalent and the same
service-independent behavior is needed by at least two provider entities.
Organize helpers by purpose, one package each, and open every package with a doc
comment saying what it owns: `compare` for normalized comparison, `parse` for
parsing and conversion, `reference` for reference objects, `wait` for
lifecycle polling.
Keep CRUD orchestration, API calls, service-specific request construction, and
resource state mapping in their owning provider packages.

### Importing the SDK

The API clients are generated in the SDK module
`github.com/viettelcloud-oss/sdks/go`. The provider requires a released SDK
version in `go.mod`, with no `replace` directive:

```go
require github.com/viettelcloud-oss/sdks/go v0.1.0
```

SDK release tags use the format `go/vX.Y.Z` because the module lives in the
SDK repository's `go/` subdirectory. The tag `go/v0.1.0` is the module version
`v0.1.0`. Always require a release tag. A pseudo-version (`v0.0.0-<time>-<sha>`)
names a commit that the public mirror does not have.

Provider code imports the service packages, for example
`github.com/viettelcloud-oss/sdks/go/network`, and
`github.com/viettelcloud-oss/sdks/go/core` for shared SDK types such as
`core.UUID` and retry policies.

To upgrade to the newest SDK release, run:

```bash
make sdk-update
```

To pin a release, pass its version, for example `SDK_VERSION=v0.1.0`. Go
picks the newest release tag and skips pre-release tags. Run
`go list -m -u github.com/viettelcloud-oss/sdks/go` to see if a newer release
exists without a change to `go.mod`.

The target runs `go get` and `go mod tidy`, then prints the SDK version that
`go.mod` now requires. Review the SDK diff for contract changes. Commit `go.mod` and
`go.sum` only after the [verification](#verification) commands and any affected
real-API tests pass.

#### Developing against a local SDK checkout

To build the provider against an unreleased SDK change, clone the SDK next to
the provider and create a Go workspace in the parent directory:

```bash
go work init ./terraform-provider-viettelcloud
go work edit -replace=github.com/viettelcloud-oss/sdks/go=./vcloud-sdks/go
```

Go then builds the provider with the local SDK checkout, and `go.mod` does not
change. Use `replace` in `go.work`, not `go work use ./vcloud-sdks/go`. With
`use`, Go still downloads the `go.mod` of the SDK version that the provider
requires, and fails if that version is not released yet. Do not commit
`go.work` or `go.work.sum`. Release the SDK change before the provider MR
that needs it, then run `make sdk-update`.

### Injecting the Client into Resources

In `internal/provider/provider.go`, `Configure` initializes public SDK clients
and passes them through `providerdata.Configured`:

```go
func (p *ViettelCloudProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
    // ... validate config ...
    networkClient, err := network.NewClient(
        endpoint,
        network.WithPAT(token),
        network.WithRetry(core.DefaultRetry()),
    )
    // Initialize the remaining service clients in the same way.
    data := &providerdata.Configured{
        Network:      networkClient,
        Project:      projectClient,
        Server:       serverClient,
        BlockStorage: blockStorageClient,
        ProjectID:    projectID,
    }
    resp.ResourceData = data
    resp.DataSourceData = data
}
```

Resources access the client in their `Configure` method:

```go
func (r *VPCResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
    if req.ProviderData == nil {
        return
    }
    data, ok := req.ProviderData.(*providerdata.Configured)
    if !ok {
        resp.Diagnostics.AddError("Unexpected provider data type", fmt.Sprintf("Expected *providerdata.Configured, got %T", req.ProviderData))
        return
    }
    r.client = data.Network
    r.project = data.Project
    r.projectID = data.ProjectID
}
```

## Naming Conventions

### Files

| Type | Pattern | Example |
|------|---------|---------|
| Resource | `<resource>_resource.go` | `server_resource.go` |
| Resource test | `<resource>_resource_test.go` | `server_resource_test.go` |
| Data source | `<resource>_data_source.go` | `server_data_source.go` |
| Data source test | `<resource>_data_source_test.go` | `server_data_source_test.go` |

### Go Identifiers

| Type | Pattern | Example |
|------|---------|---------|
| Resource struct | `<Resource>Resource` | `VPCResource` |
| Resource model | `<Resource>ResourceModel` | `VPCResourceModel` |
| Shared model | `<Resource>Model` (when schemas share response fields) | `VPCModel` |
| Shared populate fn | `populate<Resource>Model(api, *<Resource>Model)` | `populateVPCModel(...)` |
| Constructor | `New<Resource>Resource` | `NewVPCResource` |
| Data source struct | `<Resource>DataSource` | `VPCDataSource` |
| Data source model | `<Resource>DataSourceModel` | `VPCDataSourceModel` |
| Constructor | `New<Resource>DataSource` | `NewVPCDataSource` |

Prefer the shortest name that remains unambiguous in its scope. There is no
blanket preference for either one-letter names or full words: a name becomes
more descriptive as the distance between its declaration and use grows.

- Keep conventional locals such as `i`, `err`, `ctx`, `req`, `resp`, `got`,
  `want`, and `tt` short. Use a consistent one- or two-letter receiver, such as
  `r` for a resource or `d` for a data source.
- Use a one-letter argument only when its type and a short function make its
  role obvious, such as `m` in `populateVPCModel(..., m *VPCModel)`. Use words
  such as `plan`, `state`, `candidate`, or `configuredName` when values have
  distinct roles or a wider scope.
- Avoid abbreviations that are not established in the package. Preserve Go
  initialism casing such as `ID`, `URL`, `HTTP`, `VPC`, and `projectID`.
  Generated public SDK identifiers are exempt.

### Terraform Types

| Type | Pattern | Example |
|------|---------|---------|
| Resource | `viettelcloud_<prefix>_<resource>` | `viettelcloud_lb_listener` |
| Data source | `data.viettelcloud_<prefix>_<resource>` | `data.viettelcloud_lb_listener` |
| Core resource | `viettelcloud_<resource>` | `viettelcloud_subnet` |

`<prefix>` names the service and `<resource>` names the entity within it. A
resource and its data source share one type name.

- Write `<prefix>` as one lowercase token with no underscore, so the first
  token after `viettelcloud_` always names the service. Use the product
  acronym when the service has one, such as `vke` or `kms`. Otherwise use one
  short word, such as `autoscaling` or `baremetal`.
- Write `<resource>` in snake_case and spell each word out:
  `snapshot_policy`, `node_pool`, `certificate`. Keep established acronyms such
  as `l7` and `rrset`.
- Always put a resource name after the prefix, even when it repeats the
  service: `viettelcloud_lb_loadbalancer`, `viettelcloud_vke_cluster`.
- Network, server, and block storage resources are core and take no prefix:
  `viettelcloud_vpc`, `viettelcloud_security_group`, `viettelcloud_server`,
  `viettelcloud_volume_snapshot`. A core resource name must not start with a
  service prefix.

| Service | Prefix | Package | Example |
|---------|--------|---------|---------|
| Network | Core | `internal/provider/network` | `viettelcloud_security_group_rule` |
| Server | Core | `internal/provider/server` | `viettelcloud_placement_group` |
| Block Storage | Core | `internal/provider/blockstorage` | `viettelcloud_volume_snapshot` |
| Load Balancing | `lb` | `internal/provider/loadbalancer` | `viettelcloud_lb_server_group` |

Add a row in the same change that adds a service's first resource. Choose the
prefix once: renaming a type is a breaking change for every user's state.

File names and Go identifiers drop the prefix, because the package already
names the service: `viettelcloud_lb_listener` lives in
`internal/provider/loadbalancer/listener_resource.go` as `ListenerResource`.

## Comments

A comment gives the reason the code is shaped the way it is. Write one only
when a reader who knows Go, Terraform, and this provider would still be
surprised — a backend contract, a Framework behavior, a deliberate omission.

**Put it on what it explains.** A comment describes the declaration directly
beneath it, and nothing else.

- File- or module-level rationale goes at the top of the file: the package
  comment in Go, a header above `terraform {}` in an HCL fixture. Never on the
  first `variable`, `const`, or `var` block.
- One idea per block. Two reasons means two comments.

**Keep it readable in one pass.**

- Lead with the claim, then the reason. The first sentence must stand alone.
- Prefer 25 words per sentence; past 30 is a defect. Keep an in-body comment
  under six lines. A doc comment may run longer if it holds several facts.
- Keep `that` when a relative clause has its own subject: "a Placement Group
  that another run created", not "a Placement Group another run created".
- Name the thing — the run name, `state_key`, attribute, identifier — never
  "another run" or "the modifier above".
- Present tense, 80 columns.

**Go.** A declaration comment starts with the identifier: `// waitForServer
polls the server until…`, unexported ones included. A `Test*` function is the
exception: its name is already the sentence, so give only the reason.

**HCL.** Same rules with `#`. A fixture opens with a header stating its purpose
and any constraint, such as being plan-only; see
[Real-API Integration Tests](#real-api-integration-tests). Commented-out
configuration needs a note saying what restores it. `examples/` comments are
practitioner labels, not internal rationale.

**Reviewing one.** Is it needed? On the right declaration? Does the first
sentence stand alone? Is every referent named? A "no" is a rewrite, not a nit.

## Provider Design Decisions

The decisions in this section and the three that follow — references and
lookups, request and lifecycle semantics, and resource implementation practices
— are normative for generated and handwritten provider code. Depart from them
only when the public SDK or backend contract requires a different design.
Document the constraint next to the affected implementation, cover its
observable behavior with tests, and call it out in the merge request.

### Attribute Configurability

Choose schema flags from value ownership and omission behavior, not directly
from whether an API request field is required or optional.

| Behavior | Schema |
| --- | --- |
| The practitioner must supply the value and no safe default exists | `Required` |
| The practitioner may supply the value; omission remains `null` in state | `Optional` |
| Only the backend or provider supplies the value | `Computed` |
| The practitioner may supply the value; otherwise the backend or provider supplies or preserves the effective state value | `Optional` and `Computed` |

Use `Optional` and `Computed` only when the unconfigured value must still be
represented in state, such as a backend default or a preserved backend value.
Define whether removing configuration preserves, defaults, or clears the field,
and test that lifecycle explicitly. For data sources, make lookup arguments
`Required` or `Optional` and API-only results `Computed` by default.

Make every descendant of an `Optional` and `Computed` nested attribute
`Computed` too when omission preserves its prior value. Otherwise, Terraform
can propose null for the omitted attribute and leave runtime outputs unknown
on every plan. Enforce required inner values with a validator or in
`ValidateConfig`. For inputs whose omission selects a default, copy the
configured value in a plan modifier, as `preserveElasticIPState` does.
`TestProviderSchemaOmittedNestedAttributesKeepPriorValue` enforces this rule
for every resource.

### Common Plan Modifiers

Plan modifiers shape Terraform's proposed plan; CRUD must implement the same
lifecycle contract. Use the typed package matching the attribute.

| Modifier | Plan behavior | Use case |
| --- | --- | --- |
| `RequiresReplace()` | `state A -> plan B/null/unknown`: replace | Every change to an immutable attribute creates a new object, including VPC `cidr`, subnet `vpc`, or private IP `subnet`. |
| `RequiresReplaceIfConfigured()` | `configured A -> configured B`: replace; `configured A -> omitted/null`: no replacement | An immutable `Optional` and `Computed` value is allocated or preserved when omitted, such as private IP `ip_address` and `mac_address`. |
| `RequiresReplaceIf(...)` | `state A -> plan B` and predicate `true`: replace; predicate `false`: update in place | Replacement depends on provider-defined, deterministic conditions. |
| `UseStateForUnknown()` | `state A + plan unknown -> plan A` | A computed value is guaranteed not to change during the operation, such as `id` or `created_at`. Do not use it for values that can change or must be recomputed during replacement. |

Test `A -> B`, `A -> omitted/null`, and `state A -> plan unknown`. Treat an
explicit empty value as configured, not omitted. See the Terraform Framework
[plan modification documentation](https://developer.hashicorp.com/terraform/plugin/framework/resources/plan-modification).

#### Where plan modifiers live

Declare built-in modifiers inline in `Schema()`. Keep a custom modifier with
the domain logic it protects, including its identity comparison. If that needs
a separate file, follow [Non-standard files](#non-standard-files). Custom
modifiers are not part of the Framework entry-point order.

## References and Lookups

### Reference Objects

Model every practitioner-supplied reference to another object as a nested
object of criteria, never as a plain string. This rule applies to a single
reference and to each element of a set of references:

```hcl
vpc             = { id = viettelcloud_vpc.example.id }
zone            = { name = "vn-central-1a" }
security_groups = [{ id = viettelcloud_security_group.example.id }]
```

Name the attribute after the referenced object, without an `_id` or `_name`
suffix. Name each criterion after the attribute of the referenced object that
it matches, such as `id`, `name`, or `cidr`.

A later release can then add a criterion as a new attribute in the object.
Existing configurations stay valid, and Terraform decodes existing state with
the new attribute null, so the change is not breaking. A change from a plain
string to an object breaks every configuration and every state that uses the
attribute.

Build reference schemas and values with `internal/reference`. Put plan
modifiers on the object, not on the criterion. When a reference accepts more
than one criterion, make each criterion `Optional` and validate that exactly
one is configured. For an `Optional` and `Computed` reference, use
`reference.PreservedAttributes`. Its validator requires the criterion when
the reference is configured.

Catalog selectors are the exception to `internal/reference` and to the
exactly-one-criterion rule. The `image` and `volume_type` references of
servers and volumes match all configured criteria together, so
`{ os = "Ubuntu", version = "22.04" }` selects the image that matches both.
See [Catalog Selectors](#catalog-selectors).

The rule covers resource arguments. Data-source lookup arguments and computed
outputs about a referenced object stay flat; see
[Data Source Design and Filtering Conventions](#data-source-design-and-filtering-conventions)
and [Computed Outputs for Referenced Resources](#computed-outputs-for-referenced-resources).

### Human-readable References and Stable Identity

Prefer a unique, stable, human-readable name or address in practitioner-facing
configuration over a raw ID. Keep the managed object's backend ID as a computed
attribute and use it as the stable identity for `Read`, `Update`, `Delete`, and
import whenever the API supports direct lookup.

When the SDK requires an ID but configuration supplies a name or address:

1. Resolve it at the API boundary through the owning service's lookup package.
2. Implement a finder that calls paginated public SDK list methods—passing available SDK filter parameters (such as `name`) when supported by the API to narrow backend results—and returns every exact candidate within all relevant parent scopes.
3. Keep selection and use-case validation as pure functions. A finder composes
   listing, exact matching, and unique selection for callers that require
   exactly one object.
4. Return a diagnostic for zero or multiple matches; never select the first
   candidate silently.
5. Pass the resolved ID to the SDK request. Do not add a process-wide cache.

When configuration already contains an ID and the public SDK has a direct
`Get` method, use it instead of listing. Lookup code must not contain CRUD,
waiters, request builders, state mapping, or Terraform types.

This preference has trade-offs:

| Reference | Benefits | Costs and risks |
| --- | --- | --- |
| Name or address | Readable configuration and plans; fewer opaque values to copy | A rename can break lookup, and a reused value can bind to a different object; uniqueness checks add API calls and may require pagination |
| ID | Usually unique, immutable, and efficient with a direct `Get` API; suitable for state and import | Hard to recognize and type manually; exposes backend identifiers in configuration |

When referencing another Terraform-managed object, use a Terraform expression
that reads its `name`, `address`, or `id` attribute instead of a literal so
Terraform records the dependency.

Use an ID in configuration instead when no name/address is reliably unique and
stable in a documented scope, the API cannot resolve it safely, or lookup cost
would be unreasonable. If uniqueness requires more context, expose that scope
explicitly or use a composite reference rather than guessing.

Preserve the configured representation in state when it is semantically equal
to a backend-normalized value. Do not suppress a difference that identifies a
different object or hides real drift.

### Catalog Selectors

A catalog selector references a catalog entry, such as an image or a volume
type, by a combination of criteria. Selector code has two layers:

- `internal/selector` holds the logic that every selector shares: the schema
  builder, criteria validation and equality, identity decisions, and refresh.
- The service that owns the entry defines its selector in
  `<service>/selector/<entity>.go`: the criteria schema, the lookup filter,
  and the resolve call.

Each selector records the selected catalog entry in a computed `id`. The
provider decides rebuild, replacement, and retype from that ID only, and
tracks catalog identity, not catalog metadata. A resource that uses a
selector must:

- call selector planning in `ModifyPlan` before any guard that requires fully
  known configuration, so that an unrelated unknown input cannot hide the
  replacement of an immutable selector;
- keep the current ID when it still matches changed criteria, and otherwise
  leave the planned ID unknown and resolve it at apply;
- at apply, trust a known ID without a catalog lookup, and compare a newly
  resolved ID with the current ID before a mutation;
- record resolved IDs in state before any later step that can fail; and
- refresh from the entry ID in the resource response without catalog
  requests, keep the criteria when the ID matches, and reset the selector to
  the reported name and ID when it differs.

The selector `id` has three states:

- A known ID is the selected entry.
- An unknown ID is a pending selection. The plan changed the entry, and apply
  resolves it.
- A null ID means that the backend response does not identify the entry, for
  example after an image leaves the catalog. Only state written before
  selectors recorded IDs has a null ID, because refresh never replaces a
  known ID with null.

Detect a pending selection with `selector.Pending`, not by the absence of a
known ID, so that a null ID never triggers a rebuild, replacement, or retype.
When the fallback read of the current entry finds no identity, return an
error that wraps `selector.ErrNoIdentity`. `selector.Decide` then keeps the
entry while the criteria are unchanged, and the plan keeps the null ID, which
refresh cannot contradict.

Import starts with a name and ID selector, so an import configuration that
selects the same ID by other criteria plans no change.

### Computed Outputs for Referenced Resources

Represent computed information about a referenced or parent resource as flat,
top-level attributes. For example, a private IP exposes its referenced subnet
as `subnet_name` and `subnet_cidr`, not as a computed nested object. Keep these outputs
minimal—usually the stable ID and/or human-readable name needed by the child
contract. Users who need other parent details should reference the managed
parent directly or use its data source. Reserve nested objects for structured
relationships that include practitioner configuration, per-item attachment
settings, or a cohesive mix of inputs and computed outputs.

### Lookup Conventions (Finder Pattern)

Use a finder for paginated or multi-criteria SDK searches. Keep direct SDK
`Get` calls by ID in the resource or data source. Follow the ownership and
dependency rules in [Lookups and cross-service references](#lookups-and-cross-service-references).

Each entity finder defines:

- A minimal `<Entity>Lister` interface containing only the paginated SDK list
  method.
- An `<Entity>Filter` with one optional field per criterion. Keep description,
  exact matching, and SDK parameter mapping together in `String`, `matches`,
  and `params` methods.
- An `<Entity>Finder` with `Find` for all exact candidates and `Resolve` for
  exactly one. Assert the common shape with
  `var _ finder.Interface[<Entity>Filter, <Schema>] = (*<Entity>Finder)(nil)`.
- A concise candidate summary for ambiguity errors.
- An `<Entity>ResolveFunc` only when a caller needs dependency injection.

Use `finder.Collect`, `finder.ExactlyOne`, and the filter-description helpers
from `internal/finder`; do not reimplement them. Assert the common finder shape
with `finder.Interface`.

Pass every supported filter to the backend, then verify exact equality
client-side. Normalize each criterion consistently in the request, match, and
description; blank strings are unset within a finder, while callers validate
required values. Never select one of multiple matches silently.

Keep lookup code free of CRUD, request construction, state mapping, and
Terraform Framework types. Test every finder offline with a fake lister,
covering exact, zero, and multiple matches plus SDK errors; test shared helpers
in `lookup_test.go`.

### Data Source Design and Filtering Conventions

Data sources support flexible lookups by ID or combinations of human-readable criteria (e.g., `name`, `cidr`, `region`). Every data source must follow these conventions:

#### 1. Schema Design
- **Lookup Filters**: Attributes that practitioners can filter by (e.g., `id`, `name`, `cidr`, `region`) must be marked `Optional: true` and `Computed: true`.
- **Result-Only Attributes**: Read-only entity properties (e.g., `description`, `display_name`, `created_at`, `updated_at`) must be marked `Computed: true`.
- **Human-Readable References**: Expose human-readable names (e.g., `region` as a string) rather than raw backend IDs (`region_id`) in practitioner-facing data source schemas.

#### 2. Read Orchestration and Helper Architecture
Keep `Read()` concise (~15–20 lines) as an orchestrator and delegate tasks to focused private methods:

```go
func (d *<Entity>DataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config <Entity>DataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	entity, diags := d.get<Entity>(ctx, config)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() || entity == nil {
		return
	}

	state := config
	populate<Entity>DataSourceState(entity, &state)
	preserveConfiguredValues(entity, config, &state)

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
```

- **`d.get<Entity>(ctx, config)`**: Validates that at least one filter criterion is specified and non-empty. Dispatches to `d.get<Entity>ByID` when only `id` is specified; otherwise dispatches to `d.get<Entity>ByFilter`.
- **`d.get<Entity>ByID(ctx, idAttr)`**: Fast-path direct SDK `Get` when looking up by ID alone.
- **`d.get<Entity>ByFilter(ctx, config)`**: Constructs `<Entity>Filter`, resolves
  referenced entities through their owning lookup packages, and calls the
  entity finder.
- **`preserveConfiguredValues(entity, config, state)`**: Preserves the practitioner's configured representation (e.g., casing or whitespace trimming) when semantically identical to the backend value to prevent false drift.

#### 3. Validation and Error Handling
- Return an actionable diagnostic error if no criteria are supplied (`"Missing <Entity> lookup criteria"`).
- Reject whitespace-only filter strings.
- Reject invalid UUIDs with `parse.UUIDString`.
- Enforce strict uniqueness: return an actionable diagnostic error on zero matches ("not found") or multiple matches ("ambiguous candidates").

## Request and Lifecycle Semantics

### Create and Update Semantics: Omitted, Empty, PUT, and PATCH

The public SDK and backend API contract are authoritative. Preserve the
difference between an unconfigured attribute, Terraform `null`, `unknown`, and
an explicit empty or zero value. Use pointer or optional SDK fields for field
presence; never populate request bodies mechanically from Go zero values.

| Terraform value | Request behavior |
| --- | --- |
| `unknown` | Never serialize it. Return a diagnostic if the operation requires a known value; otherwise omit the field. |
| Unconfigured or `null` | Omit the field when API omission means preserve or default. Do not convert it to `""`, `0`, `false`, an empty collection, or JSON `null`. |
| Known empty or zero | Send it only when the user explicitly requested clearing and the SDK contract supports that representation. |
| Known non-empty value | Send it when required for creation or selected by the update semantics below. |

Terraform may decode an unconfigured optional attribute as `null`; do not
promise a distinction between omission and explicit `null` unless the schema
and Framework values can preserve it. If the API cannot distinguish omission
from clearing, document the supported behavior instead of simulating it.

- On create, omit unconfigured optional fields unless the API requires them.
- On update, read desired values from the plan. Compare the plan with prior
  state when the API accepts only changed fields, uses separate operations, or
  when avoiding unrelated writes matters. Do not copy prior state into a
  partial request unless the API contract requires it.
- Use the public SDK update operation whose semantics match the backend. If the
  SDK offers both safe choices, prefer `PATCH` or another partial-update method
  for sparse changes. Use `PUT` only with the complete representation expected
  by that endpoint, deliberately carrying forward every required field.
- Do not bypass a published SDK method merely to choose a different HTTP verb.
  API-specific semantics override generic HTTP expectations and must be
  covered by request-body and lifecycle tests.
- After a successful create or update, map the returned object into state, or
  perform a `Read` when the response is incomplete. State must contain known or
  null values and must remain consistent with the Terraform plan. Preserve a
  prior configured value when the backend value is semantically equal.
- Map response presence literally: a non-nil pointer to an empty or zero
  value maps to a known empty/zero Terraform value; a nil pointer maps to
  null. Never reinterpret a nil pointer as "explicitly cleared" based on
  prior state or config — presence must be read from the response itself.
  Viettel Cloud's SDK contract guarantees a cleared string field (e.g.
  `description`) comes back as a non-nil `""`, never nil, so mocks must
  return `new("")` for it. Add a per-endpoint exception, with its own test,
  only when backend evidence shows that endpoint actually violates this
  contract.

### Lifecycle Waiters

Add a waiter when Terraform must not return until the backend finishes an
asynchronous operation, such as an Elastic IP receiving its address, a volume
becoming available, or a server reaching a running state. Use
`wait.New[T]` from `internal/wait/` instead of writing a polling
loop: deadline derivation, cancellation, timeout attribution, poll spacing,
and not-ready reporting are identical for every resource and belong in one
place.

```go
func (r *<Resource>Resource) waitUntilReady(
	ctx context.Context,
	id core.UUID,
	timeout time.Duration,
	pollInterval time.Duration,
) (*sdk.<Resource>Schema, error) {
	w := wait.New[*sdk.<Resource>Schema](pollInterval, timeout)
	obj, err := w.WaitFor(ctx, func(ctx context.Context) (*sdk.<Resource>Schema, error) {
		obj, err := r.get(ctx, id)
		if err != nil {
			return nil, err
		}
		if !<ready predicate> {
			return nil, fmt.Errorf("<reason>: %w", wait.ErrNotReady)
		}
		return obj, nil
	})
	if err != nil {
		return nil, fmt.Errorf("<Resource> %s: %w", id, err)
	}
	return obj, nil
}
```

- `WaitFor` returns the value from the successful poll. Map it into Terraform
  state directly; do not add a refresh call after a successful wait.
- The poll function owns both the SDK call and outcome classification. Return
  a value with `nil` when ready, an error wrapping `wait.ErrNotReady` when
  polling should continue, and any other error to fail immediately. Include a
  concise reason when wrapping `ErrNotReady`; the final reason is surfaced on
  timeout.
- A delete poll treats the SDK not-found error as success. A terminal backend
  state returns a fatal error immediately instead of polling until timeout.
- Keep the waiter itself a private receiver helper on the resource. Extract a
  pure readiness helper only when the same predicate needs independent tests.
- Declare timeout and poll interval as package constants and pass them to
  `wait.New`. The constructor defaults non-positive values to ten minutes and
  five seconds, but production resources should state their own values so
  tests can assert elapsed waits against the same constants.
- Distinguish the two failure modes in callers with `errors.Is(err,
  wait.ErrTimeout)`. A cancelled operation wraps `context.Canceled` instead.
- A delete waiter depends on the backend reporting a removed object as HTTP
  404. Confirm that against the API before relying on it; the network OpenAPI
  documents no 404 response for any endpoint.
- Do not add backoff, retry, or transport concerns. The SDK owns HTTP retry.

## Resource Implementation Practices

- Model one backend API object per Terraform resource. Keep schemas close to
  the public API unless a deliberate Terraform UX improvement, such as a
  human-readable reference, justifies a translation.
- Use only public SDK facade types and methods. Let the SDK own transport,
  authentication, HTTP retry, serialization, and endpoint details.
- Use Terraform Framework types in models and handle `null` and `unknown`
  explicitly. Decode nested `types.Object` values into named models before
  constructing SDK requests.
- Append diagnostics after decoding configuration, plan, or state, and return
  before using those values when `Diagnostics.HasError()` is true.
- Mark an attribute `RequiresReplace` only when the backend cannot update it in
  place. Use the SDK update operation for mutable fields.
- Use `UseStateForUnknown` only for computed values known not to change during
  the relevant operation.
- On a create-only attribute that is `Optional` and `Computed`, pair
  `UseStateForUnknown` with `RequiresReplaceIfConfigured`, in that order. Any
  planned change marks every `Computed` attribute that is null in configuration
  as unknown, so a bare `RequiresReplace` sees unknown instead of the prior
  value and replaces the resource on unrelated updates. Cover both the
  unconfigured and the configured-change paths with plan-modifier tests.
- Keep provider-side validation structural and lifecycle-focused. Let the
  backend enforce business rules; do not duplicate volatile server policy.
- Refresh every available backend value during `Read`. Remove state when the
  backend reports the object is gone, and treat not-found during `Delete` as
  success.
- Preserve state/configuration/plan consistency and semantically equal values;
  do not hide real drift. Return actionable diagnostics and never leave
  unknown values in state.
- Honor `context.Context` in every SDK call and waiter. Add lifecycle waiters
  for asynchronous APIs as described in [Lifecycle Waiters](#lifecycle-waiters),
  but do not duplicate SDK HTTP retry behavior.
- Support import for every managed resource. Use a stable ID or a documented
  composite import key sufficient for `Read`.
- Mark sensitive schema attributes appropriately. Remember that `Sensitive`
  redacts CLI output but does not encrypt Terraform state.
- Preserve released state compatibility or provide an explicit state upgrade
  for a breaking schema evolution.
- Test schema-model compatibility, request field presence, response mapping,
  lookup cardinality, not-found handling, import, and replacement versus
  in-place update behavior. Keep unit tests offline and deterministic.
- Keep Framework resources in contract-first order: interface assertions;
  constants and types; constructor; `Metadata`, `Schema`, `Configure`,
  `ValidateConfig`, CRUD, and `ImportState`; private receiver helpers and
  waiters; then pure helpers. Apply the analogous order to data sources and the
  provider. `make lint` checks it; see
  [Three files per entity](#three-files-per-entity).

## Adding a New Resource

### Step-by-step Checklist

- [ ] 1. **Select the SDK service package**:
  - Create or reuse the matching service package under `internal/provider/<service>`
  - Import the corresponding public SDK package directly

- [ ] 2. In `<resource>_model.go`, define the **shared response model and populate function**:
  ```go
  type <Resource>Model struct {
      // tfsdk-tagged fields matching the API response
  }

  // populate<Resource>Model maps the API response to the shared model.
  // Called from both the resource (Create/Read/Update) and the data source (Read).
  func populate<Resource>Model(api *sdk.<Resource>Schema, m *<Resource>Model) {
      m.ID = types.StringValue(api.Id.String())
      // ...
  }
  ```
  > **Rule:** Reuse API response mapping between the resource and data source.
  > Resource-only creation inputs do not need to be forced into the data-source model.

- [ ] 3. Create `internal/provider/<service>/<resource>_resource.go`:
  - Define `<Resource>Resource` struct implementing `resource.Resource` and `resource.ResourceWithImportState`
  - Define a resource model matching its schema; alias or embed the shared response model where practical
  - Implement `Metadata()`, `Schema()`, `Configure()`, `Create()`, `Read()`, `Update()`, `Delete()`, `ImportState()` in that order
  - In `Configure()`, extract client from `req.ProviderData` with type assertion:
    ```go
    func (r *<Resource>Resource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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
    ```
  - Call the public SDK client directly; HTTP retry and transport resilience are configured by the SDK
  - Keep asynchronous lifecycle polling in the provider when Terraform must wait for a resource state (for example, server running or volume available). Build it with `wait.New[T]`; see [Lifecycle Waiters](#lifecycle-waiters). Pollers must honor `context.Context` and must not reimplement HTTP retry.
  - If the resource uses UUID fields, use `parse.UUIDString`

- [ ] 4. Create `internal/provider/<service>/<resource>_resource_test.go`:
  - Unit tests using mock HTTP responses
  - If tests need mock objects, define them in `test_helpers_test.go`

- [ ] 5. Create `examples/resources/<type>/resource.tf`, where `<type>` is the
  full type name from [Terraform Types](#terraform-types):
  - Minimal working example that `tfplugindocs` can use

- [ ] 6. Register in `internal/provider/provider.go`:
  - Add `<service>.New<Resource>Resource` to the `Resources()` return slice

- [ ] 7. Complete [Verification](#verification).

- [ ] 8. Commit with a clear message, e.g. `feat(network): add vpc resource`

## Adding a New Data Source

Similar to a resource, but simpler (read-only):

- [ ] 1. Create `internal/provider/<service>/<resource>_data_source.go`:
  - Define `<Resource>DataSource` implementing `datasource.DataSource`
  - Implement `Metadata()`, `Schema()`, `Configure()`, `Read()` in that order,
    followed by Read's lookup helpers
  - In `Configure()`, extract client from `req.ProviderData`:
    ```go
    func (d *<Resource>DataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
        if req.ProviderData == nil {
            return
        }
        data, ok := req.ProviderData.(*providerdata.Configured)
        if !ok {
            resp.Diagnostics.AddError("Unexpected provider data type", fmt.Sprintf("Expected *providerdata.Configured, got %T", req.ProviderData))
            return
        }
        d.client = data.Network
        d.project = data.Project
        d.projectID = data.ProjectID
    }
    ```

- [ ] 2. Create `internal/provider/<service>/<resource>_data_source_test.go`

- [ ] 3. Create `examples/data-sources/<type>/data-source.tf`, where `<type>`
  is the full type name from [Terraform Types](#terraform-types)

- [ ] 4. Register in `provider.go` → `DataSources()` method

- [ ] 5. Complete [Verification](#verification).

## Test Conventions

Use two complementary test layers. Unit tests prove provider behavior in
isolation, including the exact SDK contract. Real-API integration tests prove
the practitioner-visible Terraform lifecycle. Some behavior is intentionally
present in both layers: for example, a unit test proves that an omitted field
is absent from an SDK request, while an integration test proves that omission
has the documented effect in the backend.

### Unit Tests

Unit tests must be offline, deterministic, and safe to run through `make test`.
They must not require credentials or create infrastructure.

- Test schema-model compatibility and important attribute flags and plan
  modifiers.
- Test pure lookup selection, request construction, response-to-state mapping,
  normalization, and explicit `null`, `unknown`, omitted, empty, and zero-value
  handling.
- Use `httptest.NewServer` to verify SDK interaction, including HTTP method,
  path, scope and object IDs, request field presence, response mapping, and API
  error diagnostics.
- Test zero, one, and multiple lookup candidates and pagination where the
  lookup contract supports it.
- Test resource not-found behavior: `Read` removes state and `Delete` succeeds.
  Test that a data-source not-found response returns a diagnostic.
- Exercise time-dependent logic such as lifecycle waiters inside a
  `testing/synctest` bubble. Pass the production timeout and poll interval so
  the fake clock covers the real durations instead of shortened ones, and
  assert the elapsed wait.
- Test import state and whether schema changes cause replacement or an in-place
  update.
- Put resource tests in `<resource>_resource_test.go`, data-source tests in
  `<resource>_data_source_test.go`, and shared package-local fixtures in
  `test_helpers_test.go` when two or more test files need the same one. Order
  tests to follow the order of the code they cover.

Prefer testing pure request builders and mapping functions directly. Add
Framework or mock-server tests where behavior depends on Terraform decoding,
state operations, diagnostics, or the generated SDK's serialized request.

### Real-API Integration Tests

Real CRUD and Terraform lifecycle behavior is tested with native
`terraform test` files under `integration-tests/`.

- Name every run `run "<action>_<condition>_<expectedResult>"`. Use lower
  camel case within each segment and underscores only to separate the action,
  precondition, and observable result, for example
  `createServer_nameSetDescriptionOmittedAndBandwidth300_serverCreated` and
  `changeNameDescriptionAndBandwidth_existingServer_updatedInPlace`. Apply
  this to setup, lifecycle, lookup, plan, and import runs. Names must be unique
  within the file; include chain context in the condition when setup scenarios
  would otherwise collide.
- Start every `.tftest.hcl` file with a comment index, before variables and
  provider configuration. List every chain and every run in file order,
  including prerequisites and shared setup. Include each complete
  `run "<action>_<condition>_<expectedResult>" {` declaration on its own comment
  line so a reader can copy it into editor search and use Find Next to reach
  the block.
  Label each chain with its `state_key`, or shared fixture state when implicit;
  document shared setup dependencies and any separate plan-only import state.
  A short suite with a single lifecycle or lookup run still needs an index.
- Keep runs in each chain contiguous and in lifecycle order. Update the index,
  all `run.<name>.<output>` references, and the README entries described in
  [Documenting tests in the integration-tests README](#documenting-tests-in-the-integration-tests-readme)
  whenever a run is added, renamed, moved, or removed. Renaming runs must
  preserve assertions, fixture inputs, state keys, execution order, and
  parallel settings.

- Keep managed-resource and data-source coverage independently runnable. When
  an entity provides both, use fixtures at
  `integration-tests/fixtures/<entity>/resource/` and
  `integration-tests/fixtures/<entity>/datasource/`, with matching tests at
  `integration-tests/tests/<entity>_resource.tftest.hcl` and
  `integration-tests/tests/<entity>_data_source.tftest.hcl`.
- Resource tests cover create/read, every returned field, in-place update,
  omission versus explicit clearing for optional values, normalization and
  no-drift behavior, every `RequiresReplace` attribute, import followed by
  refresh, and cleanup. Verify that in-place updates preserve the stable ID and
  replacements change it.
- Cover import with a dedicated fixture module at
  `integration-tests/fixtures/<entity>/import/` that holds the `import` block,
  and reach it from a run with `command = plan` under its own `state_key`.
  Terraform 1.16 supports `import` blocks in modules loaded by test runs.
  Keep the block in the fixture because a `run` block cannot contain an
  `import` block. A plan-only run leaves the object with the state that
  created it. This prevents duplicate deletion during teardown. Create the object the
  run imports in the import chain itself instead of borrowing one another chain
  owns, so a failure stays inside one chain. Configure only the attributes whose
  omission would plan a change, and feed them from the creating run's state
  rather than literals: an import refresh has no configured representation to
  preserve, so a normalized variant of a `RequiresReplace` attribute plans a
  replacement instead of no change. Assert the attributes the refresh alone
  supplies — unconfigured `Optional` and `Computed` values and computed values —
  because the planned value of a configured attribute comes from configuration
  and cannot prove a refresh. Any planned change makes every computed attribute
  unknown and fails a plan-only run, which is what covers the configured ones.
  See `integration-tests/fixtures/placement_group/import/main.tf` and the import
  chain in `integration-tests/tests/placement_group_resource.tftest.hcl`.
- Data-source tests cover the direct-ID path, every supported lookup criterion
  in a combination that is guaranteed unique, common multi-filter lookups,
  full response mapping, and configured-value preservation. Cover provider
  diagnostic failures such as invalid criteria, zero matches, and ambiguity
  with offline unit tests because native `expect_failures` only handles
  Terraform custom-condition failures, not provider RPC diagnostics.
- Split a long resource lifecycle into independent chains rather than one serial
  sequence: give each chain its own `state_key`, declare `parallel = true` on
  every run, and give each chain its own resource names and address range. The
  chains then build their infrastructure concurrently, and a failing run only
  skips the remaining runs of its own chain instead of hiding unrelated
  coverage. Keep every `run.<name>` reference inside its chain; a cross-chain
  reference serializes the chains again. See
  `integration-tests/tests/server_resource.tftest.hcl`.
- When the backend cannot build a resource concurrently, create it once in a
  setup run that every chain shares rather than giving each chain its own. Put
  that run first and give it its own `state_key`: Terraform destroys state keys
  in reverse run order, so a shared resource created first is torn down last,
  after the chains have released what they built on top of it. For an existing
  shared resource, use a setup lookup instead. The server suite creates an
  isolated shared VPC under `network`, then tears it down after every server
  chain releases its subnets.
- Treat successful teardown as part of the test; a run with failed cleanup is
  not complete.
- Keep injected API failures, lookup ambiguity, invalid values, and exact
  request-shape assertions in unit tests. Integration tests should assert
  observable Terraform and backend behavior rather than implementation details.
- Use a dedicated test project. These tests create billable infrastructure and
  require credentials; never run them implicitly from `make test`.
- Follow `integration-tests/README.md` for initialization, provider development
  overrides, commands, and cleanup handling.

#### Documenting tests in the integration-tests README

`integration-tests/README.md` is the operator's entry point, and each
`.tftest.hcl` file is the source of truth for its own chains, runs, and
assertions. Keep that split.

- Add or update one coverage-table row per target a test file covers, naming
  the behaviors at the level of the [coverage matrix](#measuring-coverage).
  Record an inapplicable behavior as `N/A` with its reason in that row.
- Add to `## Prerequisites` only what an operator must arrange before running:
  tooling and access, catalog entries, quota, and objects that a required
  variable points to. Describe shared requirements by capability or category
  instead of copying concrete fixture defaults. Keep those values and their
  compatibility constraints in the test file, and add a nested bullet only for
  files that need extra pre-existing objects or quota. Commands an operator
  runs belong in `## Set up` and `## Run the tests` instead.
- Do not restate a test file's internals in the README: chain layout and state
  keys, per-run rationale, assertion strategy, API behavior a test established,
  or behavior left uncovered. Those belong in that file's comment index and
  per-run comments, and an uncovered behavior's reason belongs in its
  coverage-table row.
- Do not list a test file's fixed resource names. The shared prerequisites
  already require a project free of the names the test files create.
- Keep `## Structure` written against `<entity>` placeholders. Describe a
  fixture layout that a new entity introduces as the general pattern rather
  than as that entity's special case.

### Measuring Coverage

Do not combine unit and integration coverage into a single percentage: Go
statement coverage and Terraform lifecycle coverage measure different things.

Run unit coverage with:

```bash
make coverage
```

This instruments all provider packages under `internal/...` and writes
`coverage.out`. The `total` reported by `go tool cover -func=coverage.out` is:

```text
executed instrumented Go statements / all instrumented Go statements
```

The SDK module, generated documentation, examples, and Terraform fixtures
are outside this denominator. Review package and function results as well as
the total: a high aggregate must not hide untested CRUD, request-presence,
not-found, import, or diagnostic branches. New and changed behavior must be
covered even when the aggregate percentage does not decrease. The project does
not treat statement coverage alone as proof that the required behaviors work.

Measure integration coverage with an applicability matrix for each resource or
data source. Mark each applicable behavior below as covered only when a
`terraform test` run asserts it against the real API:

| Behavior | Applicable when |
| --- | --- |
| Create and refresh | Every managed resource |
| Data-source lookup | A data source is registered |
| In-place update | The resource has mutable attributes |
| Default, omit, and clear | Each supported optional-value contract |
| Replacement | Each `RequiresReplace` attribute |
| Import and refresh | Every managed resource |
| Cleanup | Every test that creates infrastructure |

Integration scenario coverage is
`covered applicable behaviors / all applicable behaviors`. The expectation is
100% of applicable lifecycle behaviors for a new resource. Record `N/A` with a
reason instead of removing a behavior from the denominator silently. This is a
review matrix, not a substitute for Go statement coverage.

## Verification

Run the standard checks from the repository root:

```bash
make fmt lint test build
VIETTELCLOUD_PROVIDER_ADDRESS=registry.terraform.io/viettelcloud-oss/viettelcloud make docs-check
```

When credentials are available, run the affected real-API test explicitly:

```bash
terraform -chdir=integration-tests test \
  -filter=tests/<entity>_<target>.tftest.hcl \
  -verbose
```

## Code Style

- Follow `golangci-lint` configuration in `.golangci.yml`
- Use `gofumpt` for strict formatting
- Use `goimports` with local module prefix
- Run `go fix ./...` before formatting to apply Go's suggested source rewrites
  and keep code aligned with current language and standard-library idioms
- No comments unless the WHY is non-obvious; see [Comments](#comments) for
  where one goes and how it should read
- Three files per entity — model, resource, data source — with Framework entry
  points in contract-first order; see
  [Three files per entity](#three-files-per-entity). File length is not a reason
  to split
- Use `types.String`, `types.Int64`, etc. from Terraform Plugin Framework (never raw Go types in models)

## PR Checklist

When you create a GitLab merge request, select `default` from the description
templates. The template is
[`.gitlab/merge_request_templates/default.md`](.gitlab/merge_request_templates/default.md).
Use the template as an outline.
Expand applicable sections to describe the change and its tests.
Delete sections that do not apply.
For tests that did not run, record the reason.

The description has four parts: a summary of what the MR does, the rationale
for the change and its approach, notes that a reviewer needs, and a test plan.
It shows only the latest state of the MR. Record changes made during review in
the discussions, not in the description. Do not include your own assessment
of compatibility or rule compliance, because the reviewer makes that
assessment.

Before submitting a merge request:

- [ ] The [verification](#verification) commands pass.
- [ ] Affected real-API tests pass, or any test that could not be run is
      documented.
- [ ] Schemas, examples, generated docs, `README.md`,
      `integration-tests/README.md`, and this guide reflect
      behavior or workflow changes where applicable.
- [ ] Commit messages follow [Conventional Commits](https://www.conventionalcommits.org/)
