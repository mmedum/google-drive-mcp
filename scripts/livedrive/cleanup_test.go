package main

import (
	"bytes"
	"fmt"
	"os"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mmedum/google-drive-mcp/v3/scripts/internal/redact"
	"github.com/mmedum/google-drive-mcp/v3/scripts/internal/transcript"
)

// These hold what a live run leaves behind, or reaches, outside the
// scratch folder it trashes: a new owner, a grant, a file in a shared
// drive, and anything a stop signal would otherwise strand. They run the
// driver's own steps against fakeDrive below, which keeps just enough of
// Drive to answer them: grants, where a moved file is, and the scratch
// folder.

// sentCall is one tool call the driver made.
type sentCall struct {
	tool string
	args map[string]any
}

func (c sentCall) flag(name string) bool {
	v, _ := c.args[name].(bool)
	return v
}

func (c sentCall) text(name string) string {
	v, _ := c.args[name].(string)
	return v
}

// fakeDrive answers the driver in memory and records every call.
type fakeDrive struct {
	calls                  []sentCall
	ids                    int
	scratchID, scratchName string
	// grants are the principals each file is shared with, lowercased.
	grants map[string]map[string]bool
	// where is the location of a file moved out of the scratch folder.
	where map[string]string
	// keepGrants answers an unshare and leaves the grant in place.
	keepGrants bool
	// movedBackTo is where a move back to the scratch folder puts a
	// file, while answering that it moved; empty is the scratch folder.
	movedBackTo string
	// before sees each call before it is answered.
	before func(sentCall)
}

func newFakeDrive() *fakeDrive {
	return &fakeDrive{grants: map[string]map[string]bool{}, where: map[string]string{}}
}

func (f *fakeDrive) CallTool(tool string, args map[string]any) (string, bool, error) {
	c := sentCall{tool: tool, args: args}
	f.calls = append(f.calls, c)
	if f.before != nil {
		f.before(c)
	}
	file, principal, dry := c.text("file"), strings.ToLower(c.text("principal")), c.flag("dry_run")
	switch tool {
	case "get_account":
		return "Live Driver <driver@example.com>\n", false, nil
	case "create_folder":
		id := f.newID()
		if name := c.text("name"); f.scratchID == "" && strings.HasPrefix(name, scratchPrefix) {
			f.scratchID, f.scratchName = id, name
			f.where[id] = "My Drive"
		}
		return "created: " + c.text("name") + " — folder\nid: " + id + "\n", false, nil
	case "share_file", "share_outside_organization":
		if !dry && principal != "" {
			if f.grants[file] == nil {
				f.grants[file] = map[string]bool{}
			}
			f.grants[file][principal] = true
		}
		return "shared: a file — text file\nid: " + file + "\n", false, nil
	case "unshare_file":
		if !dry && !f.keepGrants {
			delete(f.grants[file], principal)
		}
		return "unshared: a file — text file\nid: " + file + "\n", false, nil
	case "list_permissions":
		listing := "a file — text file\n"
		for p := range f.grants[file] {
			listing += "can edit  " + p + "  id-permission-fixture\n"
		}
		return listing, false, nil
	case "move_file":
		to := c.text("to")
		switch {
		case dry:
		case strings.HasPrefix(to, "drive:"):
			f.where[file] = strings.TrimPrefix(to, "drive:") + " (shared drive)"
		case to == f.scratchID && f.movedBackTo != "":
			f.where[file] = f.movedBackTo
		case to == f.scratchID:
			delete(f.where, file)
		}
		return "moved: a file — text file\nid: " + file + "\n", false, nil
	case "get_file":
		return "a file — text file\nid: " + file + "\nlocation: " + f.location(file) +
			"\nowner: Live Driver <driver@example.com>\n", false, nil
	case "delete_drive":
		if c.flag("confirm") && !dry {
			return "deleted: a drive, gone for good\n", false, nil
		}
	}
	return "updated: a file — text file\nid: " + f.newID() + "\n", false, nil
}

func (f *fakeDrive) ReadResource(string) (string, string, error) { return "", "", nil }
func (f *fakeDrive) StderrTail(int) []string                     { return nil }

func (f *fakeDrive) newID() string {
	f.ids++
	return fmt.Sprintf("id-fake-%03d", f.ids)
}

// location is where get_file places a file: the scratch folder unless
// it was moved out.
func (f *fakeDrive) location(id string) string {
	if loc, ok := f.where[id]; ok {
		return loc
	}
	return "My Drive/" + f.scratchName
}

// reached is every file principal still has a grant on. The stand-in
// grants what the server would refuse, too, so ask it of one address.
func (f *fakeDrive) reached(principal string) []string {
	var out []string
	for file, principals := range f.grants {
		if principals[principal] {
			out = append(out, file)
		}
	}
	return out
}

