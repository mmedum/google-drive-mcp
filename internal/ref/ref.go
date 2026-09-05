// Package ref parses the strings a tool accepts where it points at
// something in Drive: an id, any Google URL, the words for My Drive's
// root, or a path. It only parses. Turning a path into an id needs
// listings and lives in internal/service, because that is where the
// ambiguity has to be reported out loud.
package ref

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// Kind says what a reference points at.
type Kind int

// Reference kinds.
const (
	// KindID is a Drive file, folder or shortcut id.
	KindID Kind = iota
	// KindRoot is the root of My Drive.
	KindRoot
	// KindPath is a path from the root of My Drive.
	KindPath
	// KindDrivePath is a shared drive, optionally with a path inside it.
	KindDrivePath
)

// String names the kind, for messages.
func (k Kind) String() string {
	switch k {
	case KindID:
		return "id"
	case KindRoot:
		return "my drive root"
	case KindPath:
		return "path"
	case KindDrivePath:
		return "shared drive path"
	}
	return "unknown"
}

// Ref is a parsed reference.
type Ref struct {
	Kind Kind
	// ID is set for KindID.
	ID string
	// ResourceKey is the key a link-shared URL carried, if any. Files
	// under the 2021 security update need it on every call.
	ResourceKey string
	// Drive is the shared drive's name or id, for KindDrivePath. The
	// resolver tries it as an id first, then as a name.
	Drive string
	// Segments are the path components, for KindPath and KindDrivePath.
	// A KindDrivePath with no segments is the drive's own root.
	Segments []string
	// FromURL marks a reference that came from a Google URL. An id typed
	// on its own could also have been a file name, and the resolver falls
	// back to that; an id lifted out of a URL is certainly an id.
	FromURL bool
	// Raw is the string as it was given, for error messages.
	Raw string
}

// IsPath reports whether the reference still needs resolving against
// Drive before it names one item.
func (r Ref) IsPath() bool { return r.Kind == KindPath || r.Kind == KindDrivePath }

// Display renders the reference the way it should appear in a message.
func (r Ref) Display() string {
	switch r.Kind {
	case KindRoot:
		return "My Drive"
	case KindPath:
		return "/" + strings.Join(r.Segments, "/")
	case KindDrivePath:
		if len(r.Segments) == 0 {
			return r.Drive
		}
		return r.Drive + "/" + strings.Join(r.Segments, "/")
	default:
		return r.ID
	}
}

