package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/rangka-os/rangka-os/kernel/internal/server"
)

func cmdServe(args []string) int {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	var c common
	c.flags(fs)
	addr := fs.String("addr", envOr("RANGKA_ADDR", ":8080"), "listen address")
	web := fs.String("web", os.Getenv("RANGKA_WEB"), "static web directory served at / (default: rangka-os/web)")
	templates := fs.String("templates", os.Getenv("RANGKA_TEMPLATES"), "theme/layout templates (default: rangka-os/templates)")
	org := fs.String("org", "", "organisation id (default: the site directory name)")
	deploy := fs.String("deploy-target", envOr("RANGKA_DEPLOY_TARGET", "dev"), "dev, docker, helm, desktop, cloud or partner (shown in /system/status)")
	devOpen := fs.Bool("dev-open", false, "serve /api and /mcp without sign-in, as the org owner (local demos only)")
	fs.Parse(args)
	set, err := loadSchemas(c.schemas)
	if err != nil {
		return fail(err)
	}
	if c.db == "" {
		return fail(errNoDB)
	}
	appsParent := filepath.Dir(filepath.Clean(c.apps))
	cfg := server.Config{
		AppsDir: c.apps, SiteDir: c.site, DatabaseURL: c.db, Addr: *addr, Org: *org, DevOpen: *devOpen, Schemas: set, Version: Version,
		WebDir:        server.FindDir(*web, "web", "../web", filepath.Join(appsParent, "..", "rangka-os", "web")),
		TemplatesDir:  server.FindDir(*templates, "templates", "../templates", filepath.Join(appsParent, "..", "rangka-os", "templates")),
		ConnectorsDir: c.connectorsDir(), DeployTarget: *deploy,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	s, err := server.New(ctx, cfg)
	if err != nil {
		return fail(err)
	}
	defer s.Close()
	s.Start(ctx)
	log.Printf("rangka %s serving site %q (org %s) on %s; web %s; dev-open=%v", Version, c.site, s.Cfg.Org, *addr, cfg.WebDir, *devOpen)
	if *devOpen {
		log.Printf("WARNING: --dev-open: every request acts as the owner of %s; never use this on a network", s.Cfg.Org)
	}
	if err := s.ListenAndServe(ctx); err != nil && err.Error() != "http: Server closed" {
		return fail(err)
	}
	stop()
	s.Wait()
	return 0
}
