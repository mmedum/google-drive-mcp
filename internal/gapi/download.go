package gapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/mmedum/google-drive-mcp/internal/gdrive"
)

// Content is a byte stream from Drive and what the response said about
// it. The caller closes Body, which also releases the request.
type Content struct {
	Body io.ReadCloser
	// MimeType is the Content-Type Google sent, which for an export is
	// the format asked for and for a blob is the file's own type.
	MimeType string
	// Length is Content-Length, or -1 when the response did not say.
	Length int64
	// Partial reports a 206, meaning the Range header was honoured and
	// Length counts the window rather than the file.
	Partial bool
	// TotalLength is the file's full size when a partial response named
	// it in Content-Range, or -1.
	TotalLength int64
}

// DownloadOptions tune a content read.
type DownloadOptions struct {
	// Offset and Length request a byte window. A zero Length means the
	// rest of the file; a zero Offset with a zero Length means all of it.
	Offset int64
	Length int64
	// AcknowledgeAbuse downloads a file Google has flagged. It is never
	// set unless the caller asked for it explicitly.
	AcknowledgeAbuse bool
	// RevisionID reads an old revision's bytes instead of the head.
	RevisionID string
	// ResourceKey is a key learned from a URL.
	ResourceKey string
}

// Download streams a blob's bytes. Google-native documents have no bytes
// of their own and must go through Export instead.
func (c *Client) Download(ctx context.Context, fileID string, o DownloadOptions) (*Content, error) {
	if o.ResourceKey != "" {
		c.RememberResourceKey(fileID, o.ResourceKey)
	}
	v := url.Values{}
	v.Set("alt", "media")
	v.Set("supportsAllDrives", "true")
	if o.AcknowledgeAbuse {
		v.Set("acknowledgeAbuse", "true")
	}
	segment, err := fileSegment(fileID)
	if err != nil {
		return nil, err
	}
	u := c.base + "/files/" + segment
	if o.RevisionID != "" {
		u += "/revisions/" + url.PathEscape(o.RevisionID)
	}
	u += "?" + v.Encode()

	r := request{method: http.MethodGet, url: u, accept: "*/*", resourceIDs: []string{fileID}}
	if h := rangeHeader(o.Offset, o.Length); h != "" {
		r.header = http.Header{"Range": []string{h}}
	}
	return c.content(ctx, r)
}

// Export streams a Google-native document converted to another format.
// Google caps an export at 10 MB and the API sends no Content-Length for
// one, so the caller cannot know the size in advance.
func (c *Client) Export(ctx context.Context, fileID, mimeType string) (*Content, error) {
	v := url.Values{}
	v.Set("mimeType", mimeType)
	v.Set("supportsAllDrives", "true")
	segment, err := fileSegment(fileID)
	if err != nil {
		return nil, err
	}
	u := c.base + "/files/" + segment + "/export?" + v.Encode()
	return c.content(ctx, request{method: http.MethodGet, url: u,
		accept: "*/*", resourceIDs: []string{fileID}})
}

// DownloadURL streams a URL Drive itself handed back, such as a
// revision's export link. The host allowlist still decides whether the
// credentials go with it.
func (c *Client) DownloadURL(ctx context.Context, raw string) (*Content, error) {
	return c.content(ctx, request{method: http.MethodGet, url: raw, accept: "*/*"})
}

func (c *Client) content(ctx context.Context, r request) (*Content, error) {
	s, err := c.doStream(ctx, r)
	if err != nil {
		return nil, err
	}
	out := &Content{
		Body:        s.body,
		MimeType:    gdrive.MimeOnly(s.header.Get("Content-Type")),
		Length:      -1,
		TotalLength: -1,
		Partial:     s.status == http.StatusPartialContent,
	}
	if n, err := strconv.ParseInt(s.header.Get("Content-Length"), 10, 64); err == nil && n >= 0 {
		out.Length = n
	}
	if total, ok := totalFromContentRange(s.header.Get("Content-Range")); ok {
		out.TotalLength = total
	}
	return out, nil
}

// rangeHeader builds a byte-range request, or "" for the whole file.
func rangeHeader(offset, length int64) string {
	switch {
	case offset < 0:
		return ""
	case length > 0:
		// HTTP ranges are inclusive at both ends.
		return fmt.Sprintf("bytes=%d-%d", offset, offset+length-1)
	case offset > 0:
		return fmt.Sprintf("bytes=%d-", offset)
	}
	return ""
}

// totalFromContentRange reads the size out of "bytes 0-99/12345".
func totalFromContentRange(v string) (int64, bool) {
	_, after, ok := strings.Cut(v, "/")
	if !ok || strings.TrimSpace(after) == "*" {
		return 0, false
	}
	n, err := strconv.ParseInt(strings.TrimSpace(after), 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// RevisionFields is what a revision read asks Drive for.
const RevisionFields = "id,mimeType,modifiedTime,keepForever,published,size,md5Checksum," +
	"originalFilename,lastModifyingUser(displayName,emailAddress,me),exportLinks"

// GetRevision reads one revision's metadata. A Docs editors file's old
// content is not bytes on the wire: it is reached through the export
// links this call returns.
func (c *Client) GetRevision(ctx context.Context, fileID, revisionID string) (*gdrive.Revision, error) {
	segment, err := fileSegment(fileID)
	if err != nil {
		return nil, err
	}
	v := url.Values{}
	v.Set("fields", RevisionFields)
	u := c.base + "/files/" + segment + "/revisions/" + url.PathEscape(revisionID) + "?" + v.Encode()
	body, err := c.do(ctx, request{method: http.MethodGet, url: u, resourceIDs: []string{fileID}})
	if err != nil {
		return nil, err
	}
	var rev gdrive.Revision
	if err := json.Unmarshal(body, &rev); err != nil {
		return nil, fmt.Errorf("%w: decode revision: %w", ErrUnexpected, err)
	}
	return &rev, nil
}

// UpdateRevision changes whether Drive keeps a revision forever. Blob
// revisions are otherwise discarded after 30 days.
func (c *Client) UpdateRevision(ctx context.Context, fileID, revisionID string, keepForever bool) (*gdrive.Revision, error) {
	payload, err := json.Marshal(map[string]bool{"keepForever": keepForever})
	if err != nil {
		return nil, err
	}
	segment, err := fileSegment(fileID)
	if err != nil {
		return nil, err
	}
	v := url.Values{}
	v.Set("fields", RevisionFields)
	u := c.base + "/files/" + segment + "/revisions/" + url.PathEscape(revisionID) + "?" + v.Encode()
	body, err := c.do(ctx, request{method: http.MethodPatch, url: u,
		body: payload, resourceIDs: []string{fileID}})
	if err != nil {
		return nil, err
	}
	var rev gdrive.Revision
	if err := json.Unmarshal(body, &rev); err != nil {
		return nil, fmt.Errorf("%w: decode revision: %w", ErrUnexpected, err)
	}
	return &rev, nil
}
