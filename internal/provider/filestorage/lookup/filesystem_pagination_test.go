package lookup

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/viettelcloud-oss/sdks/go/core"
	filestoragepkg "github.com/viettelcloud-oss/sdks/go/filestorage"
)

type pageRoundTripFunc func(*http.Request) (*http.Response, error)

func (f pageRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// TestFilesystemFinderReadsEveryPage checks that the finder walks every list
// page through the SDK iterator and finds a match that only a later page holds.
func TestFilesystemFinderReadsEveryPage(t *testing.T) {
	t.Parallel()
	projectID := core.UUID{9}
	subnetID := core.UUID{5}
	wantID := core.UUID{1}
	name := "my-fs"
	pages := map[string]filestoragepkg.PagedFilesystemSchema{
		"1": {Count: 2, Results: []filestoragepkg.FilesystemSchema{testFilesystem(core.UUID{2}, "my-fs-old", "zone-a", subnetID)}},
		"2": {Count: 2, Results: []filestoragepkg.FilesystemSchema{testFilesystem(wantID, name, "zone-a", subnetID)}},
	}
	var requested []string
	client, err := filestoragepkg.NewClient(
		"https://filestorage.test",
		filestoragepkg.WithHTTPClient(&http.Client{Transport: pageRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			page := req.URL.Query().Get("page_number")
			requested = append(requested, page)
			if got := req.URL.Query().Get("name"); got != name {
				t.Errorf("page %s: name query = %q, want %q", page, got, name)
			}
			body, ok := pages[page]
			if !ok {
				t.Errorf("unexpected page request: %s", req.URL)
			}
			raw, _ := json.Marshal(body)
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(string(raw))),
				Request:    req,
			}, nil
		})}),
		filestoragepkg.WithRetry(core.NoRetry()),
	)
	if err != nil {
		t.Fatalf("create file storage client: %v", err)
	}

	got, err := NewFilesystemFinder(client, projectID).Resolve(context.Background(), FilesystemFilter{Name: &name})
	if err != nil || got.Id != wantID {
		t.Fatalf("resolve across pages: got=%#v err=%v", got, err)
	}
	if strings.Join(requested, ",") != "1,2" {
		t.Fatalf("requested pages = %v, want [1 2]", requested)
	}
}
