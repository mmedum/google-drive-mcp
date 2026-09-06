package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The Drive API is larger than this server's use of it, and the gap is
// supposed to be a set of decisions rather than an accident. Phase 4's
// last deliverable in §16 is that every GA method is either used or
// listed as deliberately out, with the reason.
//
// testdata/api-coverage.tsv is that list, and it covers all three of the
// APIs this server can reach rather than only Drive: the split between
// them is where a method is most easily forgotten. This gate holds it to
// three things:
//
//  1. every `used` entry names a method that really exists on
//     gapi.Client, so deleting a client method breaks the record rather
//     than quietly making it a lie;
//  2. every `out` entry carries a reason, because "not used" without one
//     is not a decision;
//  3. every exported method on gapi.Client that talks to an endpoint is
//     claimed by exactly one entry, so a new call cannot be added
//     without saying which API method it is.
//
// What it deliberately does NOT do is fetch the discovery document. A
// gate that reaches the network fails when Google is slow, and CI would
// learn to ignore it. `make api-diff` does the fetching, on purpose,
// when somebody wants to know whether the API has grown.

const coverageFile = "testdata/api-coverage.tsv"

// coverageEntry is one row: an API method and this server's verdict on
// it.
type coverageEntry struct {
	method  string
	verdict string
	detail  string
	line    int
}

// clientMethodsThatAreNotCalls are the exported methods on gapi.Client
// that implement no API method. They are bookkeeping over the client's
// own state, or they compose calls made elsewhere in the file.
var clientMethodsThatAreNotCalls = map[string]string{
	"RememberResourceKey": "records a resource key seen in a URL; no request",
	"ResourceKey":         "reads a remembered resource key; no request",
	"Download":            "files.get with alt=media, which the record counts under files.get",
	"DownloadURL":         "fetches an address a response handed out, not an API method",
	"UploadMultipart":     "the media form of files.create and files.update",
	"UploadResumable":     "the resumable form of files.create and files.update",
	"AllFileLabels":       "pages files.listLabels to the end",
	"AwaitDownload":       "polls operations.get until a download finishes",
}

func apiCoverage(out io.Writer, _ []string) error {
	entries, problems := readCoverage()
	if len(problems) == 0 {
		methods, err := clientMethods()
		if err != nil {
			return err
		}
		problems = append(problems, checkCoverage(entries, methods)...)
	}
	if len(problems) > 0 {
		for _, p := range problems {
			_, _ = fmt.Fprintln(out, p)
		}
		return fmt.Errorf("%d API coverage problem(s)", len(problems))
	}
	used, gone := 0, 0
	for _, e := range entries {
		if e.verdict == "used" {
			used++
			continue
		}
		gone++
	}
	_, _ = fmt.Fprintf(out, "api coverage ok (%d methods: %d used, %d deliberately out)\n",
		len(entries), used, gone)
	return nil
}

// readCoverage parses the record.
func readCoverage() ([]coverageEntry, []string) {
	f, err := os.Open(coverageFile)
	if err != nil {
		return nil, []string{"cannot read " + coverageFile + ": " + err.Error()}
	}
	defer func() { _ = f.Close() }()

	var entries []coverageEntry
	var problems []string
	seen := map[string]bool{}
	scanner := bufio.NewScanner(f)
	for n := 1; scanner.Scan(); n++ {
		line := strings.TrimRight(scanner.Text(), " \t")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) != 5 {
			problems = append(problems, fmt.Sprintf("%s:%d: want 5 tab-separated columns, got %d",
				coverageFile, n, len(parts)))
			continue
		}
		e := coverageEntry{method: parts[0], verdict: parts[3], detail: parts[4], line: n}
		if seen[e.method] {
			problems = append(problems, fmt.Sprintf("%s:%d: %s is listed twice", coverageFile, n, e.method))
		}
		seen[e.method] = true
		switch e.verdict {
		case "used", "out":
		default:
			problems = append(problems, fmt.Sprintf("%s:%d: %s has verdict %q; want used or out",
				coverageFile, n, e.method, e.verdict))
		}
		if strings.TrimSpace(e.detail) == "" {
			problems = append(problems, fmt.Sprintf("%s:%d: %s has no reason; \"not used\" without one is not a decision",
				coverageFile, n, e.method))
		}
		entries = append(entries, e)
	}
	if err := scanner.Err(); err != nil {
		problems = append(problems, "reading "+coverageFile+": "+err.Error())
	}
	return entries, problems
}

// clientMethods reads the exported methods on gapi.Client out of the
// package's own syntax tree. The files are listed and parsed one by one
// rather than with parser.ParseDir, which is deprecated because it
// ignores build tags — this package has none, but a gate that reads the
// source should read it the way the compiler would.
func clientMethods() (map[string]bool, error) {
	const dir = "internal/gapi"
	names, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}
	fset := token.NewFileSet()
	out := map[string]bool{}
	for _, entry := range names {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", name, err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || len(fn.Recv.List) != 1 || !fn.Name.IsExported() {
				continue
			}
			star, ok := fn.Recv.List[0].Type.(*ast.StarExpr)
			if !ok {
				continue
			}
			if ident, ok := star.X.(*ast.Ident); ok && ident.Name == "Client" {
				out[fn.Name.Name] = true
			}
		}
	}
	return out, nil
}

