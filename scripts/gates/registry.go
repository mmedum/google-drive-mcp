package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"strings"
)

// The MCP registry entry. Without one, the only way to find this server
// is to already know the repository exists — §17a raised that in phase 0
// and it stayed open until a release with a bundle in it existed to
// point at.
//
// The rules below are read from the registry's own validator
// (internal/validators/registries/mcpb.go) rather than from its schema or
// from another repository's description of it, because the schema does
// not carry them: the identifier must be HTTPS, must be a GitHub or
// GitLab release URL, must contain "mcp" somewhere, must not sit beside a
// registryBaseUrl, and must answer a HEAD with 200 or a redirect. The
// last one is why publishing runs after the release exists, and the hash
// is required by the registry and checked by CLIENTS rather than by it.
//
// registryPlaceholder is the version the COMMITTED entry carries, for the
// reason the bundle manifest carries one: a real version in the tree is a
// stale version waiting to be published.
const registryPlaceholder = "0.0.0-dev"

const (
	registryFile = "packaging/registry/server.json"
	// serverName is this server's registry name. The io.github. prefix is
	// what lets GitHub OIDC prove the namespace at publish time.
	serverName = "io.github.mmedum/google-drive-mcp"
	// releaseHost is where the bundle is published, and the only host the
	// registry accepts a GitHub release URL on.
	releaseHost = "github.com"
)

