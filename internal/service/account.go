package service

import (
	"context"

	"github.com/mmedum/google-drive-mcp/v3/internal/config"
	"github.com/mmedum/google-drive-mcp/v3/internal/model"
	"github.com/mmedum/google-drive-mcp/v3/internal/render"
)

// GetAccount renders who is signed in, what their Drive holds, and what
// this server will let a model do on their behalf. The settings belong
// in the same answer as the account: a tool that is not registered is
// invisible, and this is where a person finds out why.
func (s *Service) GetAccount(ctx context.Context) (string, error) {
	about, err := s.api.About(ctx)
	if err != nil {
		return "", wrap(err, "reading the account")
	}
	acct := model.NewAccount(about)
	if acct == nil {
		return render.Account(nil, render.AccountOptions{}), nil
	}
	o := render.AccountOptions{
		ReadOnly:    s.opts.ReadOnly,
		Destructive: s.opts.Destructive,
		Sharing:     string(s.opts.Sharing),
		LocalDir:    s.opts.LocalDir,
		MaxDownload: s.opts.MaxDownload,
		Labels:      s.opts.Labels,
		Tools:       s.RegisteredTools(),
	}
	o.Personal = organizationOf(acct.Address).personal
	if o.Sharing == "" {
		o.Sharing = string(config.SharingAll)
	}
	// A consumer account has no shared drives at all, and asking costs a
	// call that will only ever answer "none".
	if acct.CanCreateDrives {
		if drives, err := s.drivesList(ctx); err == nil {
			o.DrivesKnown = true
			for _, d := range drives {
				name := d.Name
				if d.Hidden {
					name += " (hidden)"
				}
				o.Drives = append(o.Drives, name)
			}
		}
	}
	return render.Account(acct, o), nil
}