// checkCoverage compares the record with the client in both directions.
func checkCoverage(entries []coverageEntry, methods map[string]bool) []string {
	var problems []string
	claimed := map[string]string{}
	for _, e := range entries {
		if e.verdict != "used" {
			continue
		}
		name := strings.TrimPrefix(e.detail, "Client.")
		if name == e.detail {
			problems = append(problems, fmt.Sprintf("%s:%d: %s is used, so its reason must name the "+
				"client method as Client.Something; got %q", coverageFile, e.line, e.method, e.detail))
			continue
		}
		if !methods[name] {
			problems = append(problems, fmt.Sprintf("%s:%d: %s names Client.%s, which does not exist in "+
				"internal/gapi", coverageFile, e.line, e.method, name))
			continue
		}
		if first, ok := claimed[name]; ok {
			problems = append(problems, fmt.Sprintf("%s:%d: Client.%s is claimed by both %s and %s",
				coverageFile, e.line, name, first, e.method))
			continue
		}
		claimed[name] = e.method
	}

	unclaimed := make([]string, 0, len(methods))
	for name := range methods {
		if claimed[name] != "" {
			continue
		}
		if _, known := clientMethodsThatAreNotCalls[name]; known {
			continue
		}
		unclaimed = append(unclaimed, name)
	}
	sort.Strings(unclaimed)
	for _, name := range unclaimed {
		problems = append(problems, fmt.Sprintf("Client.%s calls Drive but no entry in %s claims it. "+
			"Add the API method it implements, or list it in clientMethodsThatAreNotCalls with the reason",
			name, coverageFile))
	}
	return problems
}

// apiDiff refetches the discovery document and reports what the record
// does not know about. It reaches the network, so it is a target
// somebody runs rather than a gate CI depends on.
func apiDiff(out io.Writer, _ []string) error {
	entries, problems := readCoverage()
	if len(problems) > 0 {
		for _, p := range problems {
			_, _ = fmt.Fprintln(out, p)
		}
		return fmt.Errorf("%d API coverage problem(s)", len(problems))
	}
	known := map[string]bool{}
	for _, e := range entries {
		known[e.method] = true
	}

	live, err := discoveryMethods()
	if err != nil {
		return err
	}
	var added, removed []string
	for name := range live {
		if !known[name] {
			added = append(added, name)
		}
	}
	for name := range known {
		if !live[name] {
			removed = append(removed, name)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	for _, name := range added {
		_, _ = fmt.Fprintf(out, "NEW: %s is in the discovery document and not in %s\n", name, coverageFile)
	}
	for _, name := range removed {
		_, _ = fmt.Fprintf(out, "GONE: %s is in %s and not in the discovery document\n", name, coverageFile)
	}
	if len(added)+len(removed) > 0 {
		return fmt.Errorf("the Drive API and %s disagree about %d method(s)", coverageFile, len(added)+len(removed))
	}
	_, _ = fmt.Fprintf(out, "api diff ok (%d methods, unchanged)\n", len(live))
	return nil
}

// discoveryDocuments are the three APIs this server can reach, each with
// the prefix its methods carry in the record. Drive's methods have no
// prefix because they are the great majority and the ones §16 named; the
// other two are prefixed so that a name says which API it belongs to.
var discoveryDocuments = []struct {
	prefix string
	url    string
}{
	{"", "https://www.googleapis.com/discovery/v1/apis/drive/v3/rest"},
	{"drivelabels.", "https://drivelabels.googleapis.com/$discovery/rest?version=v2"},
	{"driveactivity.", "https://driveactivity.googleapis.com/$discovery/rest?version=v2"},
}

// discoveryMethods fetches the live method list of all three APIs.
func discoveryMethods() (map[string]bool, error) {
	out := map[string]bool{}
	client := &http.Client{Timeout: 30 * time.Second}
	for _, doc := range discoveryDocuments {
		if err := fetchMethods(client, doc.prefix, doc.url, out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// fetchMethods reads one discovery document into the set.
func fetchMethods(client *http.Client, prefix, url string, out map[string]bool) error {
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("fetch %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s answered %d", url, resp.StatusCode)
	}
	var doc struct {
		Resources map[string]json.RawMessage `json:"resources"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return fmt.Errorf("decode %s: %w", url, err)
	}
	for name, raw := range doc.Resources {
		collectMethods(prefix+name, raw, out)
	}
	return nil
}

// collectMethods walks a resource and its sub-resources, which is how
// replies sits under comments.
func collectMethods(prefix string, raw json.RawMessage, out map[string]bool) {
	var res struct {
		Methods   map[string]json.RawMessage `json:"methods"`
		Resources map[string]json.RawMessage `json:"resources"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return
	}
	for name := range res.Methods {
		out[prefix+"."+name] = true
	}
	for name, sub := range res.Resources {
		collectMethods(prefix+"."+name, sub, out)
	}
}
