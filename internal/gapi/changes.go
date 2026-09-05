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

// ChangeFields is what a page of the changes feed asks for. The file is
// carried with the lean listing fields: a change says what happened to
// an item, and a caller that wants everything about one asks files.get.
const ChangeFields = "nextPageToken,newStartPageToken," +
	"changes(changeType,time,removed,fileId,driveId," +
	"file(" + ListFileFields + "),drive(" + DriveFields + "))"

// StartPageToken asks where "from now on" starts in the changes feed.
// driveID scopes it to one shared drive, which is a different feed with
// its own tokens: a token from one is meaningless to the other.
func (c *Client) StartPageToken(ctx context.Context, driveID string) (string, error) {
	v := url.Values{}
	v.Set("supportsAllDrives", "true")
	if driveID != "" {
		v.Set("driveId", driveID)
	}
	u := c.base + "/changes/startPageToken?" + v.Encode()
	body, err := c.do(ctx, request{kind: kindRead, method: http.MethodGet, url: u})
	if err != nil {
		return "", err
	}
	var tok gdrive.StartPageToken
	if err := json.Unmarshal(body, &tok); err != nil {
		return "", fmt.Errorf("%w: decode start page token: %w", ErrUnexpected, err)
	}
	if tok.StartPageToken == "" {
		return "", fmt.Errorf("%w: startPageToken response carried no token", ErrUnexpected)
	}
	return tok.StartPageToken, nil
}

// ListChangesOptions tune one page of the changes feed.
type ListChangesOptions struct {
	// PageToken is required: the feed has no beginning, only a point
	// StartPageToken hands out.
	PageToken string
	PageSize  int
	// DriveID limits the feed to one shared drive.
	DriveID string
	// IncludeRemoved keeps the entries for items that left this
	// account's view. Drive's default is true and so is this server's:
	// "it is gone" is the change most worth hearing about.
	IncludeRemoved bool
	// RestrictToMyDrive drops changes to items outside My Drive.
	RestrictToMyDrive bool
	// IncludeCorpusRemovals keeps a change to a file this account lost
	// access to but which still exists.
	IncludeCorpusRemovals bool
}

// maxChangePageSize is Drive's ceiling for changes.list.
const maxChangePageSize = 1000

// ListChanges returns one page of the changes feed from a token.
func (c *Client) ListChanges(ctx context.Context, o ListChangesOptions) (*gdrive.ChangeList, error) {
	if o.PageToken == "" {
		return nil, fmt.Errorf("%w: changes.list needs a page token", ErrInvalid)
	}
	v := url.Values{}
	v.Set("pageToken", o.PageToken)
	size := o.PageSize
	switch {
	case size <= 0:
		size = 100
	case size > maxChangePageSize:
		size = maxChangePageSize
	}
	v.Set("pageSize", strconv.Itoa(size))
	v.Set("supportsAllDrives", "true")
	v.Set("includeItemsFromAllDrives", "true")
	v.Set("includeRemoved", boolText(o.IncludeRemoved))
	v.Set("restrictToMyDrive", boolText(o.RestrictToMyDrive))
	v.Set("includeCorpusRemovals", boolText(o.IncludeCorpusRemovals))
	if o.DriveID != "" {
		v.Set("driveId", o.DriveID)
	}
	v.Set("fields", ChangeFields)
	u := c.base + "/changes?" + v.Encode()
	body, err := c.do(ctx, request{kind: kindRead, method: http.MethodGet, url: u})
	if err != nil {
		return nil, err
	}
	var list gdrive.ChangeList
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("%w: decode changes: %w", ErrUnexpected, err)
	}
	for _, ch := range list.Changes {
		if ch != nil {
			c.rememberKeys(ch.File)
		}
	}
	return &list, nil
}
