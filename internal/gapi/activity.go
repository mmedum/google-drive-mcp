package gapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/mmedum/google-drive-mcp/v3/internal/gdrive"
)

// The Drive Activity API is a separate service on its own host, with its
// own scope and its own enablement in the Cloud project. It is not part
// of Drive v3, and its one method is activity.query. Checked by the live
// step "the scratch folder's activity is read recursively".
// https://developers.google.com/workspace/drive/activity/v2/reference/rest/v2/activity/query

// Drive Activity's page sizes. The API documents no maximum, only that
// pageSize is the minimum number it will try to return, so this is the
// ceiling this server imposes rather than one Google states.
const (
	DefaultActivityPageSize = 25
	MaxActivityPageSize     = 100
)

// QueryActivity returns one page of activity.
//
// The request is a POST that only reads — the query does not fit in a
// URL — so it is marked `reads`. Derived from the method alone it would
// spend from the write budget and would not be retried after a dropped
// connection, and both are wrong for a listing that changes nothing.
func (c *Client) QueryActivity(ctx context.Context, q gdrive.ActivityQuery) (*gdrive.ActivityResponse, error) {
	if q.PageSize <= 0 {
		q.PageSize = DefaultActivityPageSize
	}
	if q.PageSize > MaxActivityPageSize {
		q.PageSize = MaxActivityPageSize
	}
	payload, err := json.Marshal(q)
	if err != nil {
		return nil, fmt.Errorf("%w: encode activity query: %w", ErrUnexpected, err)
	}
	body, err := c.do(ctx, request{
		method: http.MethodPost,
		url:    c.activityBase + "/activity:query",
		body:   payload,
		reads:  true,
	})
	if err != nil {
		return nil, err
	}
	var out gdrive.ActivityResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("%w: decode activity: %w", ErrUnexpected, err)
	}
	return &out, nil
}
