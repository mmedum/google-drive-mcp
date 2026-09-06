package service_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mmedum/google-drive-mcp/internal/gapi"
	"github.com/mmedum/google-drive-mcp/internal/gapi/drivetest"
	"github.com/mmedum/google-drive-mcp/internal/gdrive"
	"github.com/mmedum/google-drive-mcp/internal/service"
)

// labelled builds a service with labels on and one published definition
// in the fake: a text field and a selection field with two choices,
// which is enough to exercise every branch that depends on a field's
// type without inventing five labels.
func labelled(t *testing.T) (*service.Service, *drivetest.Server) {
	t.Helper()
	svc, fake := setup(t, service.Options{Labels: true})
	fake.LabelsEnabled = true
	fake.AddLabelDefinition("id-label-review", "Review state",
		drivetest.TextFieldDefinition("id-field-owner", "Owner"),
		drivetest.SelectionFieldDefinition("id-field-stage", "Stage",
			[2]string{"id-choice-draft", "Draft"}, [2]string{"id-choice-final", "Final"}))
	return svc, fake
}

func TestListLabelsShowsFieldIDsAndTheChoicesAFieldTakes(t *testing.T) {
	svc, _ := labelled(t)

	out, err := svc.ListLabels(t.Context(), service.ListLabelsInput{})
	if err != nil {
		t.Fatalf("ListLabels: %v", err)
	}
	// The ids are the whole point: they appear nowhere else, and
	// manage_labels cannot be called without them.
	for _, want := range []string{"Review state", "id-label-review", "id-field-owner", "id-field-stage",
		"id-choice-draft", "Draft", "id-choice-final", "apply this label and remove it"} {
		if !strings.Contains(out, want) {
			t.Errorf("the listing does not carry %q:\n%s", want, out)
		}
	}
}

func TestListLabelsLeavesOutADraftAndSaysSo(t *testing.T) {
	svc, fake := labelled(t)
	draft := fake.AddLabelDefinition("id-label-draft", "Not published yet")
	draft.Lifecycle = &gdrive.LabelLifecycle{State: "UNPUBLISHED_DRAFT"}

	out, err := svc.ListLabels(t.Context(), service.ListLabelsInput{})
	if err != nil {
		t.Fatalf("ListLabels: %v", err)
	}
	if strings.Contains(out, "id-label-draft") {
		t.Errorf("a draft label was offered, and it cannot be applied:\n%s", out)
	}
	if !strings.Contains(out, "not published") {
		t.Errorf("the listing dropped a label without saying so, so the count disagrees with the console:\n%s", out)
	}
}

func TestManageLabelsAppliesSetsAndRemoves(t *testing.T) {
	svc, fake := labelled(t)
	const file = "id-budget-fixture"

	if _, err := svc.ManageLabels(t.Context(), service.ManageLabelsInput{
		File: file, Label: "id-label-review", Action: "apply",
	}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got := len(fake.FileLabels[file]); got != 1 {
		t.Fatalf("after apply the file carries %d labels, want 1", got)
	}

	res, err := svc.ManageLabels(t.Context(), service.ManageLabelsInput{
		File: file, Label: "id-label-review", Action: "set_field",
		Field: "id-field-stage", Values: []string{"id-choice-final"},
	})
	if err != nil {
		t.Fatalf("set_field: %v", err)
	}
	// The result names the label and the choice in words, not in ids: an
	// id is what you type, a display name is what you read.
	for _, want := range []string{"Review state", "Stage", "Final"} {
		if !strings.Contains(res.Text, want) {
			t.Errorf("the result does not carry %q:\n%s", want, res.Text)
		}
	}

	if _, err := svc.ManageLabels(t.Context(), service.ManageLabelsInput{
		File: file, Label: "id-label-review", Action: "remove",
	}); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if got := len(fake.FileLabels[file]); got != 0 {
		t.Fatalf("after remove the file carries %d labels, want 0", got)
	}
}

// A selection field's choices are generated ids. A wrong one has to be
// answered with the list, because there is nowhere else to find it.
func TestManageLabelsRefusesAChoiceThatIsNotOfferedAndNamesTheOnesThatAre(t *testing.T) {
	svc, _ := labelled(t)
	_, err := svc.ManageLabels(t.Context(), service.ManageLabelsInput{
		File: "id-budget-fixture", Label: "id-label-review", Action: "set_field",
		Field: "id-field-stage", Values: []string{"final"},
	})
	if err == nil {
		t.Fatal("a choice the definition does not offer was sent to Drive")
	}
	for _, want := range []string{"id-choice-draft", "id-choice-final"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name the choice %q that would have worked: %v", want, err)
		}
	}
}

