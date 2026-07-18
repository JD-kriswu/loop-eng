// Package main is the server entry point.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/yourorg/loopany-go/internal/server"
	"github.com/yourorg/loopany-go/internal/store"
	"github.com/yourorg/loopany-go/pkg/token"
	_ "github.com/lib/pq"
)

var (
	version = "dev"
)

func main() {
	var (
		addr       string
		dbURL      string
		tokenSecret string
		showHelp   bool
	)

	flag.StringVar(&addr, "addr", ":3000", "Server address")
	flag.StringVar(&addr, "a", ":3000", "Server address (shorthand)")
	flag.StringVar(&dbURL, "database-url", "", "Database URL (default: embedded pglite)")
	flag.StringVar(&dbURL, "d", "", "Database URL (shorthand)")
	flag.StringVar(&tokenSecret, "token-secret", "", "Token signing secret (or TOKEN_SECRET env)")
	flag.BoolVar(&showHelp, "help", false, "Show help")
	flag.Parse()

	if showHelp {
		fmt.Println("Loopany Server - Scheduled agent loops platform")
		fmt.Println()
		fmt.Println("Usage: loopany-server [options]")
		fmt.Println()
		fmt.Println("Options:")
		flag.PrintDefaults()
		fmt.Println()
		fmt.Println("Environment:")
		fmt.Println("  DATABASE_URL    PostgreSQL connection string")
		fmt.Println("  TOKEN_SECRET    Token signing secret")
		return
	}

	// Get token secret
	if tokenSecret == "" {
		tokenSecret = os.Getenv("TOKEN_SECRET")
	}
	if tokenSecret == "" {
		tokenSecret = "dev-secret-change-in-production"
		log.Println("Warning: using default token secret, set TOKEN_SECRET for production")
	}

	// Connect to database
	if dbURL == "" {
		dbURL = os.Getenv("DATABASE_URL")
	}
	if dbURL == "" {
		// Use embedded pglite for development
		dbURL = "postgres://postgres@localhost/loopany?sslmode=disable"
		log.Println("Warning: no DATABASE_URL, using default connection")
	}

	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	// Test connection
	if err := db.Ping(); err != nil {
		log.Fatalf("Failed to ping database: %v", err)
	}

	// Run migrations
	if err := store.Migrate(db); err != nil {
		log.Fatalf("Migration failed: %v", err)
	}
	log.Println("Database migrations complete")

	// Create store
	st := store.New(db)

	// Create token generator
	tokenGen := token.NewGenerator(tokenSecret)

	// Create HTTP server
	httpServer := server.NewHTTPServer(addr, st, tokenGen)

	// Create and start scheduler
	scheduler := server.NewScheduler(st)
	ctx, cancel := context.WithCancel(context.Background())
	scheduler.Start(ctx)

	// Handle shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigCh
		log.Printf("Received %v, shutting down...", sig)
		cancel()
		scheduler.Stop()
		httpServer.Shutdown(context.Background())
	}()

	// Start HTTP server
	log.Printf("Loopany server %s starting on %s", version, addr)
	if err := httpServer.Start(); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}