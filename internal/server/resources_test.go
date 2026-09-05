package server_test

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestResourceTemplatesAreListed(t *testing.T) {
	cs := session(t, defaultConfig(), true)
	res, err := cs.ListResourceTemplates(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListResourceTemplates: %v", err)
	}
	want := map[string]bool{
		"gdrive://{file}":            false,
		"gdrive://{file}/meta":       false,
		"gdrive://{folder}/children": false,
	}
	for _, tmpl := range res.ResourceTemplates {
		if _, ok := want[tmpl.URITemplate]; !ok {
			t.Errorf("unexpected template %q", tmpl.URITemplate)
			continue
		}
		want[tmpl.URITemplate] = true
		if tmpl.Name == "" || tmpl.Description == "" {
			t.Errorf("%s has no name or description", tmpl.URITemplate)
		}
	}
	for tmpl, found := range want {
		if !found {
			t.Errorf("%s is not registered", tmpl)
		}
	}
	// A static list would be a listing of somebody's whole Drive, so
	// there is none.
	list, err := cs.ListResources(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(list.Resources) != 0 {
		t.Errorf("the server lists %d static resources, and should list none", len(list.Resources))
	}
}

func TestReadingTheThreeResources(t *testing.T) {
	cs, fake := sessionAndFake(t, defaultConfig(), true)
	fake.SetContent("id-notes-fixture", "# Notes\n\nthe text of the document\n")

	for _, tc := range []struct {
		uri, wantMime, wantText string
	}{
		{"gdrive://id-notes-fixture", "text/markdown", "the text of the document"},
		{"gdrive://id-notes-fixture/meta", "text/plain", "Meeting notes"},
		{"gdrive://id-projects-fixture/children", "text/plain", "Meeting notes"},
	} {
		t.Run(tc.uri, func(t *testing.T) {
			res, err := cs.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: tc.uri})
			if err != nil {
				t.Fatalf("ReadResource: %v", err)
			}
			if len(res.Contents) != 1 {
				t.Fatalf("read returned %d contents, want one", len(res.Contents))
			}
			got := res.Contents[0]
			if got.URI != tc.uri {
				t.Errorf("uri = %q, want %q", got.URI, tc.uri)
			}
			if got.MIMEType != tc.wantMime {
				t.Errorf("mime = %q, want %q", got.MIMEType, tc.wantMime)
			}
			if !strings.Contains(got.Text, tc.wantText) {
				t.Errorf("text does not carry %q:\n%s", tc.wantText, got.Text)
			}
		})
	}
}

// TestTemplatesWithASharedPrefixDoNotShadowEachOther is the property §8
// rests on. It is asserted through the server rather than by reading the
// SDK: what matters is that the URI with a suffix reaches the handler
// for that suffix, whatever the SDK does internally to decide it.
func TestTemplatesWithASharedPrefixDoNotShadowEachOther(t *testing.T) {
	cs, _ := sessionAndFake(t, defaultConfig(), true)
	text, err := cs.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: "gdrive://id-notes-fixture"})
	if err != nil {
		t.Fatalf("plain: %v", err)
	}
	card, err := cs.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: "gdrive://id-notes-fixture/meta"})
	if err != nil {
		t.Fatalf("meta: %v", err)
	}
	if text.Contents[0].Text == card.Contents[0].Text {
		t.Error("gdrive://{file} and gdrive://{file}/meta answered with the same thing")
	}
	if !strings.Contains(card.Contents[0].Text, "location:") {
		t.Errorf("the /meta suffix did not reach the card handler:\n%s", card.Contents[0].Text)
	}
}

// TestAPathReferenceIsPercentEncoded records the consequence of using a
// simple expansion: a reference with a slash in it has to arrive
// encoded, and one that does not is no resource at all rather than a
// truncated one.
func TestAPathReferenceIsPercentEncoded(t *testing.T) {
	cs, fake := sessionAndFake(t, defaultConfig(), true)
	fake.SetContent("id-notes-fixture", "the text this path leads to\n")
	res, err := cs.ReadResource(context.Background(), &mcp.ReadResourceParams{
		URI: "gdrive://" + "%2FProjects%2FMeeting%20notes",
	})
	if err != nil {
		t.Fatalf("an encoded path was not read: %v", err)
	}
	if !strings.Contains(res.Contents[0].Text, "the text this path leads to") {
		t.Errorf("the encoded path resolved to something else:\n%s", res.Contents[0].Text)
	}
	if _, err := cs.ReadResource(context.Background(), &mcp.ReadResourceParams{
		URI: "gdrive:///Projects/Meeting notes",
	}); err == nil {
		t.Error("an unencoded path matched a template, which would silently read the wrong thing")
	}
}

func TestResourceRefusalsCarryTheAdviceTheToolsGive(t *testing.T) {
	cs, _ := sessionAndFake(t, defaultConfig(), true)
	for _, tc := range []struct{ uri, want string }{
		// A folder has no text, and the message names the resource that
		// does answer for it.
		{"gdrive://id-projects-fixture", "/children"},
		// And the other way round.
		{"gdrive://id-notes-fixture/children", "not a folder"},
		{"gdrive://1NoSuchFileIdFixtureAAAAAAAAAAAAAAA", "search_files finds it by name"},
	} {
		t.Run(tc.uri, func(t *testing.T) {
			_, err := cs.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: tc.uri})
			if err == nil {
				t.Fatal("the read succeeded")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal does not say %q: %v", tc.want, err)
			}
		})
	}
}

// TestATextResourceIsTheContentAndNothingElse is the fix for a review
// finding: the body used to be read_file's whole result, header and all,
// while the media type said text/csv. A client that fed those bytes to a
// csv parser got five lines of prose first.
func TestATextResourceIsTheContentAndNothingElse(t *testing.T) {
	cs, fake := sessionAndFake(t, defaultConfig(), true)
	fake.AddFile("id-rows-fixture", "rows.csv", "text/csv", "id-projects-fixture")
	fake.SetContent("id-rows-fixture", "name,amount\nfirst,1\n")

	res, err := cs.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: "gdrive://id-rows-fixture"})
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	got := res.Contents[0]
	if got.MIMEType != "text/csv" {
		t.Errorf("mime = %q", got.MIMEType)
	}
	if got.Text != "name,amount\nfirst,1\n" {
		t.Errorf("the body is not the file's bytes:\n%q", got.Text)
	}
	// The description the header used to carry is the other template's
	// whole reason to exist, so it has to be there.
	card, err := cs.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: "gdrive://id-rows-fixture/meta"})
	if err != nil {
		t.Fatalf("ReadResource meta: %v", err)
	}
	for _, want := range []string{"rows.csv", "location:"} {
		if !strings.Contains(card.Contents[0].Text, want) {
			t.Errorf("the card does not carry %q:\n%s", want, card.Contents[0].Text)
		}
	}
}

func TestSchemaDumpCarriesTheResourceTemplates(t *testing.T) {
	// The dump is what a release diffs against the last tag, so a
	// template leaving the surface has to show up in it.
	cs := session(t, defaultConfig(), true)
	res, err := cs.ListResourceTemplates(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListResourceTemplates: %v", err)
	}
	if len(res.ResourceTemplates) != 3 {
		t.Fatalf("listed %d templates, want 3", len(res.ResourceTemplates))
	}
}
