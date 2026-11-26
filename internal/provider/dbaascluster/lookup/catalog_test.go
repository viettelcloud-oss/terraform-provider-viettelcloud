package lookup

import (
	"context"
	"errors"
	"iter"
	"strings"
	"testing"

	"github.com/viettelcloud-oss/sdks/go/core"
	dbaasclustersdk "github.com/viettelcloud-oss/sdks/go/dbaascluster"
)

type fakeCatalogLister struct {
	versions []dbaasclustersdk.DBClusterEngineVersionSchema
	packages []dbaasclustersdk.DBClusterResourcePackageSchema
	classes  []dbaasclustersdk.DBClusterStorageClassSchema
	err      error

	versionParams dbaasclustersdk.ListDbClusterEngineVersionsParams
	packageParams dbaasclustersdk.ListDbClusterResourcePackagesParams
	classParams   dbaasclustersdk.ListDbClusterStorageClassesParams
}

func sequence[T any](items []T, err error) iter.Seq2[*T, error] {
	return func(yield func(*T, error) bool) {
		for i := range items {
			if !yield(&items[i], nil) {
				return
			}
		}
		if err != nil {
			yield(nil, err)
		}
	}
}

func (f *fakeCatalogLister) ListDbClusterEngineVersionsIter(_ context.Context, params dbaasclustersdk.ListDbClusterEngineVersionsParams) iter.Seq2[*dbaasclustersdk.DBClusterEngineVersionSchema, error] {
	f.versionParams = params
	return sequence(f.versions, f.err)
}

func (f *fakeCatalogLister) ListDbClusterResourcePackagesIter(_ context.Context, params dbaasclustersdk.ListDbClusterResourcePackagesParams) iter.Seq2[*dbaasclustersdk.DBClusterResourcePackageSchema, error] {
	f.packageParams = params
	return sequence(f.packages, f.err)
}

func (f *fakeCatalogLister) ListDbClusterStorageClassesIter(_ context.Context, params dbaasclustersdk.ListDbClusterStorageClassesParams) iter.Seq2[*dbaasclustersdk.DBClusterStorageClassSchema, error] {
	f.classParams = params
	return sequence(f.classes, f.err)
}

func TestEngineVersionFinderFiltersEngineVersionAndActivity(t *testing.T) {
	t.Parallel()

	lister := &fakeCatalogLister{versions: []dbaasclustersdk.DBClusterEngineVersionSchema{
		{Id: core.UUID{1}, Engine: dbaasclustersdk.DBClusterEngineTypeMongodb, Version: "7.0", IsActive: true},
		{Id: core.UUID{2}, Engine: dbaasclustersdk.DBClusterEngineTypeMongodb, Version: "7.0", IsActive: false},
		{Id: core.UUID{3}, Engine: dbaasclustersdk.DBClusterEngineTypeMongodb, Version: "7.0.1", IsActive: true},
		{Id: core.UUID{4}, Engine: dbaasclustersdk.DBClusterEngineTypeOpensearch, Version: "7.0", IsActive: true},
	}}
	engine, version, active := "mongodb", "7.0", true
	got, err := NewEngineVersionFinder(lister).Resolve(context.Background(), EngineVersionFilter{Engine: &engine, Version: &version, IsActive: &active})
	if err != nil || got.Id != (core.UUID{1}) {
		t.Fatalf("resolve: %v %v", got.Id, err)
	}
	if lister.versionParams.Engine == nil || *lister.versionParams.Engine != dbaasclustersdk.DBClusterEngineTypeMongodb ||
		lister.versionParams.IsActive == nil || !*lister.versionParams.IsActive {
		t.Fatalf("the engine and activity must reach the backend: %#v", lister.versionParams)
	}

	// Without the activity filter the inactive row is a second candidate.
	if _, err := NewEngineVersionFinder(lister).Resolve(context.Background(), EngineVersionFilter{Engine: &engine, Version: &version}); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("expected ambiguity, got %v", err)
	}
	if lister.versionParams.IsActive != nil {
		t.Fatalf("an unset filter must not be sent: %#v", lister.versionParams)
	}
}