// testOptions are the options of a run whose pauses return at once.
func testOptions(o options) options {
	o.write = true
	o.pause = func(time.Duration) {}
	return o
}

// testRun is a write run against f, with its transcript.
func testRun(t *testing.T, f *fakeDrive, o options) (*writeRun, *bytes.Buffer) {
	t.Helper()
	var out bytes.Buffer
	tr := transcript.NewTo(redact.NewRedactor(true), &out, &out)
	w := newWriteRun(f, tr, t.TempDir(), testOptions(o), time.Date(2026, 3, 6, 12, 0, 0, 0, time.UTC))
	return w, &out
}

// runAll is a whole write run against f, returning its transcript.
func runAll(t *testing.T, f *fakeDrive, o options) string {
	t.Helper()
	var out bytes.Buffer
	tr := transcript.NewTo(redact.NewRedactor(true), &out, &out)
	if _, err := runWrites(f, tr, t.TempDir(), testOptions(o)); err != nil {
		t.Fatalf("runWrites: %v", err)
	}
	return out.String()
}

// An ownership transfer cannot be undone, and Google emails the new
// owner. -share names a second person and -destructive registers the
// tool, and neither of them, alone or together, is a request to hand a
// file over: only -transfer is, and only to its own address.
func TestOwnershipGoesOnlyToTheTransferAddress(t *testing.T) {
	const colleague, spare = "colleague@example.com", "spare-account@example.net"
	for _, tc := range []struct {
		name     string
		transfer string
		want     []string
	}{
		{"-share and -destructive", "", nil},
		{"-share, -destructive and -transfer", spare, []string{spare}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeDrive()
			runAll(t, f, options{share: colleague, destructive: true, transfer: tc.transfer})
			var real []string
			probed := false
			for _, c := range f.calls {
				if c.tool != "transfer_ownership" {
					continue
				}
				probed = probed || c.flag("dry_run")
				if c.text("to") == colleague {
					t.Errorf("transfer_ownership named the -share address: %v", c.args)
				}
				if c.flag("confirm") && !c.flag("dry_run") {
					real = append(real, c.text("to"))
				}
			}
			if !probed {
				t.Fatal("transfer_ownership was never run as a dry run, so the step did not run at all")
			}
			if !slices.Equal(real, tc.want) {
				t.Errorf("real transfers went to %q, want %q", real, tc.want)
			}
		})
	}
}

func TestTransferNeedsWriteAndDestructive(t *testing.T) {
	for _, tc := range []struct {
		o    options
		want bool
	}{
		{options{transfer: "a@example.com", write: true, destructive: true}, true},
		{options{transfer: "a@example.com", write: true}, false},
		{options{transfer: "a@example.com", destructive: true}, false},
		{options{write: true}, true},
	} {
		if err := tc.o.check(); (err == nil) != tc.want {
			t.Errorf("check(%+v) = %v, want ok %v", tc.o, err, tc.want)
		}
	}
}

// unshare_file answering is not proof the grant went, and a grant that
// outlives the run is the one thing trashing the scratch folder does not
// take away. The permissions are read back, and a grant still listed
// fails the run loudly.
func TestAGrantStillListedAfterUnshareFailsLoudly(t *testing.T) {
	const share, file = "colleague@example.com", "id-text-fixture"
	for _, keep := range []bool{false, true} {
		t.Run(fmt.Sprintf("grant kept: %v", keep), func(t *testing.T) {
			f := newFakeDrive()
			f.keepGrants = keep
			w, out := testRun(t, f, options{share: share})
			w.access(made{text: file})
			if !slices.ContainsFunc(f.calls, func(c sentCall) bool {
				return c.tool == "share_file" && c.text("principal") == share && !c.flag("dry_run")
			}) {
				t.Fatal("access granted nothing, so there was nothing to take back")
			}
			loud := strings.Contains(out.String(), "A GRANT IS STILL THERE")
			if loud != keep {
				t.Errorf("the run says a grant is still there: %v; it is: %v (%v)\n%s",
					loud, keep, f.reached(share), out.String())
			}
		})
	}
}

