// Command google-drive-mcp is a Model Context Protocol server for Google
// Drive. It speaks MCP over stdio to Claude Code, Claude Desktop and any
// other MCP client.
//
// Subcommands:
//
//	google-drive-mcp login    authorize a Google account (opens a browser)
//	google-drive-mcp logout   revoke and forget the stored token
//	google-drive-mcp status   show the active profile and where the token lives
//	google-drive-mcp doctor   run live checks against Google
//	google-drive-mcp          run the MCP server (default)
//	google-drive-mcp --version | --dump-schemas
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/oauth2"

	"github.com/mmedum/google-drive-mcp/internal/auth"
	"github.com/mmedum/google-drive-mcp/internal/config"
	"github.com/mmedum/google-drive-mcp/internal/credentials"
	"github.com/mmedum/google-drive-mcp/internal/gapi"
	"github.com/mmedum/google-drive-mcp/internal/model"
	"github.com/mmedum/google-drive-mcp/internal/server"
	"github.com/mmedum/google-drive-mcp/internal/service"
	"github.com/mmedum/google-drive-mcp/internal/userconfig"
	"github.com/mmedum/google-drive-mcp/internal/version"
)

func main() {
	if len(os.Args) >= 2 {
		switch os.Args[1] {
		case "login":
			os.Exit(cmdLogin(os.Args[2:]))
		case "logout":
			os.Exit(cmdLogout(os.Args[2:]))
		case "status":
			os.Exit(cmdStatus(os.Args[2:]))
		case "doctor":
			os.Exit(cmdDoctor(os.Args[2:]))
		case "help", "-h", "--help":
			usage(os.Stdout)
			return
		}
	}
	os.Exit(runServer(os.Args[1:]))
}

func usage(w io.Writer) {
	_, _ = fmt.Fprint(w, `google-drive-mcp — MCP server for Google Drive

Usage:
  google-drive-mcp                  run the MCP server over stdio
  google-drive-mcp login            authorize a Google account
  google-drive-mcp logout           revoke and delete the stored token
  google-drive-mcp status           show profile, token location, settings
  google-drive-mcp doctor [FILE]    live checks; FILE is an id, URL or path to read
  google-drive-mcp --version
  google-drive-mcp --dump-schemas   print tool schemas as JSON

Settings come from GDRIVE_* environment variables; every subcommand also
accepts the matching flags (run one with -h).
`)
}

func fail(format string, args ...any) int {
	fmt.Fprintf(os.Stderr, "google-drive-mcp: "+format+"\n", args...)
	return 1
}

// profile bundles the per-profile state the commands share.
type profile struct {
	cfg              config.Config
	dir              string
	clientSecretPath string
	user             userconfig.Config
	hasConfig        bool // a config file existed for the profile
	store            *credentials.Store
}

func loadConfig(fs *flag.FlagSet, args []string) (config.Config, error) {
	settings, err := parseFlags(fs, args)
	if err != nil {
		return config.Config{}, err
	}
	return settings.Build()
}

// parseFlags binds the settings and parses the command line, without
// validating. --version is answered from this point, so a misspelled
// flag is still an error while an unrelated bad setting does not stop
// the one command a person runs to report a bug.
func parseFlags(fs *flag.FlagSet, args []string) (*config.Settings, error) {
	settings := config.Define(fs, os.Getenv)
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	return settings, nil
}

func openProfile(cfg config.Config, warn func(string)) (*profile, error) {
	dir, err := userconfig.ProfileDir(cfg.Profile)
	if err != nil {
		return nil, err
	}
	p := &profile{cfg: cfg, dir: dir}
	p.user, err = userconfig.Load(cfg.Profile)
	if err != nil && !errors.Is(err, userconfig.ErrNotFound) {
		return nil, err
	}
	p.hasConfig = err == nil
	switch {
	case cfg.ClientSecretPath != "":
		p.clientSecretPath = cfg.ClientSecretPath
	case p.user.ClientSecretPath != "":
		p.clientSecretPath = p.user.ClientSecretPath
	default:
		p.clientSecretPath, err = userconfig.DefaultClientSecretPath(cfg.Profile)
		if err != nil {
			return nil, err
		}
	}
	tokenFile, err := userconfig.TokenFilePath(cfg.Profile)
	if err != nil {
		return nil, err
	}
	p.store = &credentials.Store{Profile: cfg.Profile, Keyring: credentials.OSKeyring(), FilePath: tokenFile, Warn: warn}
	return p, nil
}

