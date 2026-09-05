package ref

import (
	"errors"
	"strings"
	"testing"
)

// A synthetic id in the shape Drive uses. Nothing here is a real id.
const testID = "1SyntheticFixtureFileIdAAAAAAAAAAAA"

func TestParseIDs(t *testing.T) {
	for _, in := range []string{testID, "id-projects-fixture", "0ABcdEFghIJklMNop"} {
		r, err := Parse(in)
		if err != nil {
			t.Fatalf("Parse(%q): %v", in, err)
		}
		if r.Kind != KindID || r.ID != in {
			t.Errorf("Parse(%q) = %+v", in, r)
		}
		if r.IsPath() {
			t.Errorf("Parse(%q) should not need resolving", in)
		}
	}
}

func TestParseRoot(t *testing.T) {
	for _, in := range []string{"root", "ROOT", "my_drive", "My Drive", "my-drive", "mydrive"} {
		r, err := Parse(in)
		if err != nil {
			t.Fatalf("Parse(%q): %v", in, err)
		}
		if r.Kind != KindRoot {
			t.Errorf("Parse(%q) = %+v, want the root", in, r)
		}
		if r.Display() != "My Drive" {
			t.Errorf("Display = %q", r.Display())
		}
	}
}

func TestParseURLShapes(t *testing.T) {
	cases := []struct {
		in   string
		key  string
		name string
	}{
		{"https://drive.google.com/file/d/" + testID + "/view", "", "file view"},
		{"https://drive.google.com/file/d/" + testID + "/edit?usp=sharing", "", "file edit"},
		{"https://drive.google.com/file/d/" + testID + "/view?usp=sharing&resourcekey=0-abc", "0-abc", "file with resource key"},
		{"https://drive.google.com/drive/folders/" + testID, "", "folder"},
		{"https://drive.google.com/drive/u/0/folders/" + testID, "", "folder with account switcher"},
		{"https://drive.google.com/drive/folders/" + testID + "?resourcekey=0-xyz", "0-xyz", "folder with resource key"},
		{"https://drive.google.com/open?id=" + testID, "", "open link"},
		{"https://drive.google.com/open?id=" + testID + "&resourcekey=0-k", "0-k", "open link with key"},
		{"https://drive.google.com/uc?id=" + testID + "&export=download", "", "download link"},
		{"https://docs.google.com/document/d/" + testID + "/edit", "", "doc"},
		{"https://docs.google.com/document/d/" + testID + "/edit#heading=h.abc", "", "doc with fragment"},
		{"https://docs.google.com/spreadsheets/d/" + testID + "/edit#gid=0", "", "sheet"},
		{"https://docs.google.com/presentation/d/" + testID + "/edit", "", "slides"},
		{"https://docs.google.com/forms/d/" + testID + "/edit", "", "form"},
		{"https://docs.google.com/drawings/d/" + testID + "/edit", "", "drawing"},
		{"https://docs.google.com/document/u/1/d/" + testID + "/edit", "", "doc with account switcher"},
		{"https://docs.google.com/spreadsheets/d/" + testID + "/edit?resourcekey=0-q", "0-q", "sheet with key"},
	}
	for _, c := range cases {
		r, err := Parse(c.in)
		if err != nil {
			t.Errorf("%s: Parse(%q): %v", c.name, c.in, err)
			continue
		}
		if r.Kind != KindID || r.ID != testID {
			t.Errorf("%s: got %+v", c.name, r)
		}
		if r.ResourceKey != c.key {
			t.Errorf("%s: resource key = %q, want %q", c.name, r.ResourceKey, c.key)
		}
	}
}

func TestParseSharedDriveURL(t *testing.T) {
	r, err := Parse("https://drive.google.com/drive/u/0/drives/" + testID)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if r.Kind != KindDrivePath || r.Drive != testID || len(r.Segments) != 0 {
		t.Fatalf("got %+v", r)
	}
}

func TestParseMyDriveURL(t *testing.T) {
	for _, in := range []string{
		"https://drive.google.com/drive/my-drive",
		"https://drive.google.com/drive/u/0/my-drive",
		"https://drive.google.com/drive/home",
	} {
		r, err := Parse(in)
		if err != nil {
			t.Fatalf("Parse(%q): %v", in, err)
		}
		if r.Kind != KindRoot {
			t.Errorf("Parse(%q) = %+v", in, r)
		}
	}
}

func TestParsePaths(t *testing.T) {
	r, err := Parse("/Projects/2026/Budget.xlsx")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if r.Kind != KindPath {
		t.Fatalf("kind = %v", r.Kind)
	}
	if strings.Join(r.Segments, "|") != "Projects|2026|Budget.xlsx" {
		t.Errorf("segments = %v", r.Segments)
	}
	if !r.IsPath() {
		t.Error("a path needs resolving")
	}
	if r.Display() != "/Projects/2026/Budget.xlsx" {
		t.Errorf("Display = %q", r.Display())
	}
}

func TestParsePathWithoutLeadingSlash(t *testing.T) {
	r, err := Parse("Projects/2026")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if r.Kind != KindPath || len(r.Segments) != 2 {
		t.Errorf("got %+v", r)
	}
}

