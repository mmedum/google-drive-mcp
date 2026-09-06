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
	body, err := c.do(ctx, request{method: http.MethodGet, url: u})
	if err != nil {
		return nil, err
	}
	var list gdrive.DriveList
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("%w: decode drive list: %w", ErrUnexpected, err)
	}
	return &list, nil
}

// GetDrive reads one shared drive.
func (c *Client) GetDrive(ctx context.Context, driveID string) (*gdrive.Drive, error) {
	v := url.Values{}
	v.Set("fields", DriveFields)
	u := c.base + "/drives/" + url.PathEscape(driveID) + "?" + v.Encode()
	body, err := c.do(ctx, request{method: http.MethodGet, url: u})
	if err != nil {
		return nil, err
	}
	return decodeDrive(body)
}

// CreateDrive makes a shared drive. requestId is what makes the call
// idempotent: Drive collapses a repeat carrying the same one into the
// first, so a retry after an ambiguous failure cannot leave two drives
// behind. It is required, not optional.
func (c *Client) CreateDrive(ctx context.Context, requestID string, meta *gdrive.DriveMeta) (*gdrive.Drive, error) {
	if requestID == "" {
		return nil, fmt.Errorf("%w: drives.create needs a requestId", ErrInvalid)
	}
	payload, err := json.Marshal(meta)
	if err != nil {
		return nil, err
	}
	v := url.Values{}
	v.Set("requestId", requestID)
	v.Set("fields", DriveFields)
	u := c.base + "/drives?" + v.Encode()
	body, err := c.do(ctx, request{method: http.MethodPost, url: u,
		body: payload, idempotent: true})
	if err != nil {
		return nil, err
	}
	return decodeDrive(body)
}

// UpdateDrive renames a shared drive or changes its restrictions. Only
// the fields present in meta change. Hiding goes through HideDrive
// instead: the reference gives that its own endpoint, and a field that
// a patch might quietly ignore is not the way to change what a person
// sees in their sidebar.
func (c *Client) UpdateDrive(ctx context.Context, driveID string, meta *gdrive.DriveMeta) (*gdrive.Drive, error) {
	payload, err := json.Marshal(meta)
	if err != nil {
		return nil, err
	}
	v := url.Values{}
	v.Set("fields", DriveFields)
	u := c.base + "/drives/" + url.PathEscape(driveID) + "?" + v.Encode()
	body, err := c.do(ctx, request{method: http.MethodPatch, url: u, body: payload})
	if err != nil {
		return nil, err
	}
	return decodeDrive(body)
}

// DeleteDrive removes a shared drive. Drive requires it to be empty
// unless the caller asks otherwise, and this client never does: a switch
// that deletes a drive's contents along with it is not one to offer.
func (c *Client) DeleteDrive(ctx context.Context, driveID string) error {
	u := c.base + "/drives/" + url.PathEscape(driveID)
	_, err := c.do(ctx, request{method: http.MethodDelete, url: u})
	return err
}

func decodeDrive(body []byte) (*gdrive.Drive, error) {
	var d gdrive.Drive
	if err := json.Unmarshal(body, &d); err != nil {
		return nil, fmt.Errorf("%w: decode drive: %w", ErrUnexpected, err)
	}
	return &d, nil
}

// HideDrive removes a shared drive from the default view, and UnhideDrive
// puts it back. Drive gives each its own endpoint; both answer with the
// drive.
func (c *Client) HideDrive(ctx context.Context, driveID string) (*gdrive.Drive, error) {
	return c.setDriveHidden(ctx, driveID, "hide")
}

// UnhideDrive puts a hidden shared drive back in the default view.
func (c *Client) UnhideDrive(ctx context.Context, driveID string) (*gdrive.Drive, error) {
	return c.setDriveHidden(ctx, driveID, "unhide")
}

// setDriveHidden posts to drives.hide or drives.unhide. Neither takes a
// body, and both set a state rather than adding to one, so a repeat
// means what one call meant and they are marked repeatable.
func (c *Client) setDriveHidden(ctx context.Context, driveID, action string) (*gdrive.Drive, error) {
	v := url.Values{}
	v.Set("fields", DriveFields)
	u := c.base + "/drives/" + url.PathEscape(driveID) + "/" + action + "?" + v.Encode()
	body, err := c.do(ctx, request{method: http.MethodPost, url: u, idempotent: true})
	if err != nil {
		return nil, err
	}
	return decodeDrive(body)
}