// scopes are the ones this configuration asks for.
func (p *profile) scopes() []string { return auth.Scopes(p.cfg.ReadOnly, p.cfg.Labels) }

// tokenSource builds the refresh-token-backed source, or reports why not.
func (p *profile) tokenSource(ctx context.Context) (oauth2.TokenSource, credentials.Source, error) {
	oc, err := auth.LoadClientSecret(p.clientSecretPath, p.scopes())
	if err != nil {
		return nil, "", err
	}
	tok, src, err := p.store.Resolve()
	if err != nil {
		return nil, "", err
	}
	return auth.TokenSource(ctx, oc, tok), src, nil
}

// newClient builds the Drive client for this profile's settings.
func newClient(ts oauth2.TokenSource, cfg config.Config, logger *slog.Logger) *gapi.Client {
	return gapi.New(ts, gapi.Options{
		Logger: logger, Timeout: cfg.HTTPTimeout,
		UserAgent: "google-drive-mcp/" + version.String(),
	})
}

// newService builds the orchestrator from the validated configuration.
func newService(api service.API, cfg config.Config, logger *slog.Logger) *service.Service {
	return service.New(api, service.Options{
		ReadOnly: cfg.ReadOnly, Destructive: cfg.EnableDestructive, Sharing: cfg.Sharing,
		LocalDir: cfg.LocalDir, MaxDownload: cfg.MaxDownload, Labels: cfg.Labels, Logger: logger,
	})
}

