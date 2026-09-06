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

// ListRevisions returns a file's revisions, oldest first as Drive sends
// them, paging to the end. Drive's own documentation warns that the list
// can be incomplete for a busy Docs editors file; the caller passes that
// warning on rather than hiding it.
func (c *Client) ListRevisions(ctx context.Context, fileID string) ([]*gdrive.Revision, error) {
	segment, err := fileSegment(fileID)
	if err != nil {
		return nil, err
	}
	var out []*gdrive.Revision
	pageToken := ""
	for {
		v := url.Values{}
		v.Set("fields", "nextPageToken,revisions("+RevisionFields+")")
		v.Set("pageSize", strconv.Itoa(maxRevisionPageSize))
		if pageToken != "" {
			v.Set("pageToken", pageToken)
		}
		u := c.base + "/files/" + segment + "/revisions?" + v.Encode()
		body, err := c.do(ctx, request{method: http.MethodGet, url: u,
			resourceIDs: []string{fileID}})
		if err != nil {
			return nil, err
		}
		var page gdrive.RevisionList
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, fmt.Errorf("%w: decode revisions: %w", ErrUnexpected, err)
		}
		out = append(out, page.Revisions...)
		if page.NextPageToken == "" || len(page.Revisions) == 0 {
			return out, nil
		}
		pageToken = page.NextPageToken
	}
}

// maxRevisionPageSize is Drive's ceiling for revisions.list.
const maxRevisionPageSize = 1000

// DeleteRevision removes one revision permanently. Drive refuses it for
// the head revision and for a Docs editors file, whose history is not
// made of separate revisions in this sense.
func (c *Client) DeleteRevision(ctx context.Context, fileID, revisionID string) error {
	segment, err := fileSegment(fileID)
	if err != nil {
		return err
	}
	u := c.base + "/files/" + segment + "/revisions/" + url.PathEscape(revisionID)
	_, err = c.do(ctx, request{method: http.MethodDelete, url: u,
		resourceIDs: []string{fileID}})
	return err
}

// DeleteFile removes a file for good, skipping the trash. In a shared
// drive it takes the file's contents with it if the file is a folder,
// and there is no undo at any level.
func (c *Client) DeleteFile(ctx context.Context, fileID string) error {
	// The guard matters most here: "trash" is files.emptyTrash, so
	// without it a delete of one file could empty a whole trash.
	segment, err := fileSegment(fileID)
	if err != nil {
		return err
	}
	v := url.Values{}
	v.Set("supportsAllDrives", "true")
	u := c.base + "/files/" + segment + "?" + v.Encode()
	_, err = c.do(ctx, request{method: http.MethodDelete, url: u,
		resourceIDs: []string{fileID}})
	return err
}

// EmptyTrash permanently removes everything in the trash. driveID limits
// it to one shared drive's trash; without one it is this account's own.
func (c *Client) EmptyTrash(ctx context.Context, driveID string) error {
	v := url.Values{}
	if driveID != "" {
		v.Set("driveId", driveID)
	}
	u := c.base + "/files/trash"
	if q := v.Encode(); q != "" {
		u += "?" + q
	}
	_, err := c.do(ctx, request{method: http.MethodDelete, url: u})
	return err
}
