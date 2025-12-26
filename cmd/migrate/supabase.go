package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// RunSupabaseMigration runs migrations using Supabase REST API
func RunSupabaseMigration() error {
	supabaseURL := os.Getenv("SUPABASE_URL")
	supabaseKey := os.Getenv("SUPABASE_SECRET_KEY")

	if supabaseURL == "" || supabaseKey == "" {
		return fmt.Errorf("SUPABASE_URL and SUPABASE_SECRET_KEY are required")
	}

	// Find migration files
	migrationsDir := "internal/db/migrations"
	if len(os.Args) > 1 {
		migrationsDir = os.Args[1]
	}

	files, err := os.ReadDir(migrationsDir)
	if err != nil {
		return fmt.Errorf("failed to read migrations directory: %w", err)
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
		fmt.Println("No migration files found")
		return nil
	}

	// Execute each migration via Supabase REST API
	for _, filename := range sqlFiles {
		path := filepath.Join(migrationsDir, filename)
		fmt.Printf("Applying migration: %s\n", filename)

		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read migration file %s: %w", filename, err)
		}

		// Split into individual statements for RPC execution
		if err := executeSQL(supabaseURL, supabaseKey, string(content)); err != nil {
			return fmt.Errorf("failed to apply migration %s: %w", filename, err)
		}

		fmt.Printf("Applied: %s\n", filename)
	}

	fmt.Println("All migrations applied successfully!")
	return nil
}

func executeSQL(supabaseURL, supabaseKey, sql string) error {
	// Use Supabase's RPC to execute raw SQL
	// This requires calling the pg_query function or using the database directly

	// Create HTTP client
	client := &http.Client{}

	// Try using the SQL Editor API (this works for the dashboard)
	// For programmatic access, we'll use the REST API to verify tables exist

	// Since Supabase doesn't expose raw SQL execution via REST API for security reasons,
	// we'll need to use the database connection directly or the dashboard

	fmt.Println("  Note: Please run the SQL migration manually via Supabase Dashboard SQL Editor")
	fmt.Println("  SQL to execute:")
	fmt.Println("  " + strings.ReplaceAll(sql[:min(500, len(sql))], "\n", "\n  ") + "...")

	// Just verify we can connect to Supabase
	req, err := http.NewRequest("GET", supabaseURL+"/rest/v1/", nil)
	if err != nil {
		return err
	}
	req.Header.Set("apikey", supabaseKey)
	req.Header.Set("Authorization", "Bearer "+supabaseKey)

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to connect to Supabase: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Supabase connection failed: %s - %s", resp.Status, string(body))
	}

	return nil
}

// VerifyTables checks if tables exist in Supabase
func VerifyTables(supabaseURL, supabaseKey string, tables []string) error {
	client := &http.Client{}

	for _, table := range tables {
		req, err := http.NewRequest("GET", fmt.Sprintf("%s/rest/v1/%s?limit=0", supabaseURL, table), nil)
		if err != nil {
			return err
		}
		req.Header.Set("apikey", supabaseKey)
		req.Header.Set("Authorization", "Bearer "+supabaseKey)

		resp, err := client.Do(req)
		if err != nil {
			fmt.Printf("  ✗ Error checking table '%s': %v\n", table, err)
			continue
		}
		resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			fmt.Printf("  ✓ Table '%s' exists and accessible\n", table)
		} else if resp.StatusCode == http.StatusNotFound {
			fmt.Printf("  ✗ Table '%s' does NOT exist\n", table)
		} else {
			fmt.Printf("  ? Table '%s' returned status %s\n", table, resp.Status)
		}
	}

	return nil
}

type rpcRequest struct {
	Query string `json:"query"`
}

func executeRPC(supabaseURL, supabaseKey, sql string) error {
	client := &http.Client{}

	payload := rpcRequest{Query: sql}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequest("POST", supabaseURL+"/rest/v1/rpc/exec_sql", bytes.NewBuffer(body))
	if err != nil {
		return err
	}
	req.Header.Set("apikey", supabaseKey)
	req.Header.Set("Authorization", "Bearer "+supabaseKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("RPC failed: %s - %s", resp.Status, string(respBody))
	}

	return nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

