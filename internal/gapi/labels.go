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

// Labels reach this server through two APIs, and which one answers what
// is the thing to keep straight.
//
// The VALUES applied to a file are Drive's: files.listLabels reads them
// and files.modifyLabels writes them, both under the ordinary drive
// scope. The DEFINITIONS — which labels exist, what fields they have,
// which values those fields accept — belong to the Drive Labels API on
// its own host, with its own scopes and its own enablement in the Cloud
// project.
//
// The consequence worth stating: reading the labels on a file needs no
// labels scope at all. Phase 4 found the opposite assumed here, and the
// file card had been asking for labels the wrong way since it shipped
// (see ListFileLabels).

// MaxFileLabelPageSize is Drive's own ceiling for files.listLabels; the
// discovery document gives it as both the maximum and the default.
const MaxFileLabelPageSize = 100

// ListFileLabelsOptions tune one page of a file's applied labels.
type ListFileLabelsOptions struct {
	PageSize  int
	PageToken string
}

// ListFileLabels returns one page of the labels applied to a file.
//
// This is the only way to ask for a file's labels without knowing their
// ids first. files.get takes an includeLabels parameter, but it is a
// comma-separated list of label IDS — not a flag, and not a wildcard.
// This client sent `includeLabels=*` from phase 1 until phase 4, which
// Drive answers 400 badRequest: with GDRIVE_LABELS off by default,
// nothing ever ran the call that would have said so. The discovery
// document and a live probe agree, which is what makes it a fact rather
// than one bad response.
func (c *Client) ListFileLabels(ctx context.Context, fileID string, o ListFileLabelsOptions) (*gdrive.LabelList, error) {
	segment, err := fileSegment(fileID)
	if err != nil {
		return nil, err
	}
	v := url.Values{}
	size := o.PageSize
	if size <= 0 || size > MaxFileLabelPageSize {
		size = MaxFileLabelPageSize
	}
	v.Set("maxResults", strconv.Itoa(size))
	if o.PageToken != "" {
		v.Set("pageToken", o.PageToken)
	}
	u := c.base + "/files/" + segment + "/listLabels?" + v.Encode()
	body, err := c.do(ctx, request{method: http.MethodGet, url: u, resourceIDs: []string{fileID}})
	if err != nil {
		return nil, err
	}
	var list gdrive.LabelList
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("%w: decode file labels: %w", ErrUnexpected, err)
	}
	return &list, nil
}

// AllFileLabels reads every page of a file's labels. A file carries few
// enough labels that paging them out to a caller would be a worse
// interface than the extra call costs: the API's own maximum page is
// 100, and a file that has a hundred labels on it is not a case this
// server needs to be clever about.
func (c *Client) AllFileLabels(ctx context.Context, fileID string) ([]*gdrive.Label, error) {
	var all []*gdrive.Label
	token := ""
	for {
		page, err := c.ListFileLabels(ctx, fileID, ListFileLabelsOptions{PageToken: token})
		if err != nil {
			return nil, err
		}
		all = append(all, page.Labels...)
		if page.NextPageToken == "" || len(page.Labels) == 0 {
			return all, nil
		}
		token = page.NextPageToken
	}
}

// ModifyLabels applies, changes or removes labels on a file.
//
// The request is marked idempotent, which for a POST has to be argued
// rather than assumed. Every modification the API accepts is an
// assignment and not a delta: a field setter REPLACES that field's
// values, unsetValues clears them, and removeLabel takes the label off.
// Applying the same request twice therefore leaves the same state as
// applying it once, so a retry after a network failure cannot double
// anything. The reference is also explicit that the modifications
// succeed or fail together, so there is no half-applied state a second
// attempt could land on top of.
func (c *Client) ModifyLabels(ctx context.Context, fileID string, req gdrive.ModifyLabelsRequest) (*gdrive.ModifyLabelsResponse, error) {
	segment, err := fileSegment(fileID)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("%w: encode label modifications: %w", ErrUnexpected, err)
	}
	u := c.base + "/files/" + segment + "/modifyLabels"
	body, err := c.do(ctx, request{
		method:      http.MethodPost,
		url:         u,
		body:        payload,
		resourceIDs: []string{fileID},
		idempotent:  true,
	})
	if err != nil {
		return nil, err
	}
	var out gdrive.ModifyLabelsResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("%w: decode label modifications: %w", ErrUnexpected, err)
	}
	return &out, nil
}

// Drive Labels' own page sizes for labels.list: 50 when asked for none,
// 200 at most.
const (
	DefaultLabelPageSize = 50
	MaxLabelPageSize     = 200
)

// ListLabelDefinitionsOptions tune one page of label definitions.
type ListLabelDefinitionsOptions struct {
	PageSize  int
	PageToken string
	// MinimumRole filters to labels the user holds at least this role on.
	// Empty means the API's own default, READER — every label they can
	// see, including ones they may not apply.
	MinimumRole string
}

// ListLabelDefinitions returns one page of the label definitions this
// account may use.
//
// Two parameters are fixed rather than exposed, because the wrong value
// for either produces a listing that cannot be acted on:
//
// publishedOnly=true, because an unpublished draft cannot be applied to
// a file, and because it makes the resource name reference the published
// revision. view=LABEL_VIEW_FULL, because the default view omits the
// fields — and a label listing without field ids and their permitted
// values tells a model what exists but not what it can set.
func (c *Client) ListLabelDefinitions(ctx context.Context, o ListLabelDefinitionsOptions) (*gdrive.LabelDefinitionList, error) {
	v := url.Values{}
	size := o.PageSize
	switch {
	case size <= 0:
		size = DefaultLabelPageSize
	case size > MaxLabelPageSize:
		size = MaxLabelPageSize
	}
	v.Set("pageSize", strconv.Itoa(size))
	v.Set("publishedOnly", "true")
	v.Set("view", "LABEL_VIEW_FULL")
	if o.PageToken != "" {
		v.Set("pageToken", o.PageToken)
	}
	if o.MinimumRole != "" {
		v.Set("minimumRole", o.MinimumRole)
	}
	u := c.labelsBase + "/labels?" + v.Encode()
	body, err := c.do(ctx, request{method: http.MethodGet, url: u})
	if err != nil {
		return nil, err
	}
	var list gdrive.LabelDefinitionList
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("%w: decode label definitions: %w", ErrUnexpected, err)
	}
	return &list, nil
}
