package main

import (
	"context"
	"flag"
	"log"
	"log/slog"
	"net"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/rangka-os/rangka-os/kernel/internal/api"
	"github.com/rangka-os/rangka-os/kernel/internal/bundle"
	"github.com/rangka-os/rangka-os/kernel/internal/logx"
	"github.com/rangka-os/rangka-os/kernel/internal/server"
)

func cmdServe(args []string) int {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	var c common
	c.flags(fs)
	addr := fs.String("addr", envOr("RANGKA_ADDR", ":8080"), "listen address")
	web := fs.String("web", os.Getenv("RANGKA_WEB"), "static web directory served at / (default: rangka-os/web)")
	templates := fs.String("templates", os.Getenv("RANGKA_TEMPLATES"), "theme/layout templates (default: rangka-os/templates)")
	org := fs.String("org", os.Getenv("RANGKA_ORG"), "organisation id (default $RANGKA_ORG, else the site directory name)")
	orgName := fs.String("org-name", os.Getenv("RANGKA_ORG_NAME"), "the organisation's display name, set on every start (default $RANGKA_ORG_NAME; unset: a new org is named after its id)")
	deploy := fs.String("deploy-target", envOr("RANGKA_DEPLOY_TARGET", "dev"), "dev, docker, helm, desktop, cloud or partner (shown in /system/status)")
	filesDir := fs.String("files-dir", envOr("RANGKA_FILES_DIR", ""), "attachments directory (default: system.rk files.path, else ./data/files)")
	devOpen := fs.Bool("dev-open", false, "serve /api and /mcp without sign-in, as the org owner (local demos only)")
	aiURL := fs.String("ai-url", os.Getenv("RANGKA_AI_URL"), "rangka-ai sidecar URL, e.g. http://127.0.0.1:8787 (unset: the assistant is off)")
	aiCallback := fs.String("ai-callback-url", os.Getenv("RANGKA_AI_CALLBACK_URL"), "kernel URL the sidecar reaches /mcp on (default http://127.0.0.1 + the listen port)")
	requestOnly := fs.Bool("request-only", envBool("RANGKA_REQUEST_ONLY"), "for platforms that freeze the process between requests (Lambda, Cloud Run): no migrate at start, no background loops; drive the scheduler with POST /_rangka/tick (contract 0040)")
	logFormat := fs.String("log-format", envOr("RANGKA_LOG_FORMAT", logx.FormatAuto), "auto, json or text: auto is json when stdout is not a terminal, text on a terminal (docs/logging.md)")
	fs.Parse(args)
	format, err := logx.ResolveFormat(*logFormat, os.Stdout)
	if err != nil {
		return fail(err)
	}
	var logger *slog.Logger
	logf := log.Printf
	if format == logx.FormatJSON {
		registerLogSecrets(c.db)
		logger = logx.NewJSON(os.Stdout, slog.LevelInfo)
		// log.Printf anywhere in the process now writes JSON lines through the same redacting handler
		slog.SetDefault(logger.With(slog.String("component", logx.CompKernel)))
		logf = logx.Logf(logger, logx.CompKernel)
	}
	set, err := loadSchemas(c.schemas)
	if err != nil {
		return fail(err)
	}
	if c.db == "" {
		return fail(errNoDB)
	}
	appsParent := filepath.Dir(filepath.Clean(c.apps))
	cfg := server.Config{
		AppsDir: c.apps, SiteDir: c.site, DatabaseURL: c.db, Addr: *addr, Org: *org, OrgName: strings.TrimSpace(*orgName), DevOpen: *devOpen, Schemas: set, Version: Version,
		WebDir:        server.FindDir(*web, "web", "../web", filepath.Join(appsParent, "..", "rangka-os", "web"), bundle.Dir("web")),
		TemplatesDir:  server.FindDir(*templates, "templates", "../templates", filepath.Join(appsParent, "..", "rangka-os", "templates"), bundle.Dir("templates")),
		ConnectorsDir: c.connectorsDir(), ModulesDir: c.modules, DeployTarget: *deploy, FilesDir: *filesDir,
		RequestOnly: *requestOnly, InternalToken: os.Getenv("RANGKA_INTERNAL_TOKEN"), Logger: logger,
	}
	if *aiURL != "" {
		cfg.AI = &api.AIConfig{URL: *aiURL, CallbackURL: aiCallbackURL(*aiCallback, *addr), Secret: os.Getenv("RANGKA_AI_SECRET")}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	s, err := server.New(ctx, cfg)
	if err != nil {
		return fail(err)
	}
	defer s.Close()
	if *requestOnly {
		logf("request-only: no background loops; the scheduler and queue run on POST /_rangka/tick (internal endpoints %s)",
			map[bool]string{true: "on", false: "off: RANGKA_INTERNAL_TOKEN is not set"}[cfg.InternalToken != ""])
	} else {
		s.Start(ctx)
	}
	logf("rangka %s serving site %q (org %s) on %s; web %s; dev-open=%v", Version, c.site, s.Cfg.Org, *addr, cfg.WebDir, *devOpen)
	if *devOpen {
		logf("WARNING: --dev-open: every request acts as the owner of %s; never use this on a network", s.Cfg.Org)
	}
	if cfg.AI != nil {
		logf("AI assistant: sidecar %s, MCP callback %s/mcp", cfg.AI.URL, cfg.AI.CallbackURL)
	}
	if err := s.ListenAndServe(ctx); err != nil && err.Error() != "http: Server closed" {
		return fail(err)
	}
	stop()
	s.Wait()
	return 0
}

// logSecretEnv names the environment variables whose values never appear in a JSON log line.
var logSecretEnv = []string{
	"RANGKA_INTERNAL_TOKEN", "RANGKA_AI_SECRET", "RANGKA_MASTER_KEY", "RANGKA_HOST_NOTICES_SECRET", "RANGKA_USER_PASSWORD",
	"VAULT_TOKEN", "DATABASE_URL",
}

// registerLogSecrets tells the log redactor the secrets this process knows at start: the database URL and its
// password, and the secret environment variables.
func registerLogSecrets(dbURL string) {
	logx.RegisterSecret(dbURL)
	if u, err := url.Parse(dbURL); err == nil && u.User != nil {
		// URL passwords are redacted by pattern anyway; a short local one ("rangka") would blank ordinary words
		if pw, ok := u.User.Password(); ok && len(pw) >= 12 {
			logx.RegisterSecret(pw)
		}
	}
	for _, name := range logSecretEnv {
		logx.RegisterSecret(os.Getenv(name))
	}
}

// envBool reads a boolean environment variable: 1, true, yes or on (any case) are true.
func envBool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// aiCallbackURL is the kernel URL the sidecar calls MCP on: the flag, else http://127.0.0.1 with the listen port.
func aiCallbackURL(flagValue, addr string) string {
	if flagValue != "" {
		return flagValue
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		return "http://127.0.0.1:8080"
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port)
}
