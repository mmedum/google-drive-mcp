package gapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/mmedum/google-drive-mcp/internal/gdrive"
)

// DriveFields is what a shared-drive listing asks for.
const DriveFields = "id,name,createdTime,hidden,colorRgb,capabilities,restrictions,orgUnitId"

// ListDrivesOptions tune a shared-drive listing.
type ListDrivesOptions struct {
	PageSize  int
	PageToken string
	// Query is Drive's own drives.list query, e.g. name contains 'x'.
	Query string
}

// maxDrivePageSize is Drive's ceiling for drives.list.
const maxDrivePageSize = 100

// ListDrives returns one page of the shared drives this account can see,
// hidden ones included: whether a drive is hidden from the sidebar is a
// display choice, and the caller is the one that knows what to do with
// it. Consumer accounts have no shared drives at all, and
// about.canCreateDrives says so.
func (c *Client) ListDrives(ctx context.Context, o ListDrivesOptions) (*gdrive.DriveList, error) {
	v := url.Values{}
	size := o.PageSize
	if size <= 0 || size > maxDrivePageSize {
		size = maxDrivePageSize
	}
	v.Set("pageSize", strconv.Itoa(size))
	if o.PageToken != "" {
		v.Set("pageToken", o.PageToken)
	}
	if o.Query != "" {
		v.Set("q", o.Query)
	}
	v.Set("fields", "nextPageToken,drives("+DriveFields+")")
	u := c.base + "/drives?" + v.Encode()
	body, err := c.do(ctx, request{kind: kindRead, method: http.MethodGet, url: u})
	if err != nil {
		return nil, err
	}
	var list gdrive.DriveList
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("%w: decode drive list: %w", ErrUnexpected, err)
	}
	return &list, nil
}
