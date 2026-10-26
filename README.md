# Viettel Cloud Terraform Provider

Terraform provider for managing infrastructure resources on the Viettel Cloud platform.

## Supported Resources

| Service | Resource | Data source |
|---------|----------|-------------|
| Block Storage | `viettelcloud_volume`, `viettelcloud_volume_backup`, `viettelcloud_volume_snapshot` | `viettelcloud_volume`, `viettelcloud_volume_backup`, `viettelcloud_volume_snapshot` |
| Network | `viettelcloud_security_group` | `viettelcloud_security_group` |
| Network | `viettelcloud_private_ip` | `viettelcloud_private_ip` |
| Network | `viettelcloud_elastic_ip` | `viettelcloud_elastic_ip` |
| Network | `viettelcloud_subnet` | `viettelcloud_subnet` |
| Network | `viettelcloud_vpc` | `viettelcloud_vpc` |
| Server | `viettelcloud_custom_image` | `viettelcloud_custom_image` |
| Server | `viettelcloud_placement_group` | `viettelcloud_placement_group` |
| Server | `viettelcloud_server` | — |
| Server | `viettelcloud_key_pair` | `viettelcloud_key_pair` |

See the generated [provider documentation](docs/index.md) and the
[`examples/`](examples/) directory for configuration details.

Server `data_volumes` accepts `kind = "new"` to create a volume and
`kind = "existing"` with an `id` to attach an existing volume.
An omitted `kind` defaults to `new` for earlier configurations.
Adding or removing attachments keeps the server in place.
Omitting `data_volumes` preserves attachments. An empty list detaches them.

