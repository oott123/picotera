package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"picotera/db/migrations"
	"picotera/pkg/auth"
	"picotera/pkg/configx"
	"picotera/pkg/db"
	"picotera/pkg/llmbridge"
	"picotera/pkg/server"
	"time"

	"github.com/danielgtaylor/huma/v2/humacli"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"
)

type Options struct{}

func main() {
	cli := humacli.New(func(h humacli.Hooks, o *Options) {
		h.OnStart(func() {
			ctx := context.Background()
			server, err := server.NewServer(ctx)
			if err != nil {
				log.Fatalf("failed to create server: %v", err)
			}

			err = server.Serve()
			if err != nil {
				log.Fatalf("failed to serve: %v", err)
			}
		})
	})

	cli.Root().AddCommand(&cobra.Command{
		Use:   "openapi",
		Short: "Generate OpenAPI specification",
		Run: func(cmd *cobra.Command, args []string) {
			api := server.NewHuma()
			b, _ := api.OpenAPI().DowngradeYAML()
			fmt.Println(string(b))
		},
	})

	precompileCmd := &cobra.Command{
		Use:   "precompile-llmbridge-wasm",
		Short: "Precompile the llmbridge WASM module into the wazero cache",
		Run: func(cmd *cobra.Command, args []string) {
			ctx := context.Background()
			config, err := configx.Parse()
			if err != nil {
				log.Fatalf("failed to parse config: %v", err)
			}
			if err := llmbridge.Precompile(ctx, llmbridge.Config{
				WASMPath:    config.LLMBridgeWASMPath,
				CacheDir:    config.LLMBridgeWASMCacheDir,
				RuntimeMode: config.LLMBridgeWASMRuntime,
			}); err != nil {
				log.Fatalf("failed to precompile llmbridge wasm: %v", err)
			}
			cacheDir := config.LLMBridgeWASMCacheDir
			if cacheDir == "" {
				cacheDir = llmbridge.DefaultCacheDir(config.LLMBridgeWASMPath)
			}
			fmt.Printf("precompiled %s into %s\n", config.LLMBridgeWASMPath, cacheDir)
		},
	}
	cli.Root().AddCommand(precompileCmd)

	var (
		enrollNew      bool
		enrollReset    bool
		enrollUsername string
	)

	enrollCmd := &cobra.Command{
		Use:   "enroll-admin",
		Short: "Issue a passkey enrollment URL for an admin account",
		Long: `Issue a one-time enrollment URL the operator opens in a browser to register
a passkey for an admin account.

Default behavior errors if an admin already exists; pass --new to add another
admin or --reset --username NAME to recover a specific existing admin.`,
		Run: func(cmd *cobra.Command, args []string) {
			ctx := context.Background()
			config, err := configx.Parse()
			if err != nil {
				log.Fatalf("parse config: %v", err)
			}
			if len(config.PublicOrigins) == 0 {
				log.Fatalf("PICOTERA_PUBLIC_ORIGIN is not set; the enrollment URL needs a base origin")
			}
			// Run migrations so the CLI works on a fresh DB before the server has been started.
			if _, err := migrations.UpWithResult(config.DatabaseURL); err != nil {
				log.Fatalf("apply migrations: %v", err)
			}
			conn, err := pgxpool.New(ctx, config.DatabaseURL)
			if err != nil {
				log.Fatalf("connect db: %v", err)
			}
			defer conn.Close()
			q := db.New(conn)

			var (
				token string
				exp   time.Time
				label string
			)

			switch {
			case enrollReset:
				if enrollUsername == "" {
					log.Fatalf("--reset requires --username NAME")
				}
				a, err := q.GetAccountByUsername(ctx, enrollUsername)
				if err != nil {
					if errors.Is(err, pgx.ErrNoRows) {
						log.Fatalf("user %q not found", enrollUsername)
					}
					log.Fatalf("look up user: %v", err)
				}
				if a.Role != "admin" {
					log.Fatalf("user %q has role %q; --reset only handles admin accounts. Non-admin recovery is done via the dashboard's reissue-enrollment flow.", a.Username, a.Role)
				}
				id := a.ID
				token, exp, err = auth.IssueEnrollment(ctx, q, auth.IntentReset, &id, auth.DefaultEnrollmentTTL)
				if err != nil {
					log.Fatalf("issue enrollment: %v", err)
				}
				label = fmt.Sprintf("Reset enrollment for %q", a.Username)

			case enrollNew:
				token, exp, err = auth.IssueEnrollment(ctx, q, auth.IntentBootstrap, nil, auth.DefaultEnrollmentTTL)
				if err != nil {
					log.Fatalf("issue enrollment: %v", err)
				}
				label = "Additional admin enrollment"

			default:
				has, err := q.HasAnyActiveAdmin(ctx)
				if err != nil {
					log.Fatalf("status check: %v", err)
				}
				if has {
					log.Fatalf("an admin already exists. Pass --new to add another admin, or --reset --username NAME to recover a specific admin.")
				}
				token, exp, err = auth.IssueEnrollment(ctx, q, auth.IntentBootstrap, nil, auth.DefaultEnrollmentTTL)
				if err != nil {
					log.Fatalf("issue enrollment: %v", err)
				}
				label = "Bootstrap admin enrollment"
			}

			url := config.PublicOrigins[0] + "/enroll/" + token
			fmt.Printf("%s URL (expires %s):\n%s\n", label, exp.Format(time.RFC3339), url)
		},
	}
	enrollCmd.Flags().BoolVar(&enrollNew, "new", false, "Always create a new admin enrollment (do not error if admins exist).")
	enrollCmd.Flags().BoolVar(&enrollReset, "reset", false, "Issue a reset enrollment for an existing admin (use with --username).")
	enrollCmd.Flags().StringVar(&enrollUsername, "username", "", "Target username for --reset.")
	cli.Root().AddCommand(enrollCmd)

	cli.Run()
}
