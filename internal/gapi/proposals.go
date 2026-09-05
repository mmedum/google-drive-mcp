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

// ProposalFields is what an access proposal is read with.
const ProposalFields = "proposalId,fileId,requesterEmailAddress,recipientEmailAddress," +
	"requestMessage,createTime,rolesAndViews"

// ListAccessProposals returns every pending request to be let into a
// file, paging to the end. Only an approver may call it; for anybody
// else Drive answers 403.
func (c *Client) ListAccessProposals(ctx context.Context, fileID string) ([]*gdrive.AccessProposal, error) {
	segment, err := fileSegment(fileID)
	if err != nil {
		return nil, err
	}
	var out []*gdrive.AccessProposal
	pageToken := ""
	for {
		v := url.Values{}
		v.Set("fields", "nextPageToken,accessProposals("+ProposalFields+")")
		v.Set("pageSize", strconv.Itoa(maxProposalPageSize))
		if pageToken != "" {
			v.Set("pageToken", pageToken)
		}
		u := c.base + "/files/" + segment + "/accessproposals?" + v.Encode()
		body, err := c.do(ctx, request{kind: kindRead, method: http.MethodGet, url: u,
			resourceIDs: []string{fileID}})
		if err != nil {
			return nil, err
		}
		var page gdrive.AccessProposalList
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, fmt.Errorf("%w: decode access proposals: %w", ErrUnexpected, err)
		}
		out = append(out, page.AccessProposals...)
		if page.NextPageToken == "" || len(page.AccessProposals) == 0 {
			return out, nil
		}
		pageToken = page.NextPageToken
	}
}

// maxProposalPageSize is what this client asks for per page. The
// reference sets no ceiling for accessproposals.list, so this is a
// self-imposed one: a file with a hundred pending requests is a listing,
// not a single answer.
const maxProposalPageSize = 100

// ResolveAccessProposal accepts or denies one pending request.
//
// Drive answers 200 with an EMPTY BODY — the discovery document gives
// the method no response type at all — so nothing about the result can
// be read off the call. Whoever needs to know what access exists
// afterwards has to go and look, which is what internal/service does.
//
// It is a POST, so it is not repeated after an ambiguous failure: the
// proposal may already have been accepted, and a second attempt would
// answer 404 for a grant that was in fact made.
func (c *Client) ResolveAccessProposal(ctx context.Context, fileID, proposalID string, body *gdrive.ResolveProposal) error {
	segment, err := fileSegment(fileID)
	if err != nil {
		return err
	}
	if proposalID == "" {
		return fmt.Errorf("%w: an access proposal id is required", ErrInvalid)
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	// The action is a colon-suffixed verb on the resource, which is the
	// one place this API departs from plain REST: ":resolve" below is a
	// literal this client appends, never part of the id.
	u := c.base + "/files/" + segment + "/accessproposals/" + url.PathEscape(proposalID) + ":resolve"
	_, err = c.do(ctx, request{kind: kindSharing, method: http.MethodPost, url: u,
		body: payload, resourceIDs: []string{fileID}})
	return err
}