// registryEntry is the part of server.json this repository decides. The
// rest is passed through, so a field the registry adds later survives.
type registryEntry struct {
	Name        string `json:"name"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Version     string `json:"version"`
	Packages    []struct {
		RegistryType    string `json:"registryType"`
		Identifier      string `json:"identifier"`
		FileSha256      string `json:"fileSha256"`
		RegistryBaseURL string `json:"registryBaseUrl"`
	} `json:"packages"`
}

// registry checks the committed entry against the rules the registry
// enforces in code, so a mistake in it fails on the commit that writes it
// rather than at the end of a release.
func registry(out io.Writer, _ []string) error {
	raw, err := os.ReadFile(registryFile)
	if err != nil {
		return err
	}
	entry, problems := checkRegistryEntry(raw, registryPlaceholder)
	if len(problems) > 0 {
		for _, p := range problems {
			_, _ = fmt.Fprintln(out, "  "+p)
		}
		return fmt.Errorf("%d problem(s) in %s", len(problems), registryFile)
	}
	_, _ = fmt.Fprintf(out, "registry entry ok (%s, %d package(s), placeholder version)\n",
		entry.Name, len(entry.Packages))
	return nil
}

// checkRegistryEntry holds an entry to the registry's rules. wantVersion
// is the version it must carry: the placeholder for the committed file,
// and the real one for what the release publishes.
func checkRegistryEntry(raw []byte, wantVersion string) (registryEntry, []string) {
	var entry registryEntry
	if err := json.Unmarshal(raw, &entry); err != nil {
		return entry, []string{"decode " + registryFile + ": " + err.Error()}
	}
	var problems []string
	if entry.Name != serverName {
		problems = append(problems, fmt.Sprintf("name is %q, want %q — the io.github. namespace is what "+
			"GitHub OIDC proves at publish time", entry.Name, serverName))
	}
	if entry.Version != wantVersion {
		problems = append(problems, fmt.Sprintf("version is %q, want %q", entry.Version, wantVersion))
	}
	problems = append(problems, checkRegistryLengths(entry)...)
	if len(entry.Packages) != 1 {
		problems = append(problems, fmt.Sprintf("%d packages; this server publishes exactly one, the bundle",
			len(entry.Packages)))
		return entry, problems
	}

	pkg := entry.Packages[0]
	if pkg.RegistryType != "mcpb" {
		problems = append(problems, fmt.Sprintf("registryType is %q, want \"mcpb\"", pkg.RegistryType))
	}
	// Every one of these is refused by the registry's validator, and none
	// of them by its schema.
	if pkg.RegistryBaseURL != "" {
		problems = append(problems, "registryBaseUrl is set, and an MCPB package must not have one: "+
			"the full download URL goes in identifier")
	}
	if !strings.Contains(strings.ToLower(pkg.Identifier), "mcp") {
		problems = append(problems, "the identifier must contain \"mcp\" somewhere: "+pkg.Identifier)
	}
	if len(pkg.FileSha256) != 64 || strings.Trim(pkg.FileSha256, "0123456789abcdef") != "" {
		problems = append(problems, "fileSha256 is not 64 hex characters: "+pkg.FileSha256)
	}
	problems = append(problems, checkIdentifierURL(pkg.Identifier)...)
	return entry, problems
}

// checkIdentifierURL holds the download URL to what the registry accepts:
// HTTPS, on github.com, and shaped like a release asset.
func checkIdentifierURL(identifier string) []string {
	parsed, err := url.Parse(identifier)
	if err != nil {
		return []string{"identifier is not a URL: " + err.Error()}
	}
	var problems []string
	if parsed.Scheme != "https" {
		problems = append(problems, "identifier must use https: "+identifier)
	}
	if strings.ToLower(parsed.Host) != releaseHost {
		problems = append(problems, fmt.Sprintf("identifier host is %q, want %q", parsed.Host, releaseHost))
	}
	// The shape the registry's isValidGitHubReleaseURL insists on.
	if !strings.Contains(parsed.Path, "/releases/download/") {
		problems = append(problems, "identifier is not a release asset URL (no /releases/download/): "+identifier)
	}
	if !strings.HasSuffix(parsed.Path, ".mcpb") {
		problems = append(problems, "identifier does not name a .mcpb: "+identifier)
	}
	return problems
}

// registryPublish prints the entry to publish for a version, with the
// bundle's real hash and URL in it.
//
// The hash comes from the release's own checksums.txt — the file cosign
// signs — rather than from hashing a local build, so what the registry
// hands a client is the number under the signature. The registry does not
// check the hash itself; MCP clients do, before installing.
func registryPublish(out io.Writer, args []string) error {
	version := strings.TrimPrefix(arg(args, 0, ""), "v")
	dist := arg(args, 1, "dist")
	if version == "" {
		return fmt.Errorf("usage: gates registry-publish VERSION [DIST]")
	}
	raw, err := os.ReadFile(registryFile)
	if err != nil {
		return err
	}
	var entry map[string]any
	if err := json.Unmarshal(raw, &entry); err != nil {
		return fmt.Errorf("decode %s: %w", registryFile, err)
	}
	if got, _ := entry["version"].(string); got != registryPlaceholder {
		return fmt.Errorf("%s carries version %q; the committed entry must carry %q, so that a version "+
			"in the tree can never be a stale one", registryFile, got, registryPlaceholder)
	}

	name := fmt.Sprintf("google-drive-mcp_%s.mcpb", version)
	sum, err := sumFromChecksums(dist+"/checksums.txt", name)
	if err != nil {
		return err
	}
	entry["version"] = version
	packages, _ := entry["packages"].([]any)
	if len(packages) != 1 {
		return fmt.Errorf("%s has %d packages; this server publishes exactly one", registryFile, len(packages))
	}
	pkg, _ := packages[0].(map[string]any)
	pkg["identifier"] = fmt.Sprintf("https://%s/mmedum/google-drive-mcp/releases/download/v%s/%s",
		releaseHost, version, name)
	pkg["fileSha256"] = sum

	written, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return err
	}
	if _, problems := checkRegistryEntry(written, version); len(problems) > 0 {
		for _, p := range problems {
			_, _ = fmt.Fprintln(os.Stderr, "  "+p)
		}
		return fmt.Errorf("%d problem(s) in the entry that would be published", len(problems))
	}
	_, err = out.Write(append(written, '\n'))
	return err
}

// sumFromChecksums reads one file's SHA-256 out of a checksums file.
//
// It refuses a name it cannot find rather than publishing a zero hash: a
// client checks this number before installing, so a wrong one is an
// install that fails for everybody, and an absent bundle is the packing
// step having silently not run.
func sumFromChecksums(path, name string) (string, error) {
	f, err := os.Open(path) //nolint:gosec // a path inside the release's own output
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()

	scan := bufio.NewScanner(f)
	for scan.Scan() {
		sum, file, ok := strings.Cut(strings.TrimSpace(scan.Text()), "  ")
		if ok && file == name {
			return sum, nil
		}
	}
	if err := scan.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("%s names no %s; the bundle was not packed, or not covered by the checksums "+
		"file and therefore not under the signature either", path, name)
}

// checkRegistryLengths holds the entry to the limits in the PUBLISHED
// SCHEMA, which are a different set from the rules in the validator.
//
// This gate was written to catch the rules the schema does not carry, and
// in aiming at those it skipped the ones it does. The first entry this
// repository tried to publish was refused with `body.description:
// expected length <= 100` after the OIDC login had succeeded — a
// 205-character description that this gate had just passed. The schema
// and the validator each carry rules the other does not, and checking one
// is not checking the other.
//
// The numbers are transcribed from
// static.modelcontextprotocol.io/schemas/2025-12-11/server.schema.json
// rather than fetched, because a gate that reaches the network fails when
// somebody else's CDN is slow and CI learns to ignore it. §18 records
// where they came from and when.
func checkRegistryLengths(entry registryEntry) []string {
	var problems []string
	limit := func(field, value string, minLen, maxLen int) {
		switch {
		case len(value) < minLen:
			problems = append(problems, fmt.Sprintf("%s is %d characters, and the schema wants at least %d",
				field, len(value), minLen))
		case len(value) > maxLen:
			problems = append(problems, fmt.Sprintf(
				"%s is %d characters, and the schema allows at most %d. The registry refuses this at "+
					"publish time, after the login has succeeded", field, len(value), maxLen))
		}
	}
	limit("description", entry.Description, 1, 100)
	limit("title", entry.Title, 1, 100)
	limit("name", entry.Name, 3, 200)
	limit("version", entry.Version, 1, 255)
	if !serverNamePattern.MatchString(entry.Name) {
		problems = append(problems, "name does not match the schema's pattern: "+entry.Name)
	}
	return problems
}

// serverNamePattern is the schema's own, transcribed with its source.
var serverNamePattern = regexp.MustCompile(`^[a-zA-Z0-9.-]+/[a-zA-Z0-9._-]+$`)
