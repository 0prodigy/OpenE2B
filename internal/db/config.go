package db

import (
	"fmt"
	"os"
)

// Config holds database configuration
type Config struct {
	// Supabase configuration
	SupabaseURL    string
	SupabaseKey    string
	SupabaseSecret string

	// Direct PostgreSQL connection (optional, for migrations)
	DatabaseURL string

	// Use in-memory store instead of Supabase (for development)
	UseInMemory bool
}

// LoadConfig loads database configuration from environment variables
func LoadConfig() *Config {
	cfg := &Config{
		SupabaseURL:    os.Getenv("SUPABASE_URL"),
		SupabaseKey:    os.Getenv("SUPABASE_ANON_KEY"),
		SupabaseSecret: os.Getenv("SUPABASE_SECRET_KEY"),
		DatabaseURL:    os.Getenv("DATABASE_URL"),
		UseInMemory:    os.Getenv("USE_IN_MEMORY_STORE") == "true",
	}

	// If no Supabase URL is set, default to in-memory
	if cfg.SupabaseURL == "" && cfg.DatabaseURL == "" {
		cfg.UseInMemory = true
	}

	return cfg
}

// Validate validates the configuration
func (c *Config) Validate() error {
	if c.UseInMemory {
		return nil
	}

	if c.SupabaseURL == "" && c.DatabaseURL == "" {
		return fmt.Errorf("either SUPABASE_URL or DATABASE_URL must be set")
	}

	if c.SupabaseURL != "" && c.SupabaseSecret == "" {
		return fmt.Errorf("SUPABASE_SECRET_KEY is required when using Supabase")
	}

	return nil
}

// GetPostgresURL returns the PostgreSQL connection URL
// Supabase provides this in the format: postgresql://postgres:[password]@[host]:5432/postgres
func (c *Config) GetPostgresURL() string {
	if c.DatabaseURL != "" {
		return c.DatabaseURL
	}

	// Construct from Supabase URL if possible
	// Format: https://[project-ref].supabase.co -> postgres://postgres.[project-ref]:[password]@aws-0-[region].pooler.supabase.com:5432/postgres
	return ""
}

