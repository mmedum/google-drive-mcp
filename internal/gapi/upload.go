package gapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"

	"github.com/mmedum/google-drive-mcp/internal/gdrive"
)

// Upload sizes Google documents.
const (
	// MaxMultipartUpload is the ceiling for a simple or multipart upload;
	// above it the upload has to be resumable.
	MaxMultipartUpload = 5 << 20
	// ChunkAlignment is the block a resumable chunk must be a multiple
	// of, so that Drive can report progress on a boundary.
	ChunkAlignment = 256 << 10
	// DefaultChunkSize is one resumable chunk: large enough that a big
	// file is not thousands of round trips, small enough to hold in
	// memory and to resend after an interruption.
	DefaultChunkSize = 8 << 20
)

// UploadRequest is the metadata half of a content upload. It carries
// WriteOptions rather than repeating its fields, so an upload and a
// metadata write take the same parameters and gain new ones together.
type UploadRequest struct {
	WriteOptions
	// FileID replaces that file's content; empty creates a new file.
	FileID string
	// Meta is the metadata to create or patch alongside the bytes.
	Meta *gdrive.FileMeta
	// ContentType is what the bytes are, which is not always what the
	// file becomes: an upload converts when Meta.MimeType differs.
	ContentType string
}

// uploadURL builds a media-upload URL. Drive serves uploads from /upload
// with the same version path, so the endpoint is derived from the base
// rather than configured twice.
func (c *Client) uploadURL(path string, v url.Values) string {
	base := c.base
	if i := strings.LastIndex(base, "/drive/"); i >= 0 {
		base = base[:i] + "/upload" + base[i:]
	}
	return base + path + "?" + v.Encode()
}

// uploadParams are the query parameters of one upload: a metadata
// write's, plus which upload protocol this is.
func (r UploadRequest) uploadParams(uploadType string) url.Values {
	v := r.values()
	v.Set("uploadType", uploadType)
	return v
}

// method and path differ between creating a file and replacing one's
// content, and nothing else about an upload does.
func (r UploadRequest) target() (method, path string) {
	if r.FileID != "" {
		return http.MethodPatch, "/files/" + url.PathEscape(r.FileID)
	}
	return http.MethodPost, "/files"
}

// UploadMultipart sends metadata and content in one request. Google caps
// this at 5 MB; above that the upload must be resumable.
func (c *Client) UploadMultipart(ctx context.Context, r UploadRequest, content []byte) (*gdrive.File, error) {
	if len(content) > MaxMultipartUpload {
		return nil, fmt.Errorf("%w: %d bytes is more than a multipart upload's %d",
			ErrInvalid, len(content), MaxMultipartUpload)
	}
	meta, err := json.Marshal(orEmptyMeta(r.Meta))
	if err != nil {
		return nil, err
	}
	var body bytes.Buffer
	// The payload's size is known, so the buffer is sized once instead of
	// doubling its way to five megabytes.
	body.Grow(len(content) + len(meta) + 512)
	mw := multipart.NewWriter(&body)
	metaPart, err := mw.CreatePart(textproto.MIMEHeader{"Content-Type": {"application/json; charset=UTF-8"}})
	if err != nil {
		return nil, err
	}
	if _, err := metaPart.Write(meta); err != nil {
		return nil, err
	}
	contentType := r.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	dataPart, err := mw.CreatePart(textproto.MIMEHeader{"Content-Type": {contentType}})
	if err != nil {
		return nil, err
	}
	if _, err := dataPart.Write(content); err != nil {
		return nil, err
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}

	method, path := r.target()
	// A create carries a pre-generated id, which is what makes the retry
	// inside do safe; an update is a patch and idempotent in itself.
	resp, err := c.doResponse(ctx, request{
		kind: kindWrite, method: method, url: c.uploadURL(path, r.uploadParams("multipart")),
		body: body.Bytes(), contentType: "multipart/related; boundary=" + mw.Boundary(),
		resourceIDs: idsOf(r), idempotent: carriesID(r.Meta),
	})
	if err != nil {
		return nil, err
	}
	return c.uploaded(resp.body)
}

// uploaded decodes an upload's answer and records the resource keys it
// carried, the same way a metadata write does: a link-shared file whose
// key is dropped here goes out without it on every later call.
func (c *Client) uploaded(body []byte) (*gdrive.File, error) {
	f, err := decodeFile(body)
	if err != nil {
		return nil, err
	}
	c.rememberKeys(f)
	return f, nil
}

