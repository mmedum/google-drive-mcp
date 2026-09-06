package model

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/mmedum/google-drive-mcp/internal/gdrive"
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