func TestManageLabelsRefusesAFieldTheLabelDoesNotHave(t *testing.T) {
	svc, _ := labelled(t)
	_, err := svc.ManageLabels(t.Context(), service.ManageLabelsInput{
		File: "id-budget-fixture", Label: "id-label-review", Action: "set_field",
		Field: "id-field-absent", Values: []string{"x"},
	})
	if err == nil {
		t.Fatal("a field the label does not have was sent to Drive")
	}
	if !strings.Contains(err.Error(), "id-field-owner") {
		t.Errorf("the refusal does not name the fields that do exist: %v", err)
	}
}

// The setter has to match the field's type, and the type is only in the
// definition. Picking the wrong one is a refusal from Drive that names
// neither the type nor the right setter, so it is checked here.
func TestManageLabelsSendsTheSetterThatMatchesTheFieldType(t *testing.T) {
	svc, fake := labelled(t)
	if _, err := svc.ManageLabels(t.Context(), service.ManageLabelsInput{
		File: "id-budget-fixture", Label: "id-label-review", Action: "set_field",
		Field: "id-field-owner", Values: []string{"whoever"},
	}); err != nil {
		t.Fatalf("set_field on a text field: %v", err)
	}
	applied := fake.FileLabels["id-budget-fixture"]
	if len(applied) != 1 {
		t.Fatalf("the file carries %d labels, want 1", len(applied))
	}
	field := applied[0].Fields["id-field-owner"]
	if field.ValueType != "text" || len(field.Text) != 1 || field.Text[0] != "whoever" {
		t.Errorf("a text field was stored as %+v; the wrong setter was used", field)
	}
}

func TestManageLabelsChecksTheShapeOfADateAndOfANumber(t *testing.T) {
	svc, fake := labelled(t)
	fake.AddLabelDefinition("id-label-retention", "Retention",
		&gdrive.LabelFieldDefinition{
			ID: "id-field-until", Properties: &gdrive.LabelFieldProperties{DisplayName: "Keep until"},
			Lifecycle:           &gdrive.LabelLifecycle{State: "PUBLISHED"},
			AppliedCapabilities: &gdrive.LabelFieldAppliedCapabilities{CanWrite: true},
			DateOptions:         &gdrive.LabelDateOptions{},
		},
		&gdrive.LabelFieldDefinition{
			ID: "id-field-years", Properties: &gdrive.LabelFieldProperties{DisplayName: "Years"},
			Lifecycle:           &gdrive.LabelLifecycle{State: "PUBLISHED"},
			AppliedCapabilities: &gdrive.LabelFieldAppliedCapabilities{CanWrite: true},
			IntegerOptions:      &struct{}{},
		})

	cases := []struct {
		name  string
		field string
		value string
		ok    bool
	}{
		{"a calendar date", "id-field-until", "2027-01-31", true},
		{"a timestamp, which the API does not take", "id-field-until", "2027-01-31T09:00:00Z", false},
		{"a whole number", "id-field-years", "7", true},
		{"words", "id-field-years", "seven", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := svc.ManageLabels(t.Context(), service.ManageLabelsInput{
				File: "id-budget-fixture", Label: "id-label-retention", Action: "set_field",
				Field: c.field, Values: []string{c.value},
			})
			if c.ok && err != nil {
				t.Errorf("%q was refused: %v", c.value, err)
			}
			if !c.ok && err == nil {
				t.Errorf("%q was accepted and sent to Drive", c.value)
			}
		})
	}
}