// ResumableOptions tune a chunked upload.
type ResumableOptions struct {
	// ChunkSize is how much is sent per request; it is rounded down to a
	// multiple of ChunkAlignment.
	ChunkSize int64
	// Progress is called with the bytes Drive has confirmed so far.
	Progress func(sent, total int64)
}

// maxNoProgress bounds how many times an upload may go round without
// storing a byte, whether because the connection dropped or because the
// session accepted a chunk and then said it holds no more than before.
// A 308 whose Range header is missing or unchanged reads as "nothing
// stored", and without this bound the same chunk would be sent for as
// long as the context allowed.
const maxNoProgress = 8

// UploadResumable sends a file in chunks, recovering from an interrupted
// connection through the protocol itself: ask the session how much it
// stored, and continue from there. Only one chunk is ever held in
// memory, so the size of the file does not decide the size of the
// process.
func (c *Client) UploadResumable(ctx context.Context, r UploadRequest, content io.Reader, size int64, o ResumableOptions) (*gdrive.File, error) {
	if size < 0 {
		return nil, fmt.Errorf("%w: a resumable upload needs the size in advance", ErrInvalid)
	}
	chunk := o.ChunkSize
	if chunk <= 0 {
		chunk = DefaultChunkSize
	}
	// Drive wants a multiple of 256 KiB, and at least one of them.
	chunk = max(chunk-chunk%ChunkAlignment, ChunkAlignment)

	session, err := c.startResumable(ctx, r, size)
	if err != nil {
		return nil, err
	}

	// A file smaller than a chunk gets a buffer its own size: the
	// allocation should follow what is actually sent.
	buf := make([]byte, min(chunk, max(size, 1)))
	// bufStart is the file offset buf[0] holds; confirmed is how much
	// Drive says it has stored. Both only ever move forwards.
	var bufStart, bufLen, confirmed int64
	stalled := 0
	for {
		before := confirmed
		if confirmed < bufStart {
			// Drive would have to have forgotten bytes it acknowledged.
			return nil, fmt.Errorf("%w: the upload session went backwards from %d to %d",
				ErrUnexpected, bufStart, confirmed)
		}
		if confirmed >= bufStart+bufLen {
			if confirmed > bufStart+bufLen {
				return nil, fmt.Errorf("%w: the upload session claims %d bytes but only %d were sent",
					ErrUnexpected, confirmed, bufStart+bufLen)
			}
			bufStart = confirmed
			n, err := readChunk(content, buf[:min(chunk, size-bufStart)])
			if err != nil {
				return nil, fmt.Errorf("%w: reading the file to upload: %w", ErrInvalid, err)
			}
			bufLen = int64(n)
			if bufLen == 0 {
				return nil, fmt.Errorf("%w: the file ended after %d of %d bytes", ErrInvalid, bufStart, size)
			}
		}

		res, err := c.putChunk(ctx, session, buf[confirmed-bufStart:bufLen], confirmed, size)
		if err != nil {
			// The connection dropped mid-chunk. The session knows what it
			// stored, so ask it rather than guess.
			if stalled++; stalled > maxNoProgress {
				return nil, err
			}
			got, done, qerr := c.queryResumable(ctx, session, size)
			if qerr != nil {
				return nil, err
			}
			if done != nil {
				return done, nil
			}
			c.log.DebugContext(ctx, "resumable upload recovered", "confirmed", got, "total", size)
			confirmed = got
			continue
		}
		if res.file != nil {
			if o.Progress != nil {
				o.Progress(size, size)
			}
			return res.file, nil
		}
		confirmed = res.confirmed
		if confirmed > before {
			stalled = 0
		} else if stalled++; stalled > maxNoProgress {
			return nil, fmt.Errorf("%w: the upload session accepted %d chunks without storing a byte",
				ErrUnexpected, maxNoProgress)
		}
		if o.Progress != nil {
			o.Progress(confirmed, size)
		}
		if confirmed >= size {
			// Every byte is stored but Drive did not close the session,
			// which the protocol does not allow; asking is the only
			// honest way to find out what happened.
			_, done, qerr := c.queryResumable(ctx, session, size)
			if qerr != nil {
				return nil, qerr
			}
			if done != nil {
				return done, nil
			}
			return nil, fmt.Errorf("%w: the upload session accepted every byte without completing", ErrUnexpected)
		}
	}
}

// readChunk fills p as far as the reader allows, treating a short read
// as the end rather than an error, which io.ReadFull does not.
func readChunk(r io.Reader, p []byte) (int, error) {
	n, err := io.ReadFull(r, p)
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		return n, nil
	}
	return n, err
}