// idPattern is the shape of a Drive id: the URL-safe base64 alphabet,
// long enough that no plausible file name collides with it. Drive issues
// file ids of 28 to 44 characters and shared-drive ids of 19, so the
// floor sits well below anything real and well above a file name that
// happens to contain no space, dot or punctuation. A word that clears
// the bar but names no file is retried as a name (see internal/service).
var idPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{15,}$`)

// IsID reports whether s has the shape of a Drive id.
func IsID(s string) bool { return idPattern.MatchString(s) }

// ErrParse wraps every parse failure.
type ErrParse struct {
	Input  string
	Reason string
}

func (e *ErrParse) Error() string {
	return fmt.Sprintf("cannot read %q as a file reference: %s", e.Input, e.Reason)
}

// Help is the list of forms a reference may take, for error messages.
const Help = "give an id, a Drive or Docs URL, `root` for My Drive, a path like /Projects/2026/Budget.xlsx, " +
	"or a shared-drive path like drive:Marketing/Campaigns"

// Parse reads any of the accepted forms.
func Parse(s string) (Ref, error) {
	raw := s
	s = strings.TrimSpace(s)
	if s == "" {
		return Ref{}, &ErrParse{Input: raw, Reason: "it is empty; " + Help}
	}
	if isURL(s) {
		return parseURL(s, raw)
	}
	switch strings.ToLower(s) {
	case "root", "my_drive", "mydrive", "my-drive", "my drive":
		return Ref{Kind: KindRoot, Raw: raw}, nil
	}
	if rest, ok := cutDrivePrefix(s); ok {
		return parseDrivePath(rest, raw)
	}
	if IsID(s) && !strings.Contains(s, "/") {
		return Ref{Kind: KindID, ID: s, Raw: raw}, nil
	}
	segs, err := splitPath(s)
	if err != nil {
		return Ref{}, &ErrParse{Input: raw, Reason: err.Error() + "; " + Help}
	}
	return Ref{Kind: KindPath, Segments: segs, Raw: raw}, nil
}

// cutDrivePrefix strips the `drive:` prefix, case-insensitively, without
// mistaking a URL scheme for it.
func cutDrivePrefix(s string) (string, bool) {
	if len(s) < 6 || !strings.EqualFold(s[:6], "drive:") {
		return "", false
	}
	return s[6:], true
}

func parseDrivePath(rest, raw string) (Ref, error) {
	rest = strings.TrimLeft(rest, "/")
	if rest == "" {
		return Ref{}, &ErrParse{Input: raw, Reason: "no shared drive named after `drive:`; write drive:Marketing or drive:Marketing/Campaigns"}
	}
	parts, err := splitPath(rest)
	if err != nil {
		return Ref{}, &ErrParse{Input: raw, Reason: err.Error()}
	}
	return Ref{Kind: KindDrivePath, Drive: parts[0], Segments: parts[1:], Raw: raw}, nil
}

// splitPath breaks a path into non-empty segments. A path is what a
// person types, so a stray double slash or trailing slash is tolerated.
func splitPath(s string) ([]string, error) {
	var out []string
	for _, part := range strings.Split(s, "/") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if part == "." || part == ".." {
			return nil, fmt.Errorf("`%s` has no meaning in Drive: a file has one parent and paths are absolute", part)
		}
		out = append(out, part)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("it has no path segments")
	}
	return out, nil
}

func isURL(s string) bool {
	l := strings.ToLower(s)
	return strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://")
}

// googleDocsPaths are the per-application URL prefixes on
// docs.google.com; the id follows /d/.
var googleDocsPaths = map[string]bool{
	"document": true, "spreadsheets": true, "presentation": true,
	"forms": true, "drawings": true,
}

func parseURL(s, raw string) (Ref, error) {
	u, err := url.Parse(s)
	if err != nil {
		return Ref{}, &ErrParse{Input: raw, Reason: "it is not a valid URL: " + err.Error()}
	}
	host := strings.ToLower(u.Hostname())
	// resourcekey rides on both drive.google.com and docs.google.com links.
	key := u.Query().Get("resourcekey")

	segs := pathSegments(u.Path)
	switch host {
	case "drive.google.com":
		return parseDriveURL(segs, u, key, raw)
	case "docs.google.com":
		return parseDocsURL(segs, key, raw)
	}
	return Ref{}, &ErrParse{Input: raw, Reason: "only drive.google.com and docs.google.com links name a Drive file; " + Help}
}

func parseDriveURL(segs []string, u *url.URL, key, raw string) (Ref, error) {
	// /drive/u/0/folders/<id> carries an account switcher; drop it.
	segs = dropUserSegment(segs)
	switch {
	case len(segs) >= 3 && segs[0] == "file" && segs[1] == "d":
		return idRef(segs[2], key, raw)
	case len(segs) >= 3 && segs[0] == "drive" && segs[1] == "folders":
		return idRef(segs[2], key, raw)
	case len(segs) >= 3 && segs[0] == "drive" && segs[1] == "drives":
		return Ref{Kind: KindDrivePath, Drive: segs[2], Raw: raw}, nil
	case len(segs) >= 2 && segs[0] == "drive" && (segs[1] == "my-drive" || segs[1] == "home"):
		return Ref{Kind: KindRoot, Raw: raw}, nil
	case len(segs) >= 1 && (segs[0] == "open" || segs[0] == "uc" || segs[0] == "thumbnail"):
		if id := u.Query().Get("id"); id != "" {
			return idRef(id, key, raw)
		}
		return Ref{}, &ErrParse{Input: raw, Reason: "the link has no id parameter"}
	case len(segs) >= 2 && segs[0] == "drive" && segs[1] == "shared-drives":
		return Ref{}, &ErrParse{Input: raw, Reason: "that link is the list of shared drives, not one drive; open one drive and copy its URL from the address bar"}
	}
	return Ref{}, &ErrParse{Input: raw, Reason: "the link does not contain a file, folder or shared-drive id; " + Help}
}

func parseDocsURL(segs []string, key, raw string) (Ref, error) {
	segs = dropUserSegment(segs)
	if len(segs) >= 3 && googleDocsPaths[segs[0]] && segs[1] == "d" {
		// /spreadsheets/d/e/<long>/pubhtml is a published link whose id
		// is not the file id, and no API call accepts it.
		if segs[2] == "e" {
			return Ref{}, &ErrParse{Input: raw,
				Reason: "that is a published-to-web link; its id is not the file's id. Open the file in Drive and copy the URL from the address bar"}
		}
		return idRef(segs[2], key, raw)
	}
	return Ref{}, &ErrParse{Input: raw, Reason: "the link does not contain a document id; " + Help}
}

// dropUserSegment removes the /u/<n>/ account switcher Google puts in
// URLs when several accounts are signed in.
func dropUserSegment(segs []string) []string {
	for i := 0; i+2 < len(segs); i++ {
		if segs[i] == "u" && isDigits(segs[i+1]) {
			return append(append([]string{}, segs[:i]...), segs[i+2:]...)
		}
	}
	return segs
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func idRef(id, key, raw string) (Ref, error) {
	if !IsID(id) {
		return Ref{}, &ErrParse{Input: raw, Reason: fmt.Sprintf("%q is not a Drive id", id)}
	}
	return Ref{Kind: KindID, ID: id, ResourceKey: key, FromURL: true, Raw: raw}, nil
}

func pathSegments(p string) []string {
	var out []string
	for _, s := range strings.Split(p, "/") {
		if s == "" {
			continue
		}
		if unescaped, err := url.PathUnescape(s); err == nil {
			s = unescaped
		}
		out = append(out, s)
	}
	return out
}