After import, configure retained data volumes as `existing` with their IDs.
See the [server import guide](docs/resources/server.md#import).

## Using the Provider

Terraform 1.16 or later is required.

```terraform
terraform {
  required_providers {
    viettelcloud = {
      source  = "viettelcloud-oss/viettelcloud"
      version = "~> 0.1.1"
    }
  }
}

provider "viettelcloud" {
  endpoint   = "<api-endpoint>"
  token      = "<personal-access-token>"
  project_id = "<project-id-or-slug>"
}
```

Get `<api-endpoint>` and a `<personal-access-token>` (PAT) from the Viettel Cloud
portal. Pass the token through an environment variable, not a committed file.

`project_id` accepts a project UUID or a project slug.

Some nested fields appear as Optional because Terraform also stores backend
values in them. When you configure their parent object, supply every field
that its description marks as required. For example, `zone` requires `name`,
and `key_pair` requires `id`.

Do not commit credentials, `.tfvars` files, or state files.

```bash
terraform init
terraform plan
```

## Development

Development additionally requires Go 1.26.8 or later, Git, and optionally
pre-commit. Go downloads the SDK module
`github.com/viettelcloud-oss/sdks/go` at the version pinned in `go.mod`.

```bash
git clone https://github.com/viettelcloud-oss/terraform-provider-viettelcloud
cd terraform-provider-viettelcloud

# Install developer tools
make tools

# Build, lint, and test
make fmt lint test build
```

Maintainers who need to upgrade the SDK version should follow the SDK update
workflow in [`CONTRIBUTING.md`](CONTRIBUTING.md#importing-the-sdk).

For the merge request description template and submission workflow, see the
[PR Checklist](CONTRIBUTING.md#pr-checklist).

### Run locally

Add a development override to the Terraform CLI configuration file. The file
is `~/.terraformrc` on Linux or macOS, and `%APPDATA%\terraform.rc` on
Windows. Set the override path to the absolute path of the directory that
holds the built binary. `make build` writes the binary to the repository root:

```hcl
provider_installation {
  dev_overrides {
    "registry.terraform.io/viettelcloud-oss/viettelcloud" = "/absolute/path/to/terraform-provider-viettelcloud"
  }

  direct {}
}
```

Use the configuration from [Using the Provider](#using-the-provider). Keep its
provider source address unchanged so Terraform matches the development
override.

```bash
make build
export VIETTELCLOUD_PROVIDER_ADDRESS="registry.terraform.io/viettelcloud-oss/viettelcloud"

terraform plan
terraform apply
```

Run `make build` again after changing provider code. The development override
warning from Terraform is expected.

### Build for other platforms

`make build` builds for the host operating system and architecture. To build
for Linux, macOS, and Windows from one machine, run:

```bash
make build-all
```

The binaries go to `dist/<os>_<arch>/`:

```
dist/
├── darwin_amd64/terraform-provider-viettelcloud        # macOS, Intel
├── darwin_arm64/terraform-provider-viettelcloud        # macOS, Apple silicon
├── linux_amd64/terraform-provider-viettelcloud
├── linux_arm64/terraform-provider-viettelcloud
├── windows_amd64/terraform-provider-viettelcloud.exe
└── windows_arm64/terraform-provider-viettelcloud.exe
```

To build one platform, give its `<os>_<arch>` pair:

```bash
make build-windows_amd64
```

To build a different set, set `PLATFORMS` to `GOOS/GOARCH` pairs:

```bash
make build-all PLATFORMS="windows/amd64 darwin/arm64"
```

The builds use `CGO_ENABLED=0`, so you do not need a C toolchain for the target
platform. Copy a binary to the target machine and point the development
override at its directory.

## Project Layout

```
terraform-provider-viettelcloud/
├── main.go                          # Entry point; calls providerserver.Serve
├── go.mod / go.sum                  # Go module dependencies
├── Makefile                         # Build, test, lint, fmt, docs targets
├── .golangci.yml                    # Linter configuration (golangci-lint v2)
├── .pre-commit-config.yaml          # Git pre-commit hooks
├── .editorconfig                    # Editor formatting rules
├── README.md                        # This file
├── CONTRIBUTING.md                  # Contribution guide & checklists
├── RELEASING.md                     # Release process (semantic-release)
├── .releaserc                       # semantic-release plugin configuration
├── internal/
│   ├── provider/
│   │   ├── provider.go              # Provider definition, Configure, registration
│   │   ├── provider_test.go         # Provider-level tests
│   │   ├── providerdata/
│   │   │   └── configured.go        # Configured SDK clients and project context
│   │   ├── project/
│   │   │   └── lookup/              # region, zone and project finders
│   │   ├── network/                 # Network resources and data sources
│   │   │   ├── lookup/              # Finders for network-owned entities
│   │   │   │   ├── vpc.go
│   │   │   │   ├── vpc_test.go
│   │   │   │   ├── <entity>.go
│   │   │   │   └── <entity>_test.go
│   │   │   ├── vpc_model.go
│   │   │   ├── vpc_resource.go
│   │   │   ├── vpc_resource_test.go
│   │   │   ├── vpc_data_source.go
│   │   │   ├── vpc_data_source_test.go
│   │   │   ├── <resource>_model.go
│   │   │   ├── <resource>_resource.go
│   │   │   ├── <resource>_resource_test.go
│   │   │   ├── <resource>_data_source.go
│   │   │   └── <resource>_data_source_test.go
│   │   └── <service>/               # Other SDK service resources and data sources
│   ├── finder/                      # The finder pattern: Interface, Collect, ExactlyOne
│   ├── compare/                     # Semantic equality of Terraform values
│   ├── parse/                       # Terraform values to Go and SDK types
│   └── wait/                        # Asynchronous lifecycle polling
├── examples/                        # Consumed by tfplugindocs
│   ├── provider/provider.tf
│   ├── resources/<type>/resource.tf
│   └── data-sources/<type>/data-source.tf
├── docs/                            # Generated by tfplugindocs (do not hand-edit)
└── tools/
    ├── go.mod                       # Go tool directives and pinned versions
    └── go.sum                       # Tool dependency checksums
```

### Key Design Decisions

- **`internal/provider/<service>/`** — resources, data sources, models, and tests grouped by SDK service.
- **`providerdata/`** — configured public SDK clients and shared project context passed through `ProviderData`.
- **`<service>/lookup/`** — finders for entities owned by that SDK service.
  Shared finder mechanics live in `internal/finder/`.
- **Three files per entity** — model, resource, and data source. The model file
  owns all API-to-state mapping. Framework entry points use a consistent order.
- **Reusable helpers** — one purpose per package under `internal/`: `compare`,
  `parse`, `wait`, and `finder`. Names avoid stuttering, such as
  `parse.UUIDString` and `wait.New`.
- **SDK dependency** — clients from the separate `github.com/viettelcloud-oss/sdks/go` module are used directly. Retry is configured through SDK options in `provider.Configure`.
- **`examples/`** is not optional — `tfplugindocs` reads from this exact structure to generate docs.
- **Developer tools** use a separate Go module with `tool` directives, keeping their dependency graph isolated from the Provider.

## Makefile Targets

| Target | Description |
|--------|-------------|
| `make sdk-update` | Update the SDK to the newest release. For a specific version, add `SDK_VERSION=vX.Y.Z` |
| `make build` | Build the provider binary for the host platform with version info |
| `make build-all` | Cross-compile for every platform in `PLATFORMS` (Linux, macOS, Windows) into `dist/` |
| `make build-<os>_<arch>` | Cross-compile one platform, for example `make build-darwin_arm64` |
| `make install` | Install into the local Terraform plugin directory (`~/.terraform.d/plugins`, Linux and macOS layout) |
| `make test` | Run unit tests with race detector |
| `make lint` | Run golangci-lint |
| `make fix` | Apply suggested fixes from the Go fix tool (runs automatically before `make fmt`) |
| `make fmt` | Apply Go fixes, then format with gofmt, gofumpt, goimports |
| `make tools` | Install developer tools at the versions pinned in `tools/go.mod` |
| `make docs` | Generate provider documentation |
| `make docs-check` | Verify docs are up to date |
| `make clean` | Remove build artifacts, including `dist/` |

`make docs` and `make docs-check` serve the provider to Terraform, so they need
the provider address the same way CI passes it:

```bash
VIETTELCLOUD_PROVIDER_ADDRESS=registry.terraform.io/viettelcloud-oss/viettelcloud make docs-check
```

## Pre-commit Hooks

```bash
pip install pre-commit
pre-commit install
```

Hooks run on `git commit`: end-of-file-fixer, trailing-whitespace, go-fmt, go-lint, go-test.

## Naming Conventions

| Item | Convention | Example |
|------|-----------|---------|
| Binary | `terraform-provider-<name>` | `terraform-provider-viettelcloud` |
| Provider type | `<name>` | `viettelcloud` |
| Resource file | `<resource>_resource.go` | `vpc_resource.go` |
| Resource struct | `<Resource>Resource` | `VPCResource` |
| Resource model | `<Resource>ResourceModel` | `VPCResourceModel` |
| Shared model | `<Resource>Model` (when schemas share response fields) | `VPCModel` |
| Shared populate fn | `populate<Resource>Model(api, *<Resource>Model)` | `populateVPCModel(...)` |
| Data source file | `<resource>_data_source.go` | `vpc_data_source.go` |
| Data source model | `<Resource>DataSourceModel` | `VPCDataSourceModel` |
| Terraform type | `viettelcloud_<prefix>_<resource>`, see [Terraform Types](CONTRIBUTING.md#terraform-types) | `viettelcloud_lb_listener` |

## Adding a New Resource

1. Add the response model and shared mapping to `internal/provider/<service>/<resource>_model.go`. Keep creation-only fields in the resource model.
2. Create `internal/provider/<service>/<resource>_resource.go` and implement schema and CRUD using the public SDK client.
3. Create `internal/provider/<service>/<resource>_resource_test.go` with unit tests.
4. Create `examples/resources/<type>/resource.tf`, where `<type>` is the full Terraform type name.
5. Register in `provider.go` → `Resources()`.
6. Run `make fmt lint test build`.

See [CONTRIBUTING.md](CONTRIBUTING.md) for the detailed checklist.

## Verification

Run these checks before submitting a change:

- **format and lint** —
  `make tools && make fmt && git diff --exit-code && make lint`
- **test and build** — `make test && make build`
- **generated docs** — `make docs-check`
