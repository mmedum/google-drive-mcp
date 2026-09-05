package gapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/mmedum/google-drive-mcp/internal/gdrive"
)

// ShareOptions are the parameters of permissions.create. They are a
// separate type from UpdateShareOptions because the two calls do not
// take the same set: the reference gives create sendNotificationEmail,
// emailMessage and moveToNewOwnersRoot, and update removeExpiration, and
// one struct covering both would offer every caller four parameters that
// silently do nothing on half the calls.
type ShareOptions struct {
	// SendNotificationEmail asks Google to mail the new grantee. Drive
	// defaults it to true for users and groups and REFUSES it for a
	// domain or anyone grant, so nil means "do not send the parameter at
	// all" rather than "false": for those two principal types sending it
	// either way is a 400.
	SendNotificationEmail *bool
	// EmailMessage rides along with that mail, and only matters when one
	// is being sent.
	EmailMessage string
	// TransferOwnership is required for the owner role; Drive refuses the
	// role without it, as an acknowledgement of what it does.
	TransferOwnership bool
	// MoveToNewOwnersRoot puts a transferred file in the new owner's root
	// folder. It takes effect only outside a shared drive and only on a
	// transfer.
	MoveToNewOwnersRoot bool
	// ResourceIDs carry resource keys for the ids this call names.
	ResourceIDs []string
}

// UpdateShareOptions are the parameters of permissions.update.
type UpdateShareOptions struct {
	// TransferOwnership is required when the role becomes owner.
	TransferOwnership bool
	// RemoveExpiration clears the expiry. It is a query parameter rather
	// than an empty field in the body: the reference gives update its own
	// removeExpiration, and Drive does not read "" as "clear this".
	RemoveExpiration bool
	ResourceIDs      []string
}

func boolText(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func (o ShareOptions) values() url.Values {
	v := url.Values{}
	v.Set("fields", PermissionFields)
	v.Set("supportsAllDrives", "true")
	if o.SendNotificationEmail != nil {
		// Always explicit where it is allowed at all: Drive's default is
		// true, and a default that puts mail in someone's inbox is not
		// one to inherit by omission.
		v.Set("sendNotificationEmail", boolText(*o.SendNotificationEmail))
		if *o.SendNotificationEmail && o.EmailMessage != "" {
			v.Set("emailMessage", o.EmailMessage)
		}
	}
	if o.TransferOwnership {
		v.Set("transferOwnership", "true")
	}
	if o.MoveToNewOwnersRoot {
		v.Set("moveToNewOwnersRoot", "true")
	}
	return v
}

func (o UpdateShareOptions) values() url.Values {
	v := url.Values{}
	v.Set("fields", PermissionFields)
	v.Set("supportsAllDrives", "true")
	if o.TransferOwnership {
		v.Set("transferOwnership", "true")
	}
	if o.RemoveExpiration {
		v.Set("removeExpiration", "true")
	}
	return v
}

// CreatePermission grants access to a file or a shared drive. Drive
// allows one permission per principal, so granting to a principal that
// already has one is refused and the caller updates instead; the service
// looks the principal up before choosing between the two.
//
// It is a POST with no pre-generated id, so it is not repeatable and is
// not marked as such: a second attempt after an ambiguous failure could
// add a grant the first one already made. Sharing is exactly where that
// must not be guessed at, so the failure is reported rather than
// retried.
func (c *Client) CreatePermission(ctx context.Context, fileID string, meta *gdrive.PermissionMeta, o ShareOptions) (*gdrive.Permission, error) {
	payload, err := json.Marshal(meta)
	if err != nil {
		return nil, err
	}
	segment, err := fileSegment(fileID)
	if err != nil {
		return nil, err
	}
	u := c.base + "/files/" + segment + "/permissions?" + o.values().Encode()
	body, err := c.do(ctx, request{kind: kindSharing, method: http.MethodPost, url: u,
		body: payload, resourceIDs: append([]string{fileID}, o.ResourceIDs...)})
	if err != nil {
		return nil, err
	}
	return decodePermission(body)
}

// UpdatePermission changes an existing grant: its role, its expiry, or
// whether it is discoverable. A PATCH, so a retry is safe.
func (c *Client) UpdatePermission(ctx context.Context, fileID, permissionID string, meta *gdrive.PermissionMeta, o UpdateShareOptions) (*gdrive.Permission, error) {
	payload, err := json.Marshal(meta)
	if err != nil {
		return nil, err
	}
	segment, err := fileSegment(fileID)
	if err != nil {
		return nil, err
	}
	u := c.base + "/files/" + segment + "/permissions/" + url.PathEscape(permissionID) +
		"?" + o.values().Encode()
	body, err := c.do(ctx, request{kind: kindSharing, method: http.MethodPatch, url: u,
		body: payload, resourceIDs: append([]string{fileID}, o.ResourceIDs...)})
	if err != nil {
		return nil, err
	}
	return decodePermission(body)
}

// DeletePermission revokes one grant. Drive answers 204 with no body.
func (c *Client) DeletePermission(ctx context.Context, fileID, permissionID string) error {
	segment, err := fileSegment(fileID)
	if err != nil {
		return err
	}
	v := url.Values{}
	v.Set("supportsAllDrives", "true")
	u := c.base + "/files/" + segment + "/permissions/" + url.PathEscape(permissionID) +
		"?" + v.Encode()
	_, err = c.do(ctx, request{kind: kindSharing, method: http.MethodDelete, url: u,
		resourceIDs: []string{fileID}})
	return err
}

func decodePermission(body []byte) (*gdrive.Permission, error) {
	var p gdrive.Permission
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, fmt.Errorf("%w: decode permission: %w", ErrUnexpected, err)
	}
	return &p, nil
}
