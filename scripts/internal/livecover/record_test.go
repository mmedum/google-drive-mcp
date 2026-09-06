package livecover

import (
	"strings"
	"testing"
)

// TestARunSaysWhatItActuallySent, including the case that made the first
// version of this wrong: get_account takes no options at all, so a run
// that called it recorded an empty set of options and then reported the
// tool as never called.
func TestARunSaysWhatItActuallySent(t *testing.T) {
	rec := NewRecorder()
	rec.Sent("get_account", map[string]any{})
	rec.Sent("get_file", map[string]any{"file": "id"})

	published := map[string][]string{
		"get_account": {},
		"get_file":    {"file", "include_labels"},
		"trash_file":  {"file", "dry_run"},
	}
	report := rec.Report(published, nil)
	if !strings.Contains(report, "sent 1 of 4 options") {
		t.Errorf("the count is wrong: %s", report)
	}
	if !strings.Contains(report, "in 2 calls") {
		t.Errorf("the calls are not counted: %s", report)
	}
	if strings.Contains(report, "get_account") {
		t.Errorf("a tool with no options was reported as never called: %s", report)
	}
	if !strings.Contains(report, "trash_file") {
		t.Errorf("a tool that really was never called is not named: %s", report)
	}
}

// TestAStepThatExistsAndDoesNotRunIsNamed is the whole reason there is a
// recorder as well as a gate over the source. A sibling repository
// deleted one call and watched its static gate report full coverage;
// this is the check that would have caught it.
func TestAStepThatExistsAndDoesNotRunIsNamed(t *testing.T) {
	rec := NewRecorder()
	rec.Sent("search_files", map[string]any{"name": "a name"})

	published := map[string][]string{"search_files": {"name", "starred", "trashed"}}
	believed := map[string]map[string]bool{
		"search_files": {"name": true, "starred": true},
	}
	report := rec.Report(published, believed)
	if !strings.Contains(report, "search_files.starred") {
		t.Errorf("an option the source claims and the run did not send is not named: %s", report)
	}
	if strings.Contains(report, "search_files.trashed") {
		t.Errorf("trashed is undriven in the source too, so it is the gate's business, not this: %s", report)
	}
}

// TestAToolNeverCalledIsReportedOnce, by name, rather than once for each
// option it did not send: a mode that was not asked for would otherwise
// bury the one line worth reading.
func TestAToolNeverCalledIsReportedOnce(t *testing.T) {
	rec := NewRecorder()
	rec.Sent("get_file", map[string]any{"file": "id"})

	published := map[string][]string{
		"get_file":    {"file"},
		"empty_trash": {"confirm", "dry_run", "drive"},
	}
	believed := map[string]map[string]bool{
		"empty_trash": {"confirm": true, "dry_run": true, "drive": true},
	}
	report := rec.Report(published, believed)
	if strings.Count(report, "empty_trash") != 1 {
		t.Errorf("a tool that was never called is named %d times, want 1:\n%s",
			strings.Count(report, "empty_trash"), report)
	}
}
