package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"picotera/pkg/configx"
	"picotera/pkg/db"
	"picotera/pkg/server"

	"github.com/danielgtaylor/huma/v2/humacli"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"
)

type Options struct{}

func main() {
	cli := humacli.New(func(h humacli.Hooks, o *Options) {
		var srv *server.Server
		h.OnStart(func() {
			ctx := context.Background()
			var err error
			srv, err = server.NewServer(ctx)
			if err != nil {
				log.Fatalf("failed to create server: %v", err)
			}

			err = srv.Serve()
			if err != nil {
				log.Fatalf("failed to serve: %v", err)
			}
		})
		h.OnStop(func() {
			if srv == nil {
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			if err := srv.Shutdown(ctx); err != nil {
				log.Printf("failed to shutdown cleanly: %v", err)
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

	cli.Root().AddCommand(&cobra.Command{
		Use:   "set-admin <user-id>",
		Short: "Grant admin to a user by id",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				log.Fatalf("invalid user id %q: %v", args[0], err)
			}

			ctx := context.Background()
			config, err := configx.Parse()
			if err != nil {
				log.Fatalf("failed to parse config: %v", err)
			}

			pool, err := pgxpool.New(ctx, config.DatabaseURL)
			if err != nil {
				log.Fatalf("failed to connect to database: %v", err)
			}
			defer pool.Close()

			user, err := db.New(pool).UpdateUserAdmin(ctx, db.UpdateUserAdminParams{ID: id, IsAdmin: true})
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					log.Printf("user %d not found", id)
					os.Exit(1)
				}
				log.Fatalf("failed to set admin: %v", err)
			}

			fmt.Printf("user %d (%s) is now an admin\n", user.ID, user.DisplayName)
		},
	})

	bindIdentity := &cobra.Command{
		Use:   "bind-identity <provider> <identity> <user-id>",
		Short: "Bind an identity (provider, identity) to a user by id",
		Args:  cobra.ExactArgs(3),
		Run: func(cmd *cobra.Command, args []string) {
			provider, identity := args[0], args[1]
			id, err := strconv.ParseInt(args[2], 10, 64)
			if err != nil {
				log.Fatalf("invalid user id %q: %v", args[2], err)
			}
			force, err := cmd.Flags().GetBool("force")
			if err != nil {
				log.Fatalf("failed to read --force: %v", err)
			}

			ctx := context.Background()
			config, err := configx.Parse()
			if err != nil {
				log.Fatalf("failed to parse config: %v", err)
			}

			pool, err := pgxpool.New(ctx, config.DatabaseURL)
			if err != nil {
				log.Fatalf("failed to connect to database: %v", err)
			}
			defer pool.Close()
			q := db.New(pool)

			if _, err := q.GetUserByID(ctx, id); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					log.Printf("user %d not found", id)
					os.Exit(1)
				}
				log.Fatalf("failed to look up user: %v", err)
			}

			existing, err := q.GetUserIdentity(ctx, db.GetUserIdentityParams{Provider: provider, Identity: identity})
			switch {
			case err == nil:
				if existing.UserID == id {
					fmt.Printf("identity (%s, %s) is already bound to user %d\n", provider, identity, id)
					return
				}
				if !force {
					log.Printf("identity (%s, %s) is already bound to user %d; pass --force to rebind", provider, identity, existing.UserID)
					os.Exit(1)
				}
				if _, err := q.UpdateUserIdentityUser(ctx, db.UpdateUserIdentityUserParams{Provider: provider, Identity: identity, UserID: id}); err != nil {
					log.Fatalf("failed to rebind identity: %v", err)
				}
				fmt.Printf("identity (%s, %s) rebound from user %d to user %d\n", provider, identity, existing.UserID, id)
			case errors.Is(err, pgx.ErrNoRows):
				if _, err := q.InsertUserIdentity(ctx, db.InsertUserIdentityParams{UserID: id, Provider: provider, Identity: identity}); err != nil {
					log.Fatalf("failed to bind identity: %v", err)
				}
				fmt.Printf("identity (%s, %s) bound to user %d\n", provider, identity, id)
			default:
				log.Fatalf("failed to look up identity: %v", err)
			}
		},
	}
	bindIdentity.Flags().BoolP("force", "f", false, "rebind even if the identity is already bound to another user")
	cli.Root().AddCommand(bindIdentity)

	cli.Run()
}
