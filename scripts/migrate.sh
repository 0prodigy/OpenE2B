#!/bin/bash
# E2B Server Database Migration Script
# Usage: ./scripts/migrate.sh [DATABASE_URL]
#
# This script applies all migrations to the database.
# You can provide the DATABASE_URL as an argument or set it as an environment variable.

set -e

# Get database URL from argument or environment
DATABASE_URL="${1:-$DATABASE_URL}"

if [ -z "$DATABASE_URL" ]; then
    echo "Error: DATABASE_URL is required"
    echo "Usage: ./scripts/migrate.sh [DATABASE_URL]"
    echo "   or: DATABASE_URL=... ./scripts/migrate.sh"
    exit 1
fi

MIGRATIONS_DIR="$(dirname "$0")/../internal/db/migrations"

echo "Applying migrations from: $MIGRATIONS_DIR"
echo ""

# Apply each migration file in order
for migration in $(ls -1 "$MIGRATIONS_DIR"/*.sql | sort); do
    echo "Applying: $(basename "$migration")"
    psql "$DATABASE_URL" -f "$migration"
    echo "Done: $(basename "$migration")"
    echo ""
done

echo "All migrations applied successfully!"