func runServer(args []string) int {
	fs := flag.NewFlagSet("google-drive-mcp", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var showVersion, dumpSchemas bool
	fs.BoolVar(&showVersion, "version", false, "print version and exit")
	fs.BoolVar(&dumpSchemas, "dump-schemas", false, "print tool schemas as JSON and exit")
	settings, err := parseFlags(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return fail("%v", err)
	}
	if showVersion {
		fmt.Println(version.Info())
		return 0
	}
	cfg, err := settings.Build()
	if err != nil {
		return fail("%v", err)
	}
	logger := config.NewLogger(cfg, os.Stderr)
	slog.SetDefault(logger)

	if dumpSchemas {
		srv := server.New(server.Deps{Config: cfg, Logger: logger, Version: version.String()})
		if err := server.DumpSchemas(context.Background(), srv, os.Stdout, version.String()); err != nil {
			return fail("dump schemas: %v", err)
		}
		return 0
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	p, err := openProfile(cfg, func(msg string) { logger.Warn(msg) })
	if err != nil {
		return fail("%v", err)
	}
	ts, src, err := p.tokenSource(ctx)
	if err != nil {
		// A server that exits here shows the person "failed to connect"
		// and the model never learns why. It keeps serving instead, and
		// every tool answers with an actionable [auth] error.
		logger.Warn("no usable credentials; every tool will return an [auth] error until `google-drive-mcp login` succeeds", "err", err)
		ts = gapi.NoCredentials{Reason: err}
	} else {
		logger.Info("credentials resolved", "profile", cfg.Profile, "source", string(src))
		// Warm the token off the startup path; the token source is safe
		// for concurrent use and the first tool call reuses the result.
		go func() {
			if _, err := ts.Token(); err != nil {
				logger.Warn("credential check failed; tools will return [auth] errors until `google-drive-mcp login` succeeds", "err", err)
			} else {
				logger.Info("credential check ok")
			}
		}()
	}

	svc := newService(newClient(ts, cfg, logger), cfg, logger)
	srv := server.New(server.Deps{Service: svc, Config: cfg, Logger: logger, Version: version.String()})
	logger.Info("serving MCP over stdio", "version", version.String(),
		"read_only", cfg.ReadOnly, "sharing", string(cfg.Sharing),
		"destructive", cfg.EnableDestructive, "transfers", cfg.LocalDir != "")
	if err := srv.Run(ctx, &mcp.StdioTransport{}); err != nil && !isDisconnect(err) {
		return fail("server: %v", err)
	}
	logger.Info("client disconnected; exiting")
	return 0
}

// JSON-RPC codes the SDK uses when a session is winding down. They are
// not in the JSON-RPC specification's own range; the SDK defines them
// and exports the error type carrying them through its jsonrpc package.
const (
	codeServerClosing = -32004
	codeClientClosing = -32003
)

// isDisconnect reports whether the session ended because the client went
// away. That is how every stdio session ends, and it is not a failure: a
// non-zero exit here shows up in the client's log as a crash, on every
// clean shutdown.
//
// The SDK reports it as a wire error wrapping its own sentinel, and the
// EOF underneath is text rather than a wrapped error, so errors.Is on
// io.EOF does not see it. The code is the thing to match: it survives a
// reworded message, which a string check would not.
func isDisconnect(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var wire *jsonrpc.Error
	if errors.As(err, &wire) {
		return wire.Code == codeServerClosing || wire.Code == codeClientClosing
	}
	return false
}

// openCommand parses a subcommand's flags, loads the configuration and
// opens the profile. A non-nil exit code means the caller should return it.
func openCommand(name string, args []string, define func(*flag.FlagSet)) (*profile, *flag.FlagSet, *int) {
	fs := flag.NewFlagSet("google-drive-mcp "+name, flag.ContinueOnError)
	if define != nil {
		define(fs)
	}
	cfg, err := loadConfig(fs, args)
	if err != nil {
		code := 1
		if errors.Is(err, flag.ErrHelp) {
			code = 0
		} else {
			fail("%v", err)
		}
		return nil, fs, &code
	}
	p, err := openProfile(cfg, warnStderr)
	if err != nil {
		code := fail("%v", err)
		return nil, fs, &code
	}
	return p, fs, nil
}

func warnStderr(msg string) { fmt.Fprintln(os.Stderr, "warning: "+msg) }

func cmdLogin(args []string) int {
	var noBrowser bool
	var timeout time.Duration
	p, _, code := openCommand("login", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&noBrowser, "no-browser", false, "print the URL instead of opening a browser")
		fs.DurationVar(&timeout, "timeout", 5*time.Minute, "how long to wait for the browser")
	})
	if code != nil {
		return *code
	}
	cfg := p.cfg
	if _, err := os.Stat(p.clientSecretPath); err != nil {
		return fail("OAuth client JSON not found at %s\n"+
			"Create a Desktop-app OAuth client in your own Google Cloud project, download its JSON, and either place "+
			"it there or pass --client-secret PATH (GDRIVE_CLIENT_SECRET). The README walks through the whole setup.",
			p.clientSecretPath)
	}
	scopes := p.scopes()
	oc, err := auth.LoadClientSecret(p.clientSecretPath, scopes)
	if err != nil {
		return fail("%v", err)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	opts := auth.LoginOptions{Out: os.Stdout, Timeout: timeout}
	if noBrowser {
		opts.OpenBrowser = func(string) error { return errors.New("--no-browser") }
	}
	tok, err := auth.Login(ctx, oc, opts)
	if err != nil {
		return fail("login failed: %v", err)
	}
	src, err := p.store.Save(tok.RefreshToken)
	if err != nil {
		return fail("store token: %v", err)
	}
	email := ""
	if info, err := auth.Inspect(ctx, nil, tok.AccessToken); err == nil {
		email = info.Email
		if missing := auth.HasScopes(info.Scopes, scopes); len(missing) > 0 {
			warnStderr("Google granted fewer scopes than requested; missing: " + strings.Join(missing, ", ") +
				". Re-run login and approve every checkbox.")
		}
	}
	if email == "" {
		api := gapi.New(oauth2.StaticTokenSource(tok), gapi.Options{Timeout: 30 * time.Second})
		if about, err := api.About(ctx); err == nil && about.User != nil {
			email = about.User.EmailAddress
		}
	}
	// Login rewrites the client-secret path and the account, so
	// re-authenticating can never leave stale state behind.
	p.user.ClientSecretPath = p.clientSecretPath
	p.user.AccountEmail = email
	p.user.TokenStore = string(src)
	p.user.Scopes = scopes
	if err := userconfig.Save(cfg.Profile, p.user); err != nil {
		return fail("save profile: %v", err)
	}
	fmt.Printf("Logged in as %s (profile %q, token stored in %s).\n", orUnset(email), cfg.Profile, src)
	return 0
}

func cmdLogout(args []string) int {
	p, _, code := openCommand("logout", args, nil)
	if code != nil {
		return *code
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// Only the stored token is revoked and removed; an environment
	// override is outside this command's reach.
	if tok, _, err := p.store.ResolveStored(); err == nil {
		if err := auth.Revoke(ctx, nil, tok); err != nil {
			warnStderr(fmt.Sprintf("could not revoke the token at Google (%v); it is still deleted locally", err))
		}
	}
	if os.Getenv(credentials.EnvVar) != "" {
		warnStderr(credentials.EnvVar + " is set; logout cannot remove or revoke it")
	}
	if err := p.store.Delete(); err != nil {
		return fail("%v", err)
	}
	if p.hasConfig {
		p.user.AccountEmail, p.user.TokenStore, p.user.Scopes = "", "", nil
		if err := userconfig.Save(p.cfg.Profile, p.user); err != nil {
			return fail("save profile: %v", err)
		}
	}
	fmt.Printf("Logged out of profile %q.\n", p.cfg.Profile)
	return 0
}

func cmdStatus(args []string) int {
	p, _, code := openCommand("status", args, nil)
	if code != nil {
		return *code
	}
	printStatus(os.Stdout, p)
	return 0
}

// printStatus writes what is configured, without contacting Google. It
// prints the account to the terminal and never to the log.
func printStatus(w io.Writer, p *profile) {
	cfg := p.cfg
	_, _ = fmt.Fprintln(w, version.Info())
	_, _ = fmt.Fprintf(w, "profile:          %s (%s)\n", cfg.Profile, p.dir)
	exists := "missing"
	if _, err := os.Stat(p.clientSecretPath); err == nil {
		exists = "present"
	}
	_, _ = fmt.Fprintf(w, "client secret:    %s (%s)\n", p.clientSecretPath, exists)
	if _, src, err := p.store.Resolve(); err == nil {
		_, _ = fmt.Fprintf(w, "refresh token:    stored in %s\n", src)
	} else {
		_, _ = fmt.Fprintf(w, "refresh token:    none (%v)\n", err)
	}
	_, _ = fmt.Fprintf(w, "account:          %s\n", orUnset(p.user.AccountEmail))
	if len(p.user.Scopes) > 0 {
		_, _ = fmt.Fprintf(w, "scopes at login:  %s\n", strings.Join(p.user.Scopes, " "))
	}
	_, _ = fmt.Fprintf(w, "scopes wanted:    %s\n", strings.Join(p.scopes(), " "))
	_, _ = fmt.Fprintf(w, "read-only:        %t\n", cfg.ReadOnly)
	_, _ = fmt.Fprintf(w, "sharing tools:    %s\n", cfg.Sharing)
	_, _ = fmt.Fprintf(w, "destructive:      %t\n", cfg.EnableDestructive)
	_, _ = fmt.Fprintf(w, "labels:           %t\n", cfg.Labels)
	_, _ = fmt.Fprintf(w, "local dir:        %s\n", orUnset(cfg.LocalDir))
	_, _ = fmt.Fprintf(w, "max download:     %s\n", model.HumanSize(cfg.MaxDownload))
	_, _ = fmt.Fprintf(w, "http timeout:     %s\n", cfg.HTTPTimeout)
}

func cmdDoctor(args []string) int {
	p, fs, code := openCommand("doctor", args, nil)
	if code != nil {
		return *code
	}
	cfg := p.cfg
	printStatus(os.Stdout, p)
	fmt.Println()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	failed := 0
	check := func(name string, err error, detail string) {
		if err != nil {
			failed++
			fmt.Printf("✘ %s: %v\n", name, err)
			return
		}
		fmt.Printf("✔ %s%s\n", name, detail)
	}

	// The local transfer directory is checked first because it needs no
	// network and is the setting people most often get wrong.
	if cfg.LocalDir == "" {
		fmt.Println("• no local directory set (GDRIVE_LOCAL_DIR): downloads and uploads are off; inline text still works")
	} else {
		check("local directory writable", checkLocalDir(cfg.LocalDir), " "+cfg.LocalDir)
	}

	ts, _, err := p.tokenSource(ctx)
	check("credentials found", err, "")
	if err != nil {
		fmt.Println("\nRun `google-drive-mcp login` first.")
		return 1
	}
	tok, err := ts.Token()
	check("refresh token exchange", err, "")
	if err != nil {
		return 1
	}
	wanted := p.scopes()
	if info, err := auth.Inspect(ctx, nil, tok.AccessToken); err != nil {
		check("token inspection", err, "")
	} else if missing := auth.HasScopes(info.Scopes, wanted); len(missing) > 0 {
		check("granted scopes", fmt.Errorf("missing %s; re-run login and approve every checkbox", strings.Join(missing, ", ")), "")
	} else {
		check("granted scopes", nil, fmt.Sprintf(" (%d scopes, token valid %s)", len(info.Scopes), info.ExpiresIn.Round(time.Second)))
	}

	api := newClient(ts, cfg, slog.New(slog.DiscardHandler))
	about, err := api.About(ctx)
	if err != nil {
		check("Drive API (about.get)", err, "")
		fmt.Printf("\n%d check(s) failed\n", failed)
		return 1
	}
	check("Drive API (about.get)", nil, " as "+about.User.EmailAddress)
	if about.CanCreateDrives {
		drives, err := api.ListDrives(ctx, gapi.ListDrivesOptions{})
		if err != nil {
			check("shared drives (drives.list)", err, "")
		} else {
			check("shared drives (drives.list)", nil, fmt.Sprintf(" %d visible", len(drives.Drives)))
		}
	} else {
		fmt.Println("• this account cannot create shared drives; they are a Google Workspace feature")
	}

	svc := newService(api, cfg, slog.New(slog.DiscardHandler))
	if fs.NArg() > 0 {
		target := fs.Arg(0)
		res, err := svc.Resolve(ctx, target, service.ResolveOptions{})
		if err != nil {
			check("files.get on the reference given", err, "")
		} else {
			loc := svc.Location(ctx, res.File)
			check("files.get on the reference given", nil,
				fmt.Sprintf(" %q (%s) in %s", res.File.Name, model.Kind(res.File), loc))
		}
	} else {
		fmt.Println("• pass a file id, URL or path to also test files.get and path resolution")
	}

	if failed > 0 {
		fmt.Printf("\n%d check(s) failed\n", failed)
		return 1
	}
	fmt.Println("\nall checks passed")
	return 0
}

// checkLocalDir confirms the transfer directory exists and can be
// written, which is the difference between a download working and a
// confusing failure halfway through one.
func checkLocalDir(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("%s: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	f, err := os.CreateTemp(dir, ".google-drive-mcp-doctor-*")
	if err != nil {
		return fmt.Errorf("%s is not writable: %w", dir, err)
	}
	name := f.Name()
	_ = f.Close()
	return os.Remove(name)
}

func orUnset(s string) string {
	if s == "" {
		return "(unset)"
	}
	return s
}
