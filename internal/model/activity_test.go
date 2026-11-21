package model

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/mmedum/google-drive-mcp/v3/internal/gdrive"
)

// TestNamedMembersMatchesTheWireType holds namedMembers to the type it
// is about, in both directions.
//
// namedMembers exists so UnnamedMembers can answer without re-deriving
// the switch below it, and a hand-written list beside a switch is the
// drift this repository keeps finding. Here the drift has teeth: a kind
// added to ActionDetail and not to the map would be reported as one
// Google grew, which is the false alarm this whole area exists to stop.
func TestNamedMembersMatchesTheWireType(t *testing.T) {
	fields := reflect.TypeOf(gdrive.ActionDetail{})
	onTheWire := map[string]bool{}
	for i := range fields.NumField() {
		tag, _, _ := strings.Cut(fields.Field(i).Tag.Get("json"), ",")
		// Members carries no wire name; it is what the decoder keeps.
		if tag == "" || tag == "-" {
			continue
		}
		onTheWire[tag] = true
	}
	if !reflect.DeepEqual(onTheWire, namedMembers) {
		t.Errorf("namedMembers and ActionDetail's fields disagree:\nfields: %v\nmap:    %v",
			keysOf(onTheWire), keysOf(namedMembers))
	}
}

// TestEveryNamedMemberHasAWord ties the map to the switch: a name in
// namedMembers that actionWords falls through would make UnnamedMembers
// call a describable action undescribable, and the entry would vanish
// from the listing rather than be reported.
func TestEveryNamedMemberHasAWord(t *testing.T) {
	for name := range namedMembers {
		var d gdrive.ActionDetail
		body := fmt.Sprintf(`{%q:{}}`, name)
		if err := json.Unmarshal([]byte(body), &d); err != nil {
			t.Fatalf("decode %s: %v", body, err)
		}
		if what, _ := actionWords(&d); what == "" {
			t.Errorf("%q is in namedMembers but actionWords has no word for it", name)
		}
		if got := UnnamedMembers(&d); len(got) != 0 {
			t.Errorf("%q is named but UnnamedMembers reported %v", name, got)
		}
	}
}

