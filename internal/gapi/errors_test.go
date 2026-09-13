package gapi

import (
	"strings"
	"testing"
)

// TestAPermissionDenialDoesNotRepeatTheAccount: Google names the account in
// the message of a permission failure, and this server repeats that
// message verbatim into an error string that reaches a log, a terminal
// and a tool response. The domain stays, because it is what tells a
// person which account was refused.
func TestAPermissionDenialDoesNotRepeatTheAccount(t *testing.T) {
	body := []byte(`{"error":{"code":403,"status":"PERMISSION_DENIED",` +
		`"message":"The user someone.private@example.com does not have permission to access this file."}}`)
	e := parseAPIError(403, "GET", "/drive/v3/files/x", body)
	if strings.Contains(e.Error(), "someone.private@example.com") {
		t.Errorf("the address was repeated verbatim: %s", e.Error())
	}
	if !strings.Contains(e.Error(), "…@example.com") {
		t.Errorf("the domain should survive so the account is still identifiable: %s", e.Error())
	}
	// A message with no address in it is untouched.
	plain := parseAPIError(404, "GET", "/x", []byte(`{"error":{"message":"File not found."}}`))
	if plain.Message != "File not found." {
		t.Errorf("message = %q, want it unchanged", plain.Message)
	}

	// The body that is not an error envelope at all is the worse case:
	// it is kept verbatim as the message, so whatever Google sent goes
	// straight into the error. The first pass at this masked only the
	// parsed field and left this one.
	raw := parseAPIError(500, "GET", "/x", []byte("upstream refused someone.private@example.com"))
	if strings.Contains(raw.Error(), "someone.private@example.com") {
		t.Errorf("an unparsed body was kept verbatim: %s", raw.Error())
	}
	if !strings.Contains(raw.Error(), "…@example.com") {
		t.Errorf("the domain should survive: %s", raw.Error())
	}
}
