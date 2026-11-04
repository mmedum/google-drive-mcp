# Releasing

A maintainer releases by pushing a tag (D-81). `release.yml` runs the
tests and builds with GoReleaser for Linux, macOS and Windows on amd64
and arm64. It publishes a release, never a draft, with the tag's
CHANGELOG section as its notes. The archives, an SBOM each and the
`.mcpb` bundle are all in the cosign-signed `checksums.txt` and carry a
build provenance attestation (D-82, D-84). Last, `publish-mcp.yml`
publishes the MCP registry entry (D-83).

## Before the release commit

- `make check` passes, and CI is green on Linux, macOS and Windows
  (D-78).
- `make live` has run with the modes the release touches, and its
  transcript was read (D-70). `make evals` has run if the release
  changes what a model reads.
- `/simplify`, `/code-review medium` and `/security-review` have run
  since the previous tag.
- `make api-diff` and `make schema-refetch`. Both reach the network, so
  neither is in `make check`. `api-diff` rewrites
  `testdata/api-methods.json`, and a method Google added fails
  `make api-coverage` until `testdata/api-coverage.tsv` gives it a
  verdict (D-69). `schema-refetch` checks the vendored schemas against
  what their sources serve.
- `go run ./scripts/gates leaks history`. It walks every blob in every
  commit, which `make check` does not, and a file removed from the tip
  is still in the history (D-07).
- Rehearse the build into `dist/`, publishing nothing. It skips
  signing, which needs the workflow's identity, and SBOMs, since syft
  is installed only in CI:

  ```bash
  go run github.com/goreleaser/goreleaser/v2@v2.18.0 release --snapshot --clean --skip=sign,sbom,publish
  ```

  The version must agree in the bundle's and the archives' filenames,
  `--version` and `checksums.txt`. A bundle missing from
  `checksums.txt` ships unsigned and looks no different.

## Steps

1. On a topic branch, rename `[Unreleased]` in `CHANGELOG.md` to
   `## [X.Y.Z] - YYYY-MM-DD` and leave an empty `[Unreleased]` above it.
   `make staleness` accepts the notes under a version with no tag yet
   (D-66).
2. In the same commit, run `make schema-baseline VERSION=vX.Y.Z`;
   `make schema-diff` fails until it is done. It refuses a build stamped
   with another version, and a break unless the release is a new major
   version, with go.mod's `/vN` raised (D-64).
3. Commit as `Release X.Y.Z`, open a pull request, and merge once CI is
   green on that commit.
4. Tag the merge, annotated, and push the tag on its own:

   ```bash
   git switch main && git pull --ff-only
   git tag -a vX.Y.Z -m "vX.Y.Z"
   git push origin vX.Y.Z
   ```

   GitHub creates no tag events past the third in one push. If the
   release never starts, run `release.yml` by hand on the tag.

## What no rehearsal reaches

Signing, attestation and the registry publish need the workflow's OIDC
token, so read the first run after any change to the pipeline.

| Fails | State afterward | Recovery |
|---|---|---|
| tests, cosign or SBOMs | no release | fix it, then release the next patch |
| attestation | release published, unattested | fix it, then release the next patch |
| registry publish | release fine, no entry | `gh workflow run publish-mcp.yml --ref main -f tag=vX.Y.Z`; do not tag again |

## Verify from outside

```bash
cd "$(mktemp -d)" && gh release download vX.Y.Z --repo mmedum/google-drive-mcp
sha256sum -c checksums.txt --ignore-missing
cosign verify-blob checksums.txt --bundle checksums.txt.bundle \
  --certificate-identity 'https://github.com/mmedum/google-drive-mcp/.github/workflows/release.yml@refs/tags/vX.Y.Z' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
gh attestation verify google-drive-mcp_X.Y.Z_linux_amd64.tar.gz --repo mmedum/google-drive-mcp
gh attestation verify google-drive-mcp_X.Y.Z.mcpb --repo mmedum/google-drive-mcp
```

A pass prints little, so read the exit code. Last, run the README's
`go install` command outside the repository, as a user would, and check
that `google-drive-mcp --version` names the release.

## A bad release

A bad release is fixed forward.

- Never move or delete a pushed tag. The Go module proxy caches it
  within minutes, and the checksum database records its hash.
- Ship a patch release with the fix.
- In that patch, add `retract vX.Y.Z // why` to `go.mod`, so the go
  command skips the bad version for `@latest`.
- Mark the bad version's CHANGELOG heading `[YANKED]`:
  `## [X.Y.Z] - YYYY-MM-DD [YANKED]`.
