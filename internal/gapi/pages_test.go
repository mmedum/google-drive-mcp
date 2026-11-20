package gapi_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/time/rate"

	"github.com/mmedum/google-drive-mcp/v3/internal/gapi"
)

// pagedServer answers every request with the page its token names; a
// request with no token gets start.
func pagedServer(t *testing.T, start string, pages map[string]string) (*gapi.Client, *int) {
	t.Helper()
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		token := r.URL.Query().Get("pageToken")
		if token == "" {
			token = start
		}
		_, _ = w.Write([]byte(pages[token]))
	}))
	t.Cleanup(srv.Close)
	return gapi.New(oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "tok"}), gapi.Options{
		BaseURL:      srv.URL,
		ReadLimiter:  rate.NewLimiter(rate.Inf, 1),
		WriteLimiter: rate.NewLimiter(rate.Inf, 1),
		Timeout:      5 * time.Second,
		AllowURL:     func(*url.URL) bool { return true },
	}), &requests
}

// Only a missing token ends a listing, so an item on the page after an
// empty one is still read.
func TestListingsFollowTheTokenPastAnEmptyPage(t *testing.T) {
	for _, tc := range []struct {
		name, key string
		list      func(context.Context, *gapi.Client) (int, error)
	}{
		{"access requests", "accessProposals", func(ctx context.Context, c *gapi.Client) (int, error) {
			got, err := c.ListAccessProposals(ctx, "id-budget-fixture")
			return len(got), err
		}},
		{"permissions", "permissions", func(ctx context.Context, c *gapi.Client) (int, error) {
			got, err := c.ListPermissions(ctx, "id-budget-fixture")
			return len(got), err
		}},
		{"revisions", "revisions", func(ctx context.Context, c *gapi.Client) (int, error) {
			got, err := c.ListRevisions(ctx, "id-budget-fixture", "id")
			return len(got), err
		}},
		{"labels", "labels", func(ctx context.Context, c *gapi.Client) (int, error) {
			got, err := c.AllFileLabels(ctx, "id-budget-fixture")
			return len(got), err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := pagedServer(t, "", map[string]string{
				"":      `{"nextPageToken":"tok-1"}`,
				"tok-1": `{"` + tc.key + `":[{"id":"id-late-fixture"}]}`,
			})
			if got, err := tc.list(t.Context(), c); err != nil || got != 1 {
				t.Errorf("listed %d, %v; want the 1 item on the page after the empty one", got, err)
			}
		})
	}
}

func TestATokenThatNeverEndsIsBounded(t *testing.T) {
	c, requests := pagedServer(t, "tok-endless", map[string]string{
		"tok-endless": `{"nextPageToken":"tok-endless"}`,
	})
	got, err := c.ListPermissions(t.Context(), "id-budget-fixture")
	if !errors.Is(err, gapi.ErrUnexpected) || got != nil {
		t.Errorf("ListPermissions = %v, %v; want nil, an unexpected-response error", got, err)
	}
	if *requests != 100 {
		t.Errorf("a token that never ends was followed for %d requests, want the bound of 100", *requests)
	}
}
