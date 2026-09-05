# google-drive-mcp

A production-grade [Model Context Protocol](https://modelcontextprotocol.io)
server for Google Drive, written in Go: find files and know where they
live and who can see them, organise folders, move and copy, get content
in and out, share without widening access by accident, follow what
changed, and manage shared drives. It stops at the file boundary; what
happens inside a Google Doc, Sheet or Slides deck is out of scope.

**Status: design.** There is no code yet. The design, the decided
constraints, the evidence log and the phase plan are in
[docs/architecture.md](docs/architecture.md). Phase 0 starts on an
explicit go.

## Licence

Apache-2.0.