// A date field decodes from `dateString`. The tag said `date` until phase
// 4, which meant every date-valued field read back empty — and nothing
// noticed, because an absent member and an unset one look the same.
func TestADateValuedFieldSurvivesTheRoundTrip(t *testing.T) {
	svc, fake := labelled(t)
	fake.AddLabelDefinition("id-label-retention", "Retention",
		&gdrive.LabelFieldDefinition{
			ID: "id-field-until", Properties: &gdrive.LabelFieldProperties{DisplayName: "Keep until"},
			Lifecycle:           &gdrive.LabelLifecycle{State: "PUBLISHED"},
			AppliedCapabilities: &gdrive.LabelFieldAppliedCapabilities{CanWrite: true},
			DateOptions:         &gdrive.LabelDateOptions{},
		})
	res, err := svc.ManageLabels(t.Context(), service.ManageLabelsInput{
		File: "id-budget-fixture", Label: "id-label-retention", Action: "set_field",
		Field: "id-field-until", Values: []string{"2027-01-31"},
	})
	if err != nil {
		t.Fatalf("set_field: %v", err)
	}
	if !strings.Contains(res.Text, "2027-01-31") {
		t.Errorf("the date did not survive the round trip, so the json tag is wrong again:\n%s", res.Text)
	}
}

func TestLabelToolsRefuseWhenTheDeployerHasNotTurnedLabelsOn(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	if _, err := svc.ListLabels(t.Context(), service.ListLabelsInput{}); err == nil {
		t.Error("list_labels answered with labels off")
	} else if !strings.Contains(err.Error(), "GDRIVE_LABELS") {
		t.Errorf("the refusal does not name the setting that turns them on: %v", err)
	}
	if _, err := svc.ManageLabels(t.Context(), service.ManageLabelsInput{
		File: "id-budget-fixture", Label: "id-label-review", Action: "apply",
	}); err == nil {
		t.Error("manage_labels answered with labels off")
	}
}

// The failure every deployer meets first: the Drive Labels API is a
// separate API with a separate scope, and its refusal says only
// "insufficient authentication scopes".
func TestALabelsRefusalNamesTheSetupStepBehindIt(t *testing.T) {
	svc, fake := labelled(t)
	fake.LabelsEnabled = false

	_, err := svc.ListLabels(t.Context(), service.ListLabelsInput{})
	if err == nil {
		t.Fatal("the Labels API refused the token and the call succeeded anyway")
	}
	for _, want := range []string{"Drive Labels API", "login"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q, so a deployer cannot act on it: %v", want, err)
		}
	}
}

// Applying a label goes through Drive on the ordinary scope; only the
// DEFINITIONS need the Labels API. So an account that can reach Drive but
// not the Labels API must still be able to apply and remove a label it
// already knows the id of. Only set_field is refused there, and for a
// reason it can state: the setter depends on the field's type, and the
// type is in the definition.
func TestApplyAndRemoveWorkWithoutTheDefinitions(t *testing.T) {
	svc, fake := labelled(t)
	fake.LabelsEnabled = false
	const file = "id-budget-fixture"

	if _, err := svc.ManageLabels(t.Context(), service.ManageLabelsInput{
		File: file, Label: "id-label-review", Action: "apply",
	}); err != nil {
		t.Fatalf("apply needs no labels scope, but was refused: %v", err)
	}
	if got := len(fake.FileLabels[file]); got != 1 {
		t.Fatalf("after apply the file carries %d labels, want 1", got)
	}

	_, err := svc.ManageLabels(t.Context(), service.ManageLabelsInput{
		File: file, Label: "id-label-review", Action: "set_field",
		Field: "id-field-owner", Values: []string{"whoever"},
	})
	if err == nil {
		t.Fatal("a field was set without knowing its type, so a setter was guessed")
	}
	if !strings.Contains(err.Error(), "will not guess") {
		t.Errorf("the refusal does not say why it cannot proceed: %v", err)
	}

	if _, err := svc.ManageLabels(t.Context(), service.ManageLabelsInput{
		File: file, Label: "id-label-review", Action: "remove",
	}); err != nil {
		t.Fatalf("remove needs no labels scope, but was refused: %v", err)
	}
	if got := len(fake.FileLabels[file]); got != 0 {
		t.Fatalf("after remove the file carries %d labels, want 0", got)
	}
}

