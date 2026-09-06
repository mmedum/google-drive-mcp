package service

import (
	"fmt"

	"github.com/mmedum/google-drive-mcp/internal/gapi"
)

// Two of this server's features are not Drive. Labels are defined by the
// Drive Labels API and activity is reported by the Drive Activity API,
// each on its own host, with its own scope, its own enablement in the
// Cloud project, and its own GDRIVE_ flag to turn the tools on.
//
// That is a shape, not a coincidence, and it was written out twice
// before it was named — which showed immediately in the way the two
// copies had already drifted: one answered a deployer who had not turned
// the feature on with [unsupported] and the other with the same thing,
// while the two OLDER guards for the identical situation (read-only
// mode, sharing off) both answer [forbidden]. A model should not have to
// learn two words for "this server was started without it".
//
// So the three facts that differ between the two — the API's name, the
// flag, and what to add to the consent screen — are a table, and the
// class is decided once.
type auxAPI struct {
	// name is the API as the Cloud console lists it, because enabling it
	// is the step a deployer has to take.
	name string
	// env is the GDRIVE_ setting that registers the tools and asks for
	// the scope.
	env string
	// scopes is how to describe what the consent screen needs.
	scopes string
}

var (
	labelsAPI = auxAPI{
		name:   "Drive Labels API",
		env:    "GDRIVE_LABELS",
		scopes: "the label scopes",
	}
	activityAPI = auxAPI{
		name:   "Drive Activity API",
		env:    "GDRIVE_ACTIVITY",
		scopes: "the activity scope",
	}
)

// enabled refuses when the deployer has not turned the feature on. The
// class matches writable and sharable, which answer the same question
// about read-only mode and about sharing.
func (a auxAPI) enabled(on bool) error {
	if on {
		return nil
	}
	return Errorf(ClassForbidden,
		"this server was started without %s=true, so this is not available. That setting is also what "+
			"asks for the scope at login, so turning it on means running `google-drive-mcp login` again.",
		a.env)
}

// refused names both halves of the setup behind the failure every
// deployer meets first. The scope and the enablement fail identically —
// a 403 saying only that the token had insufficient scopes — so a
// message naming one of them would send half of the people who see it
// looking in the wrong place.
func (a auxAPI) refused(err error, doing string) error {
	switch gapi.Class(err) {
	case ClassAuth, ClassForbidden:
		return &Error{Class: ClassForbidden, Message: fmt.Sprintf(
			"%s failed because the %s refused the token. It is a separate API from Drive: enable the %s "+
				"in the Cloud project, add %s to the consent screen, and run `google-drive-mcp login` "+
				"again with %s=true. Google said: %s",
			doing, a.name, a.name, a.scopes, a.env, gapi.Message(err)), Err: err}
	}
	return wrap(err, doing)
}
