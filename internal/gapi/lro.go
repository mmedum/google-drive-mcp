package gapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/mmedum/google-drive-mcp/internal/gdrive"
)

// files.download is a long-running operation, and it is the only way to
// get the bytes of a Google Vid: exporting one answers fileNotExportable.
//
// Three things about it come from the long-running-operations guide
// rather than from the discovery document, which types the operation's
// response as a bare Any:
//
//   - the finished operation carries the bytes' address in
//     response.downloadUri;
//   - a new operation is often pending, with `done: null` rather than
//     `done: false`, "especially for Vids files";
//   - polling is operations.get with an exponential backoff, and an
//     operation lives "a minimum of 12 hours". Section 2 recorded 24,
//     which phase 4 corrected against the guide.

// Polling bounds for a download operation. The guide suggests around ten
// seconds between polls; the ceiling here is this server's, so a tool
// call cannot hang for the operation's whole life.
const (
	downloadPollFirst = 2 * time.Second
	downloadPollMax   = 10 * time.Second
	// DownloadPollLimit is how long AwaitDownload will wait before giving
	// up and handing the operation name back. Rendering a video can take
	// longer than any one tool call should, and an operation that is
	// still running is not a failure.
	DownloadPollLimit = 2 * time.Minute
)

// StartDownload begins a download operation. mimeType applies only to a
// Workspace document; revisionID only to blobs, Docs and Sheets.
func (c *Client) StartDownload(ctx context.Context, fileID, mimeType, revisionID string) (*gdrive.Operation, error) {
	segment, err := fileSegment(fileID)
	if err != nil {
		return nil, err
	}
	v := url.Values{}
	if mimeType != "" {
		v.Set("mimeType", mimeType)
	}
	if revisionID != "" {
		v.Set("revisionId", revisionID)
	}
	u := c.base + "/files/" + segment + "/download"
	if len(v) > 0 {
		u += "?" + v.Encode()
	}
	// A download starts work but creates nothing and costs nothing to
	// repeat: a second operation on the same file is another operation,
	// not another file. That makes it safe to retry, which matters
	// because the alternative is failing a read on a dropped connection.
	body, err := c.do(ctx, request{
		method: http.MethodPost, url: u, resourceIDs: []string{fileID}, reads: true,
	})
	if err != nil {
		return nil, err
	}
	return decodeOperation(body)
}

// GetOperation polls one operation by the name the download answered
// with. The guide is explicit that the name is returned only there:
// there is no way to list operations and find it again.
func (c *Client) GetOperation(ctx context.Context, name string) (*gdrive.Operation, error) {
	if name == "" {
		return nil, fmt.Errorf("%w: an operation name is required", ErrUnexpected)
	}
	// The name Drive hands back is already `operations/{id}`, and the
	// endpoint is /operations/{name}. Sending the prefix twice is a 404
	// that reads like a lost operation.
	u := c.base + "/operations/" + url.PathEscape(trimOperationPrefix(name))
	body, err := c.do(ctx, request{method: http.MethodGet, url: u})
	if err != nil {
		return nil, err
	}
	return decodeOperation(body)
}

// trimOperationPrefix removes the resource-name prefix if it is there,
// so a caller may pass either form.
func trimOperationPrefix(name string) string {
	const prefix = "operations/"
	if len(name) > len(prefix) && name[:len(prefix)] == prefix {
		return name[len(prefix):]
	}
	return name
}

// AwaitDownload polls until the operation finishes and returns the
// address of the bytes.
//
// It gives up after DownloadPollLimit and says so with the operation
// name, because a Vid can take longer to render than any tool call
// should wait, and an operation that is still running is not a failure —
// the caller can ask again with the name.
func (c *Client) AwaitDownload(ctx context.Context, op *gdrive.Operation) (*gdrive.DownloadResponse, error) {
	deadline := time.Now().Add(DownloadPollLimit)
	wait := downloadPollFirst
	for {
		if op.Error != nil {
			return nil, fmt.Errorf("%w: the download operation failed: %s", ErrUnexpected, op.Error.Message)
		}
		if op.Finished() {
			if op.Response == nil || op.Response.DownloadURI == "" {
				return nil, fmt.Errorf("%w: the download operation finished with no download address", ErrUnexpected)
			}
			return op.Response, nil
		}
		if op.Name == "" {
			return nil, fmt.Errorf("%w: the download operation is unfinished and carries no name to poll", ErrUnexpected)
		}
		if time.Now().After(deadline) {
			return nil, &OperationPendingError{Name: op.Name, Waited: DownloadPollLimit}
		}
		if err := c.sleep(ctx, wait); err != nil {
			return nil, err
		}
		if wait *= 2; wait > downloadPollMax {
			wait = downloadPollMax
		}
		next, err := c.GetOperation(ctx, op.Name)
		if err != nil {
			return nil, err
		}
		op = next
	}
}

// OperationPendingError says a download is still being prepared. It is
// its own type because it is not a failure: the work continues, and the
// name is what resumes it.
type OperationPendingError struct {
	Name   string
	Waited time.Duration
}

func (e *OperationPendingError) Error() string {
	return fmt.Sprintf("the download is still being prepared after %s (operation %s)", e.Waited, e.Name)
}

func decodeOperation(body []byte) (*gdrive.Operation, error) {
	var op gdrive.Operation
	if err := json.Unmarshal(body, &op); err != nil {
		return nil, fmt.Errorf("%w: decode operation: %w", ErrUnexpected, err)
	}
	return &op, nil
}
