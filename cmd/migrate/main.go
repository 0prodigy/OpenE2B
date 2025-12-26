package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
)

func main() {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL environment variable is required")
	}

	ctx := context.Background()

	// Connect to database
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer conn.Close(ctx)

	log.Println("Connected to database")

	// Find migration files
	migrationsDir := "internal/db/migrations"
	if len(os.Args) > 1 {
		migrationsDir = os.Args[1]
	}

	files, err := os.ReadDir(migrationsDir)
	if err != nil {
		log.Fatalf("Failed to read migrations directory: %v", err)
	}

	// Sort files by name
	var sqlFiles []string
	for _, f := range files {
		if !f.IsDir() && strings.HasSuffix(f.Name(), ".sql") {
			sqlFiles = append(sqlFiles, f.Name())
		}
	}
	sort.Strings(sqlFiles)

	if len(sqlFiles) == 0 {
		log.Println("No migration files found")
		return
	}

	// Apply each migration
	for _, filename := range sqlFiles {
		path := filepath.Join(migrationsDir, filename)
		log.Printf("Applying migration: %s", filename)

		content, err := os.ReadFile(path)
		if err != nil {
			log.Fatalf("Failed to read migration file %s: %v", filename, err)
		}

		// Execute the migration
		_, err = conn.Exec(ctx, string(content))
		if err != nil {
			log.Fatalf("Failed to apply migration %s: %v", filename, err)
		}

		log.Printf("Applied: %s", filename)
	}

	log.Println("All migrations applied successfully!")

	// Verify tables were created
	log.Println("\nVerifying tables...")
	tables := []string{"teams", "team_users", "templates", "builds", "sandboxes", "nodes", "api_keys", "access_tokens"}
	for _, table := range tables {
		var exists bool
		err := conn.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT FROM information_schema.tables 
				WHERE table_schema = 'public' 
				AND table_name = $1
			)
		`, table).Scan(&exists)
		if err != nil {
			log.Printf("  Error checking table %s: %v", table, err)
		} else if exists {
			fmt.Printf("  ✓ Table '%s' exists\n", table)
		} else {
			fmt.Printf("  ✗ Table '%s' NOT found\n", table)
		}
	}
}

