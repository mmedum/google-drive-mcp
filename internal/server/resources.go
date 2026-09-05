package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-drive-mcp/internal/service"
)

// Scheme is the URI scheme this server's resources live under.
const Scheme = "gdrive://"

// The three resource templates. A variable in an RFC 6570 simple
// expansion excludes "/", and the SDK matches a template through an
// ANCHORED regexp built from it (verified against go-sdk v1.7.0 and
// uritemplate v3.0.2, §18), so gdrive://{file} does not also match
// gdrive://{file}/meta and the three cannot shadow each other. A
// reserved expansion — {+file} — would, and is deliberately not used.
//
// The consequence for a caller is that a reference containing a slash —
// a path, a URL — has to arrive percent-encoded: gdrive://My%20Drive%2F
// Reports. Ids need no encoding, and ids are the contract.
const (
	fileTemplate     = Scheme + "{file}"
	metaTemplate     = Scheme + "{file}/meta"
	childrenTemplate = Scheme + "{folder}/children"
)

// registerResources adds the gdrive:// templates. They are reads, so
// every mode registers them; a resource has no arguments, which is why
// each is the plainest form of its question — the whole text, the card,
// the first page.
//
// There is no static resource list. Listing every file in a Drive is a
// listing, and a client that expected the resource list to be cheap
// would be paying for one on every connection.
func registerResources(s *mcp.Server, d Deps) {
	s.AddResourceTemplate(&mcp.ResourceTemplate{
		Name:        "file text",
		Title:       "Drive file as text",
		URITemplate: fileTemplate,
		Description: "The text of a Drive file: a Google Doc as markdown, a Sheet as csv, and a text file " +
			"as itself. The whole file up to one large budget, where read_file returns a window and can be " +
			"asked for the next one. The {file} is a file id; a path or a URL has to be percent-encoded.",
	}, resourceHandler(d, func(ctx context.Context, ref string) (*service.Resource, error) {
		return d.Service.ResourceText(ctx, ref)
	}))

	s.AddResourceTemplate(&mcp.ResourceTemplate{
		Name:        "file card",
		Title:       "Drive file description",
		URITemplate: metaTemplate,
		MIMEType:    "text/plain",
		Description: "What a Drive file is, where it sits, who can see it and what this account may do with " +
			"it: the same card get_file returns.",
	}, resourceHandler(d, func(ctx context.Context, ref string) (*service.Resource, error) {
		return d.Service.ResourceCard(ctx, ref)
	}))

	s.AddResourceTemplate(&mcp.ResourceTemplate{
		Name:        "folder listing",
		Title:       "Drive folder contents",
		URITemplate: childrenTemplate,
		MIMEType:    "text/plain",
		Description: "The first page of a folder's contents. One page and never a tree: list_folder is the " +
			"tool that walks one, with the budgets a walk needs.",
	}, resourceHandler(d, func(ctx context.Context, ref string) (*service.Resource, error) {
		return d.Service.ResourceChildren(ctx, ref)
	}))
}

// resourceHandler turns one of the service's resource readers into an
// SDK handler: pull the reference out of the URI, call, and shape the
// result. The refusals are the service's own, so a resource read and the
// matching tool call fail with the same words.
func resourceHandler(d Deps, read func(context.Context, string) (*service.Resource, error),
) mcp.ResourceHandler {
	return func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		uri := req.Params.URI
		ref, err := reference(uri)
		if err != nil {
			return nil, err
		}
		if d.Service == nil {
			return nil, errors.New("[unexpected] this server has no Drive connection")
		}
		out, err := read(ctx, ref)
		if err != nil {
			return nil, resourceError(uri, err)
		}
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{
			URI: uri, MIMEType: out.MimeType, Text: out.Text,
		}}}, nil
	}
}

// reference pulls the file reference out of a gdrive:// URI and undoes
// the percent-encoding the template required. The suffix is stripped
// here rather than matched again: the SDK has already decided which
// template this URI belongs to, and re-deciding it in the handler would
// be a second matcher to keep in step with the first.
func reference(uri string) (string, error) {
	rest, ok := strings.CutPrefix(uri, Scheme)
	if !ok {
		return "", mcp.ResourceNotFoundError(uri)
	}
	for _, suffix := range []string{"/meta", "/children"} {
		rest = strings.TrimSuffix(rest, suffix)
	}
	ref, err := url.PathUnescape(rest)
	if err != nil {
		return "", mcp.ResourceNotFoundError(uri)
	}
	if strings.TrimSpace(ref) == "" {
		return "", mcp.ResourceNotFoundError(uri)
	}
	return ref, nil
}

// resourceError turns a service failure into the error a client sees.
//
// Every failure keeps the "[class] message" the tools carry, because the
// advice in it is the same advice and the model is the reader either
// way. A missing file ALSO gets the protocol's resource-not-found code
// and the uri in its data, so a client can tell "there is no such
// resource" from "the server is broken" without reading prose.
//
// The SDK's own ResourceNotFoundError is not used for this: it replaces
// the message with the fixed words "Resource not found", which would
// drop the sentence saying what to do about it.
func resourceError(uri string, err error) error {
	var se *service.Error
	if errors.As(err, &se) {
		if se.Class == service.ClassNotFound {
			return &jsonrpc.Error{
				Code:    mcp.CodeResourceNotFound,
				Message: se.Error(),
				Data:    json.RawMessage(fmt.Sprintf(`{"uri":%q}`, uri)),
			}
		}
		return errors.New(se.Error())
	}
	return errors.New("[unexpected] " + err.Error())
}