// startResumable opens an upload session and returns its URI.
func (c *Client) startResumable(ctx context.Context, r UploadRequest, size int64) (string, error) {
	meta, err := json.Marshal(orEmptyMeta(r.Meta))
	if err != nil {
		return "", err
	}
	contentType := r.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	method, path := r.target()
	resp, err := c.doResponse(ctx, request{
		kind: kindWrite, method: method, url: c.uploadURL(path, r.uploadParams("resumable")),
		body: meta, contentType: "application/json; charset=UTF-8",
		// Opening a session allocates a URI, not a file: nothing exists
		// until the chunks are committed, so a second attempt abandons
		// the first session and creates nothing. Without this a 503 on
		// the opening request would abort a whole large upload before a
		// byte was sent.
		idempotent: true,
		header: http.Header{
			"X-Upload-Content-Type":   {contentType},
			"X-Upload-Content-Length": {strconv.FormatInt(size, 10)},
		},
		resourceIDs: idsOf(r),
	})
	if err != nil {
		return "", err
	}
	session := resp.header.Get("Location")
	if session == "" {
		return "", fmt.Errorf("%w: the upload session carried no Location", ErrUnexpected)
	}
	return session, nil
}

// chunkResult is what one chunk PUT told us: either the finished file,
// or how much of it Drive has stored so far.
type chunkResult struct {
	file      *gdrive.File
	confirmed int64
}

// acceptResumable treats 308 as success: it is how Drive says "stored
// what you sent, keep going", and it is not an error to retry or report.
func acceptResumable(status int) bool {
	return status == http.StatusPermanentRedirect || (status >= 200 && status < 300)
}

func (c *Client) putChunk(ctx context.Context, session string, data []byte, offset, total int64) (*chunkResult, error) {
	last := offset + int64(len(data)) - 1
	resp, err := c.doResponse(ctx, request{
		kind: kindWrite, method: http.MethodPut, url: session, body: data,
		contentType: "application/octet-stream",
		header: http.Header{
			"Content-Range": {fmt.Sprintf("bytes %d-%d/%d", offset, last, total)},
		},
		accepted: acceptResumable,
	})
	if err != nil {
		return nil, err
	}
	if resp.status == http.StatusPermanentRedirect {
		return &chunkResult{confirmed: storedBytes(resp.header.Get("Range"))}, nil
	}
	f, err := c.uploaded(resp.body)
	if err != nil {
		return nil, err
	}
	return &chunkResult{file: f}, nil
}

// queryResumable asks a session how much it has stored. An empty body
// with "bytes */total" is the documented way to ask, and a 200 or 201
// answer means the upload had in fact finished.
func (c *Client) queryResumable(ctx context.Context, session string, total int64) (int64, *gdrive.File, error) {
	resp, err := c.doResponse(ctx, request{
		kind: kindWrite, method: http.MethodPut, url: session,
		header:   http.Header{"Content-Range": {fmt.Sprintf("bytes */%d", total)}},
		accepted: acceptResumable,
	})
	if err != nil {
		return 0, nil, err
	}
	if resp.status == http.StatusPermanentRedirect {
		return storedBytes(resp.header.Get("Range")), nil, nil
	}
	f, err := c.uploaded(resp.body)
	if err != nil {
		return 0, nil, err
	}
	return total, f, nil
}

// storedBytes reads how much a session holds out of "bytes=0-262143".
// No Range header at all means nothing has been stored yet, which is
// what Google's guide says an absent header means.
func storedBytes(v string) int64 {
	_, after, ok := strings.Cut(v, "-")
	if !ok {
		return 0
	}
	n, err := strconv.ParseInt(strings.TrimSpace(after), 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	// The header names the last byte stored, inclusive.
	return n + 1
}

func decodeFile(body []byte) (*gdrive.File, error) {
	var f gdrive.File
	if err := json.Unmarshal(body, &f); err != nil {
		return nil, fmt.Errorf("%w: decode file: %w", ErrUnexpected, err)
	}
	return &f, nil
}

func orEmptyMeta(m *gdrive.FileMeta) *gdrive.FileMeta {
	if m == nil {
		return &gdrive.FileMeta{}
	}
	return m
}

// idsOf names the ids whose resource keys an upload needs: the file
// being replaced, and the parent it is going into.
func idsOf(r UploadRequest) []string {
	ids := make([]string, 0, 2)
	if r.FileID != "" {
		ids = append(ids, r.FileID)
	}
	if r.Meta != nil {
		ids = append(ids, r.Meta.Parents...)
	}
	return ids
}