func TestResourcePackageAndStorageClassFindersMatchNamesExactly(t *testing.T) {
	t.Parallel()

	lister := &fakeCatalogLister{
		packages: []dbaasclustersdk.DBClusterResourcePackageSchema{{Id: core.UUID{1}, Name: "2c4g"}, {Id: core.UUID{2}, Name: "2c4g-extended"}, {Id: core.UUID{3}, Name: "2C4G"}},
		classes:  []dbaasclustersdk.DBClusterStorageClassSchema{{Id: core.UUID{4}, Name: "ssd"}, {Id: core.UUID{5}, Name: "ssd-encrypted"}},
	}
	name := " 2c4g "
	pkg, err := NewResourcePackageFinder(lister).Resolve(context.Background(), ResourcePackageFilter{Name: &name})
	if err != nil || pkg.Id != (core.UUID{1}) || lister.packageParams.Name == nil || *lister.packageParams.Name != "2c4g" {
		t.Fatalf("package: %v %v params=%#v", pkg.Id, err, lister.packageParams)
	}
	className := "ssd"
	class, err := NewStorageClassFinder(lister).Resolve(context.Background(), StorageClassFilter{Name: &className})
	if err != nil || class.Id != (core.UUID{4}) {
		t.Fatalf("class: %v %v", class.Id, err)
	}
}

func TestResourcePackageAndStorageClassFindersRejectAmbiguousNames(t *testing.T) {
	t.Parallel()

	lister := &fakeCatalogLister{
		packages: []dbaasclustersdk.DBClusterResourcePackageSchema{{Id: core.UUID{1}, Name: "2c4g"}, {Id: core.UUID{2}, Name: "2c4g"}},
		classes:  []dbaasclustersdk.DBClusterStorageClassSchema{{Id: core.UUID{4}, Name: "ssd"}, {Id: core.UUID{5}, Name: "ssd"}},
	}
	name, className := "2c4g", "ssd"
	if _, err := NewResourcePackageFinder(lister).Resolve(context.Background(), ResourcePackageFilter{Name: &name}); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("package: err = %v, want an ambiguity error", err)
	}
	if _, err := NewStorageClassFinder(lister).Resolve(context.Background(), StorageClassFilter{Name: &className}); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("class: err = %v, want an ambiguity error", err)
	}
}

func TestCatalogFindersReportZeroAndErrors(t *testing.T) {
	t.Parallel()

	missing := "absent"
	empty := &fakeCatalogLister{}
	if _, err := NewResourcePackageFinder(empty).Resolve(context.Background(), ResourcePackageFilter{Name: &missing}); err == nil || !strings.Contains(err.Error(), "was not found") {
		t.Errorf("package: %v", err)
	}
	if _, err := NewStorageClassFinder(empty).Resolve(context.Background(), StorageClassFilter{Name: &missing}); err == nil || !strings.Contains(err.Error(), "was not found") {
		t.Errorf("class: %v", err)
	}
	if _, err := NewEngineVersionFinder(empty).Resolve(context.Background(), EngineVersionFilter{Version: &missing}); err == nil || !strings.Contains(err.Error(), "was not found") {
		t.Errorf("version: %v", err)
	}

	failing := &fakeCatalogLister{err: errors.New("boom")}
	if _, err := NewResourcePackageFinder(failing).Find(context.Background(), ResourcePackageFilter{}); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("package error: %v", err)
	}
	if _, err := NewStorageClassFinder(failing).Find(context.Background(), StorageClassFilter{}); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("class error: %v", err)
	}
	if _, err := NewEngineVersionFinder(failing).Find(context.Background(), EngineVersionFilter{}); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("version error: %v", err)
	}
}