// A move back out of a shared drive that answered "moved:" is not proof
// the file left. get_file is: a file still in the drive, where its
// members can see it, or anywhere but the scratch folder, which is what
// the run trashes, fails the run loudly.
func TestAFileNotBackInTheScratchFolderFailsLoudly(t *testing.T) {
	const small, scratch = "id-small-fixture", scratchPrefix + " 2026-03-06 12:00:00"
	for _, tc := range []struct {
		name, landsIn string
		loud          bool
	}{
		{"back in the scratch folder", "", false},
		{"still at the drive's root", "Team drive (shared drive)", true},
		{"in a folder of the drive named like the scratch folder", "Team drive (shared drive)/" + scratch, true},
		{"out of the drive and not in the scratch folder", "My Drive", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeDrive()
			f.movedBackTo = tc.landsIn
			w, out := testRun(t, f, options{drive: "Team drive"})
			w.scratchName = scratch
			w.scratchID = w.createAndKeepID("create_folder", map[string]any{"name": w.scratchName})
			w.sharedDrive(made{small: small, folder: "id-folder-fixture"})
			if !slices.ContainsFunc(f.calls, func(c sentCall) bool {
				return c.tool == "move_file" && c.text("to") == "drive:Team drive" && c.text("file") == small &&
					!c.flag("dry_run")
			}) {
				t.Fatal("the file was never moved into the shared drive")
			}
			loud := strings.Contains(out.String(), "A FILE IS NOT BACK IN THE SCRATCH FOLDER")
			if loud != tc.loud {
				t.Errorf("the run says the file is not back: %v; it is at %q\n%s",
					loud, f.location(small), out.String())
			}
		})
	}
}

// A stop signal ends a run at the next call, and the cleanup it would
// have done anyway still runs: the grant taken back and read back, the
// file brought out of the shared drive and read back, the scratch shared
// drive deleted, and the scratch folder trashed. Nothing else is called.
func TestAStopSignalStillCleansUp(t *testing.T) {
	for _, tc := range []struct {
		name string
		o    options
		// at is the call during which the signal arrives.
		at func(sentCall) bool
		// after is every call made after it, in order.
		after []string
	}{
		{
			name: "while -share's grant stands",
			o:    options{share: "colleague@example.com"},
			at: func(c sentCall) bool {
				return c.tool == "share_file" && c.text("principal") == "colleague@example.com" && !c.flag("dry_run")
			},
			after: []string{"unshare_file", "list_permissions", "trash_file"},
		},
		{
			name: "while a file is in -drive's shared drive",
			o:    options{drive: "Team drive"},
			at: func(c sentCall) bool {
				return c.tool == "move_file" && c.text("to") == "drive:Team drive" && !c.flag("dry_run")
			},
			after: []string{"move_file", "get_file", "get_file", "trash_file"},
		},
		{
			name: "while the scratch shared drive exists",
			o:    options{destructive: true},
			at: func(c sentCall) bool {
				return c.tool == "manage_drive" && c.text("action") == "create"
			},
			after: []string{"empty_trash", "delete_drive", "trash_file"},
		},
		{
			// Inside the drive's own cleanup, which empties the drive's
			// trash for the second time and then deletes the drive.
			name:  "while the scratch shared drive is being deleted",
			o:     options{destructive: true},
			at:    nth(2, func(c sentCall) bool { return c.tool == "empty_trash" && c.flag("confirm") && !c.flag("dry_run") }),
			after: []string{"delete_drive", "trash_file"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeDrive()
			tc.o.stop = &stopper{}
			at := -1
			f.before = func(c sentCall) {
				if at < 0 && tc.at(c) {
					at = len(f.calls) - 1
					tc.o.stop.set(syscall.SIGHUP)
				}
			}
			out := runAll(t, f, tc.o)
			if at < 0 {
				t.Fatal("the run never reached the call the signal was to arrive during")
			}
			var after []string
			for _, c := range f.calls[at+1:] {
				after = append(after, c.tool)
			}
			if !slices.Equal(after, tc.after) {
				t.Errorf("after the signal the run called %q, want %q", after, tc.after)
			}
			if last := f.calls[len(f.calls)-1]; last.text("file") != f.scratchID {
				t.Errorf("the last call was %s on %q, not the trash of the scratch folder", last.tool, last.text("file"))
			}
			if left := f.reached("colleague@example.com"); len(left) > 0 {
				t.Errorf("-share's address can still reach %q", left)
			}
			if len(f.where) > 1 {
				t.Errorf("files left out of the scratch folder: %v", f.where)
			}
			if !strings.Contains(out, "STOPPED by hangup") {
				t.Errorf("the transcript does not say the run stopped:\n%s", out)
			}
		})
	}
}

// nth is true of the nth call match is true of, and of no other.
func nth(n int, match func(sentCall) bool) func(sentCall) bool {
	seen := 0
	return func(c sentCall) bool {
		if !match(c) {
			return false
		}
		seen++
		return seen == n
	}
}

// Before anything is written there is nothing to clean up, and a stop
// ends the run before its next call.
func TestAStopEndsTheReadsBeforeTheNextCall(t *testing.T) {
	f := newFakeDrive()
	stop := &stopper{}
	stop.set(os.Interrupt)
	var out bytes.Buffer
	tr := transcript.NewTo(redact.NewRedactor(true), &out, &out)
	_, err := runReads(f, tr, []call{{tool: "get_account", args: map[string]any{}}}, stop)
	if err == nil || len(f.calls) != 0 {
		t.Errorf("a stopped read run returned %v after %d calls, want an error and none", err, len(f.calls))
	}
}