// TestUnnamedMembersReportsOnlyWhatItCannotName is the case the guard is
// for: a member Google adds after this server was written.
func TestUnnamedMembersReportsOnlyWhatItCannotName(t *testing.T) {
	var d gdrive.ActionDetail
	if err := json.Unmarshal([]byte(`{"edit":{},"approvalChange":{}}`), &d); err != nil {
		t.Fatalf("decode: %v", err)
	}
	got := UnnamedMembers(&d)
	if len(got) != 1 || got[0] != "approvalChange" {
		t.Errorf("want only approvalChange reported, got %v", got)
	}
	if UnnamedMembers(nil) != nil {
		t.Error("a nil detail reported members")
	}
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// What an activity says happened, decoded from the detail Google sends.
func TestActivityWordsForDeletesAndSharing(t *testing.T) {
	for _, c := range []struct {
		wire         string
		what, detail string
	}{
		{`{"delete":{"type":"PERMANENT_DELETE"}}`, "deleted permanently", ""},
		{`{"delete":{"type":"TRASH"}}`, "trashed", ""},
		{`{"permissionChange":{"addedPermissions":[{},{}],"removedPermissions":[{}]}}`, "changed who can see it", "2 grants added, 1 grant removed"},
		{`{"permissionChange":{"addedPermissions":[{}]}}`, "changed who can see it", "1 grant added"},
		{`{"permissionChange":{"removedPermissions":[{}]}}`, "changed who can see it", "1 grant removed"},
		{`{"permissionChange":{}}`, "changed who can see it", ""},
	} {
		var d gdrive.ActionDetail
		if err := json.Unmarshal([]byte(c.wire), &d); err != nil {
			t.Fatalf("decode %s: %v", c.wire, err)
		}
		a, _ := NewActivity(&gdrive.DriveActivity{PrimaryActionDetail: &d})
		if a == nil {
			t.Errorf("%s: no activity", c.wire)
			continue
		}
		if a.What != c.what || a.Detail != c.detail {
			t.Errorf("%s: got %q / %q, want %q / %q", c.wire, a.What, a.Detail, c.what, c.detail)
		}
	}
}

// A kind Google adds is named rather than read as the nearest known one:
// a new way to create is still a create, and says which; a new kind of
// actor or person says what it is called.
func TestAnUnknownKindIsNamedNotGuessed(t *testing.T) {
	for _, tc := range []struct {
		name, json, what, detail, who string
	}{
		{"a create Google added", `{"primaryActionDetail":{"create":{"fromTemplate":{}}},` +
			`"actors":[{"user":{"knownUser":{"isCurrentUser":true}}}]}`,
			"created", "by a kind of create this server has no word for: fromTemplate", "you"},
		{"a plain create", `{"primaryActionDetail":{"create":{"new":{}}},"actors":[{"system":{}}]}`,
			"created", "", "Drive itself"},
		{"an actor Google added", `{"primaryActionDetail":{"edit":{}},"actors":[{"robot":{}}]}`,
			"edited", "", "an actor of a kind this server has no word for: robot"},
		{"a person Google cannot identify", `{"primaryActionDetail":{"edit":{}},"actors":[{"user":{"unknownUser":{}}}]}`,
			"edited", "", "somebody Drive cannot identify"},
		{"a kind of person Google added", `{"primaryActionDetail":{"edit":{}},"actors":[{"user":{"guestUser":{}}}]}`,
			"edited", "", "a person, of a kind this server has no word for: guestUser"},
		{"no actor at all", `{"primaryActionDetail":{"edit":{}}}`, "edited", "", "somebody"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var a gdrive.DriveActivity
			if err := json.Unmarshal([]byte(tc.json), &a); err != nil {
				t.Fatal(err)
			}
			got, _ := NewActivity(&a)
			if got == nil || got.What != tc.what || got.Detail != tc.detail || got.Who != tc.who {
				t.Errorf("NewActivity = %+v, want what %q, detail %q, who %q", got, tc.what, tc.detail, tc.who)
			}
		})
	}
}

// An actor the API identifies wins over one it does not, wherever each
// stands in the list, and a member that decoded from null is not a kind
// Google added.
func TestTheIdentifiedActorIsTheAnswer(t *testing.T) {
	for _, tc := range []struct{ name, actors, who string }{
		{"an unidentified person first", `[{"user":{"unknownUser":{}}},{"user":{"knownUser":{"isCurrentUser":true}}}]`, "you"},
		{"an unknown kind first", `[{"robot":{}},{"system":{}}]`, "Drive itself"},
		{"an empty person first", `[{"user":{}},{"administrator":{}}]`, "an administrator"},
		{"a known member sent as null", `[{"user":null}]`, "somebody"},
		{"only an unidentified person", `[{"user":{"unknownUser":{}}}]`, "somebody Drive cannot identify"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var a gdrive.DriveActivity
			if err := json.Unmarshal([]byte(`{"primaryActionDetail":{"edit":{}},"actors":`+tc.actors+`}`), &a); err != nil {
				t.Fatal(err)
			}
			if got, _ := NewActivity(&a); got == nil || got.Who != tc.who {
				t.Errorf("Who = %+v, want %q", got, tc.who)
			}
		})
	}
	var a gdrive.DriveActivity
	if err := json.Unmarshal([]byte(`{"primaryActionDetail":{"create":{"copy":{},"fromTemplate":{}}}}`), &a); err != nil {
		t.Fatal(err)
	}
	if got, _ := NewActivity(&a); got == nil || got.What != "copied" ||
		got.Detail != "by a kind of create this server has no word for: fromTemplate" {
		t.Errorf("a copy that also carries an unknown kind = %+v, want copied, with the kind named", got)
	}
}
