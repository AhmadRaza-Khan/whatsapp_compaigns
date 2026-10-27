// Command app runs the WhatsApp campaign platform.
//
//	app server        HTTP API (+ built frontend when STATIC_DIR is set)
//	app worker        background sender, scheduler, webhook processor
//	app all           server and worker in one process (local development)
//	app migrate       apply database migrations
//	app create-admin  -email you@example.com -name "Name" [-password ...]
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // Asia/Qatar must resolve even in minimal containers

	"github.com/jackc/pgx/v5/pgxpool"

	"wacampaigns/internal/api"
	"wacampaigns/internal/auth"
	"wacampaigns/internal/bird"
	"wacampaigns/internal/config"
	"wacampaigns/internal/db"
	"wacampaigns/internal/worker"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: app server|worker|all|migrate|create-admin")
		os.Exit(2)
	}
	if err := run(os.Args[1], os.Args[2:]); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(cmd string, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		return err
	}
	bc := bird.New(cfg.BirdAPIHost, cfg.BirdAPIKey)

	switch cmd {
	case "migrate":
		slog.Info("migrations up to date")
		return nil
	case "create-admin":
		return createAdmin(ctx, pool, args)
	case "server":
		return serve(ctx, cfg, api.New(pool, cfg, bc))
	case "worker":
		return worker.New(pool, bc, cfg).Run(ctx)
	case "all":
		errc := make(chan error, 2)
		go func() { errc <- worker.New(pool, bc, cfg).Run(ctx) }()
		go func() { errc <- serve(ctx, cfg, api.New(pool, cfg, bc)) }()
		return <-errc
	}
	return fmt.Errorf("unknown command %q", cmd)
}

func serve(ctx context.Context, cfg *config.Config, s *api.Server) error {
	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           s.Router(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      120 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	slog.Info("http server listening", "port", cfg.Port, "public_url", cfg.PublicURL)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// createAdmin creates an admin, or resets the password of an existing user
// and makes them admin (useful if the admin is locked out).
func createAdmin(ctx context.Context, pool *pgxpool.Pool, args []string) error {
	fs := flag.NewFlagSet("create-admin", flag.ExitOnError)
	email := fs.String("email", os.Getenv("ADMIN_EMAIL"), "admin email")
	name := fs.String("name", os.Getenv("ADMIN_NAME"), "admin name")
	password := fs.String("password", os.Getenv("ADMIN_PASSWORD"), "password (min 10 chars); generated if empty")
	_ = fs.Parse(args)
	if *email == "" {
		return errors.New("-email is required")
	}
	pw := *password
	generated := pw == ""
	if generated {
		tok, _ := auth.RandomToken()
		pw = tok[:16]
	}
	hash, err := auth.HashPassword(pw)
	if err != nil {
		return err
	}
	_, err = pool.Exec(ctx, `INSERT INTO users (email, name, role, password_hash) VALUES ($1,$2,'admin',$3)
		ON CONFLICT (email) DO UPDATE SET password_hash=EXCLUDED.password_hash, role='admin', active=true,
		  password_changed_at=now(), updated_at=now()`,
		strings.ToLower(strings.TrimSpace(*email)), *name, hash)
	if err != nil {
		return err
	}
	fmt.Printf("Admin ready: %s\n", *email)
	if generated {
		fmt.Printf("Generated password: %s\n(change it after first login)\n", pw)
	}
	return nil
}