func TestManageLabelsRefusesAFileDriveSaysYouMayNotLabel(t *testing.T) {
	svc, fake := labelled(t)
	fake.Files["id-budget-fixture"].Capabilities.CanModifyLabels = false

	_, err := svc.ManageLabels(t.Context(), service.ManageLabelsInput{
		File: "id-budget-fixture", Label: "id-label-review", Action: "apply",
	})
	if err == nil {
		t.Fatal("a file whose canModifyLabels is false was labelled anyway")
	}
	if !strings.Contains(err.Error(), "canModifyLabels") {
		t.Errorf("the refusal does not name the capability it read: %v", err)
	}
}

// The file card is where a label is normally seen, and it has to name
// the label rather than print the id an administrator generated.
func TestTheFileCardNamesTheLabelsOnAFile(t *testing.T) {
	svc, fake := labelled(t)
	fake.ApplyLabel("id-budget-fixture", &gdrive.Label{
		ID: "id-label-review", RevisionID: "1",
		Fields: map[string]gdrive.LabelField{
			"id-field-stage": {ID: "id-field-stage", ValueType: "selection", Selection: []string{"id-choice-draft"}},
		},
	})
	out, err := svc.GetFile(t.Context(), service.GetFileInput{File: "id-budget-fixture", IncludeLabels: true})
	if err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	for _, want := range []string{"Review state", "Stage", "Draft"} {
		if !strings.Contains(out, want) {
			t.Errorf("the card does not carry %q:\n%s", want, out)
		}
	}
}

// AllFileLabels and allLabelDefinitions both page to the end, and until
// phase 4's cleanup the fake served every result in one answer with no
// next token — so neither loop had a test that could fail. These are
// them.
func TestAFilesLabelsAreReadPastTheFirstPage(t *testing.T) {
	svc, fake := labelled(t)
	// More labels on one file than a page holds, which is what makes the
	// loop run at all.
	for i := range gapi.MaxFileLabelPageSize + 3 {
		fake.ApplyLabel("id-budget-fixture", &gdrive.Label{
			ID: fmt.Sprintf("id-label-bulk-%03d", i), RevisionID: "1",
		})
	}
	labels, err := drivetest.Client(t, fake).AllFileLabels(t.Context(), "id-budget-fixture")
	if err != nil {
		t.Fatalf("AllFileLabels: %v", err)
	}
	if len(labels) != gapi.MaxFileLabelPageSize+3 {
		t.Errorf("read %d labels, want %d: the paging loop stopped early",
			len(labels), gapi.MaxFileLabelPageSize+3)
	}
	_ = svc
}

func TestLabelDefinitionsAreReadPastTheFirstPage(t *testing.T) {
	svc, fake := labelled(t)
	for i := range gapi.DefaultLabelPageSize + 2 {
		fake.AddLabelDefinition(fmt.Sprintf("id-label-many-%03d", i), fmt.Sprintf("Label %d", i))
	}
	out, err := svc.ListLabels(t.Context(), service.ListLabelsInput{})
	if err != nil {
		t.Fatalf("ListLabels: %v", err)
	}
	// list_labels shows one page; the cache behind manage_labels reads
	// them all, and that is the loop under test.
	if !strings.Contains(out, "labels available") {
		t.Fatalf("the listing did not render:\n%s", out)
	}
	if _, err := svc.ManageLabels(t.Context(), service.ManageLabelsInput{
		File: "id-budget-fixture", Label: "id-label-many-051", Action: "set_field",
		Field: "id-field-owner", Values: []string{"x"},
	}); err == nil || !strings.Contains(err.Error(), "id-field-owner") {
		// The label exists only past the first page. Reaching it at all
		// proves the loop ran; the error is about the field, which that
		// definition does not have.
		t.Errorf("a definition past the first page was not found: %v", err)
	}
}
