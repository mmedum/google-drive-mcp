package model

import (
	"github.com/mmedum/google-drive-mcp/v3/internal/gdrive"
)

// Account is the signed-in person as this server reports them.
type Account struct {
	Name    string
	Address string
	// Storage is nil when Drive reported no quota.
	Storage *Storage
	// CanCreateDrives is false on a personal account, which has no shared
	// drives, and on a Workspace account whose administrator turned it off.
	CanCreateDrives bool
}

// Storage is an account's quota in bytes. Drive sends each count as a
// decimal string; one it cannot parse reads as zero.
type Storage struct {
	Used, InDrive, InTrash int64
	// Limit is meaningful only when Limited: an account with no limit has
	// no limit field, and one Drive sent unreadable is read as none.
	Limit   int64
	Limited bool
}

// NewAccount converts about.get's answer, or returns nil when it names
// nobody.
func NewAccount(a *gdrive.About) *Account {
	if a == nil || a.User == nil {
		return nil
	}
	out := &Account{Name: a.User.DisplayName, Address: a.User.EmailAddress, CanCreateDrives: a.CanCreateDrives}
	if q := a.StorageQuota; q != nil {
		count := func(s string) int64 { n, _ := gdrive.ParseCount(s); return n }
		limit, limited := gdrive.ParseCount(q.Limit)
		out.Storage = &Storage{
			Used: count(q.Usage), InDrive: count(q.UsageInDrive), InTrash: count(q.UsageInDriveTrash),
			Limit: limit, Limited: limited,
		}
	}
	return out
}