func TestParseBareNameIsASingleSegmentPath(t *testing.T) {
	// A name that is not id-shaped is what a person means by it: one
	// segment under My Drive, resolved out loud.
	r, err := Parse("Budget.xlsx")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if r.Kind != KindPath || len(r.Segments) != 1 || r.Segments[0] != "Budget.xlsx" {
		t.Errorf("got %+v", r)
	}
}

func TestParseTolerantPaths(t *testing.T) {
	r, err := Parse("//Projects//2026/")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if strings.Join(r.Segments, "|") != "Projects|2026" {
		t.Errorf("segments = %v", r.Segments)
	}
}

func TestParseDrivePaths(t *testing.T) {
	r, err := Parse("drive:Marketing/Campaigns/Q3")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if r.Kind != KindDrivePath || r.Drive != "Marketing" {
		t.Fatalf("got %+v", r)
	}
	if strings.Join(r.Segments, "|") != "Campaigns|Q3" {
		t.Errorf("segments = %v", r.Segments)
	}
	if r.Display() != "Marketing/Campaigns/Q3" {
		t.Errorf("Display = %q", r.Display())
	}
}

func TestParseDriveRootOnly(t *testing.T) {
	r, err := Parse("drive:Marketing")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if r.Kind != KindDrivePath || r.Drive != "Marketing" || len(r.Segments) != 0 {
		t.Fatalf("got %+v", r)
	}
	if r.Display() != "Marketing" {
		t.Errorf("Display = %q", r.Display())
	}
}

func TestParseDrivePathByID(t *testing.T) {
	r, err := Parse("drive:" + testID + "/Q3")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if r.Drive != testID || len(r.Segments) != 1 {
		t.Errorf("got %+v", r)
	}
}

func TestParseRejections(t *testing.T) {
	cases := map[string]string{
		"":                              "empty",
		"   ":                           "empty",
		"drive:":                        "no shared drive named",
		"/Projects/../etc":              "no meaning in Drive",
		"https://example.com/file/d/x":  "only drive.google.com and docs.google.com",
		"https://drive.google.com/":     "does not contain a file",
		"https://drive.google.com/open": "no id parameter",
		"https://docs.google.com/document/d/short/edit":                "not a Drive id",
		"https://drive.google.com/drive/shared-drives":                 "list of shared drives",
		"https://docs.google.com/spreadsheets/d/e/2PACX-1vабв/pubhtml": "published-to-web",
		"https://sites.google.com/view/thing":                          "only drive.google.com",
	}
	for in, want := range cases {
		_, err := Parse(in)
		if err == nil {
			t.Errorf("Parse(%q) should fail", in)
			continue
		}
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Parse(%q) error = %v, want it to mention %q", in, err, want)
		}
	}
}

func TestParseErrorsCarryTheInput(t *testing.T) {
	_, err := Parse("https://example.com/x")
	var pe *ErrParse
	if !asErrParse(err, &pe) {
		t.Fatalf("err = %T, want *ErrParse", err)
	}
	if pe.Input != "https://example.com/x" {
		t.Errorf("Input = %q", pe.Input)
	}
	if !strings.Contains(err.Error(), Help) {
		t.Errorf("the error should list the accepted forms: %v", err)
	}
}

func asErrParse(err error, target **ErrParse) bool { return errors.As(err, target) }

func TestIsID(t *testing.T) {
	// Drive issues file ids of 28-44 characters and shared-drive ids of 19.
	for _, s := range []string{testID, "0ABcdEFghIJklMNop", "id-projects-fixture"} {
		if !IsID(s) {
			t.Errorf("IsID(%q) should be true", s)
		}
	}
	// Nothing shorter than a real id, and nothing carrying a character an
	// id cannot hold, is taken for one; those are names.
	for _, s := range []string{"short", "Budget2026", "has space", "has/slash", "has.dot", ""} {
		if IsID(s) {
			t.Errorf("IsID(%q) should be false", s)
		}
	}
}

func TestBareWordThatIsNotIDShapedIsAName(t *testing.T) {
	// Names of this length are common; ids of it do not exist. Reading
	// them as a path is what a person means, and the service still tries
	// an id first when the word could be one.
	r, err := Parse("Budget2026")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if r.Kind != KindPath {
		t.Errorf("Parse(%q) = %v, want a path", "Budget2026", r.Kind)
	}
}

func TestIDFromAURLIsMarked(t *testing.T) {
	r, err := Parse("https://drive.google.com/file/d/" + testID + "/view")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !r.FromURL {
		t.Error("an id lifted out of a URL is certainly an id and should be marked")
	}
	bare, _ := Parse(testID)
	if bare.FromURL {
		t.Error("a bare token could also have been a name and must not be marked")
	}
}

func TestKindString(t *testing.T) {
	want := map[Kind]string{KindID: "id", KindRoot: "my drive root", KindPath: "path", KindDrivePath: "shared drive path", Kind(99): "unknown"}
	for k, s := range want {
		if got := k.String(); got != s {
			t.Errorf("Kind(%d).String() = %q, want %q", k, got, s)
		}
	}
}

func TestDisplayOfAnID(t *testing.T) {
	r, _ := Parse(testID)
	if r.Display() != testID {
		t.Errorf("Display = %q", r.Display())
	}
}
