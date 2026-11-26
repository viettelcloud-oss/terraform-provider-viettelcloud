package lookup

import (
	"context"
	"encoding/json"
	"iter"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/viettelcloud-oss/sdks/go/core"
	dbaassdk "github.com/viettelcloud-oss/sdks/go/dbaas"
)

type fakeInstanceLister struct {
	items  []*dbaassdk.DBInstanceSchema
	err    error
	params dbaassdk.ListDbInstancesParams
}

func (f *fakeInstanceLister) ListDbInstancesIter(_ context.Context, params dbaassdk.ListDbInstancesParams) iter.Seq2[*dbaassdk.DBInstanceSchema, error] {
	f.params = params
	return fakeSeq(f.items, f.err)
}

func TestInstanceFinder(t *testing.T) {
	t.Parallel()
	projectID := core.UUID{9}
	match := &dbaassdk.DBInstanceSchema{Id: core.UUID{1}, Name: "orders"}
	prefix := &dbaassdk.DBInstanceSchema{Id: core.UUID{2}, Name: "orders-replica"}
	lister := &fakeInstanceLister{}
	assertCardinality(t, match, prefix, func(items []*dbaassdk.DBInstanceSchema, err error) (dbaassdk.DBInstanceSchema, error) {
		lister.items, lister.err = items, err
		return NewInstanceFinder(lister, projectID).Resolve(background(), InstanceFilter{Name: new(" orders ")})
	}, func(instance dbaassdk.DBInstanceSchema) string { return instance.Id.String() })
	if lister.params.ProjectID != projectID || lister.params.Name == nil || *lister.params.Name != "orders" {
		t.Fatalf("the trimmed name and project must reach the backend: %#v", lister.params)
	}
}

func TestInstanceFinderReadsEveryPage(t *testing.T) {
	t.Parallel()
	pages := map[string][]dbaassdk.DBInstanceSchema{
		"1": {{Id: core.UUID{1}, Name: "orders-a"}},
		"2": {{Id: core.UUID{2}, Name: "orders"}},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/v2/dbaas/db-instances/" || req.URL.Query().Get("name") != "orders" {
			t.Errorf("unexpected request %s", req.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(dbaassdk.PagedDBInstanceSchema{Count: 2, Results: pages[req.URL.Query().Get("page_number")]})
	}))
	defer server.Close()
	client, err := dbaassdk.NewClient(server.URL, dbaassdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatal(err)
	}
	got, err := NewInstanceFinder(client, core.UUID{9}).Resolve(background(), InstanceFilter{Name: new("orders")})
	if err != nil || got.Id != (core.UUID{2}) {
		t.Fatalf("the exact match on the second page was not found: %v %v", got.Id, err)
	}
}
