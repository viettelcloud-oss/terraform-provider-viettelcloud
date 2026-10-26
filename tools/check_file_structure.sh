#!/usr/bin/env bash
# Enforces the leaf-package rules and the two navigation rules for service
# packages.
#
# 1. Framework entry points appear in contract-first order, so a reader scrolling
#    a resource always meets Metadata, Schema, Configure, then CRUD, then
#    ImportState. Helpers, types and vars are not ranked; only the relative order
#    of the entry points is checked.
# 2. A file whose name is not one of the standard forms documents what it owns.
#
# A service package is identified by containing a resource or data source, so
# the rules follow new services automatically.
set -euo pipefail

SUFFIXES="model resource data_source test_helpers"
RESOURCE_ORDER="Metadata Schema Configure ValidateConfig Create Read Update Delete ImportState"
DATASOURCE_ORDER="Metadata Schema Configure Read"

service_dirs() {
	find internal/provider -mindepth 2 -maxdepth 2 \
		\( -name '*_resource.go' -o -name '*_data_source.go' \) -printf '%h\n' | sort -u
}

# Reports the entry points a file defines, in the order they appear.
entry_points() {
	local file=$1 order=$2
	grep -oE "^func \([a-zA-Z] \*[A-Za-z0-9_]+\) [A-Za-z0-9_]+" "$file" |
		awk '{print $NF}' |
		grep -xF -f <(printf '%s\n' $order) || true
}

fail=0

# --- rule 0: lookup packages stay leaves ------------------------------------
# A <service>/lookup package may import internal/finder and nothing else from
# this repository. That is what lets any service resolve any other service's
# entities without an import cycle: service -> other/lookup edges can never
# close, because lookup packages point at no service.
for file in internal/provider/*/lookup/*.go; do
	[ -e "$file" ] || continue
	bad=$(grep -oE '"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/[a-z/]*"' "$file" |
		grep -v '"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/finder"' || true)
	if [ -n "$bad" ]; then
		echo "$file: a lookup package may only import internal/finder from this repo" >&2
		echo "$bad" | sed 's/^/    found: /' >&2
		echo "    importing a service package here creates a cycle the moment that" >&2
		echo "    service resolves one of this service's entities" >&2
		fail=1
	fi
done

# --- rule 0b: catalog selector packages stay leaves --------------------------
# A <service>/selector package may import internal/selector and its own
# service's lookup package, and nothing else from this repository. Server and
# blockstorage each import the other's selector, so a service import here
# would close a cycle.
for file in internal/provider/*/selector/*.go; do
	[ -e "$file" ] || continue
	service=$(basename "$(dirname "$(dirname "$file")")")
	bad=$(grep -oE '"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/[a-z/]*"' "$file" |
		grep -v -e '"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/selector"' \
			-e "\"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/$service/lookup\"" || true)
	if [ -n "$bad" ]; then
		echo "$file: a selector package may only import internal/selector and its own lookup" >&2
		echo "$bad" | sed 's/^/    found: /' >&2
		echo "    server and blockstorage import each other's selectors, so a service" >&2
		echo "    import here creates a cycle" >&2
		fail=1
	fi
done

# --- rule 1: contract-first order -------------------------------------------
for dir in $(service_dirs); do
	for file in "$dir"/*.go; do
		case "$(basename "$file")" in
		*_test.go) continue ;;
		*_data_source.go) order=$DATASOURCE_ORDER ;;
		*_resource.go) order=$RESOURCE_ORDER ;;
		*) continue ;;
		esac

		actual=$(entry_points "$file" "$order")
		[ -n "$actual" ] || continue
		expected=$(printf '%s\n' $order | grep -xF -f <(printf '%s\n' "$actual") || true)
		if [ "$actual" != "$expected" ]; then
			echo "$file: Framework entry points are out of contract-first order" >&2
			echo "    found:    $(echo "$actual" | tr '\n' ' ')" >&2
			echo "    expected: $(echo "$expected" | tr '\n' ' ')" >&2
			fail=1
		fi
	done
done

# --- rule 2: non-standard files document themselves -------------------------
for dir in $(service_dirs); do
	for file in "$dir"/*.go; do
		base="$(basename "$file" .go)"
		base="${base%_test}.go"
		for suffix in $SUFFIXES; do
			case "$base" in
			*_"$suffix".go | "$suffix".go) continue 2 ;;
			esac
		done

		if ! awk '
			/^package /        { seen = 1; next }
			!seen              { next }
			/^import \(/       { inimport = 1; next }
			inimport && /^\)/  { inimport = 0; next }
			inimport           { next }
			/^import /         { next }
			/^\/\//            { found = 1; exit }
			/^(func|type|var|const)[ \t(]/ { exit }
			END                { exit !found }
		' "$file"; then
			echo "$file: non-standard file name needs a docstring after the package clause" >&2
			echo "    name the file and say what it owns; standard suffixes are: ${SUFFIXES// /, }" >&2
			fail=1
		fi
	done
done

if [ "$fail" -ne 0 ]; then
	exit 1
fi
echo "file-structure: ok"
