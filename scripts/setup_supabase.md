# Supabase Setup Guide for E2B Server

This guide explains how to set up Supabase as the database backend for E2B Server.

## Prerequisites

- A Supabase account (free tier works fine)
- Access to the Supabase Dashboard

## Step 1: Create a Supabase Project

1. Go to [Supabase Dashboard](https://app.supabase.com/)
2. Click "New Project"
3. Choose your organization
4. Enter a project name (e.g., "e2b-server")
5. Generate a secure database password (save this!)
6. Select a region close to your deployment
7. Click "Create new project"

## Step 2: Get Connection Details

Once your project is created:

1. Go to **Project Settings** → **Database**
2. Under "Connection string", copy the **URI** format
3. Replace `[YOUR-PASSWORD]` with your database password

Your connection string should look like:
```
postgresql://postgres.[project-ref]:[password]@aws-0-[region].pooler.supabase.com:5432/postgres
```

## Step 3: Get API Keys

For future API authentication features:

1. Go to **Project Settings** → **API**
2. Note down:
   - **Project URL** (e.g., `https://[project-ref].supabase.co`)
   - **anon public** key
   - **service_role** key (keep this secret!)

## Step 4: Run Migrations

Option A: Using the migration script:
```bash
DATABASE_URL="postgresql://postgres.[project-ref]:[password]@aws-0-[region].pooler.supabase.com:5432/postgres" ./scripts/migrate.sh
```

Option B: Using the Supabase SQL Editor:
1. Go to **SQL Editor** in your Supabase Dashboard
2. Click "New Query"
3. Copy and paste the contents of `internal/db/migrations/001_initial_schema.sql`
4. Click "Run"

## Step 5: Configure E2B Server

Set the following environment variables:

```bash
# Required for database connection
export DATABASE_URL="postgresql://postgres.[project-ref]:[password]@aws-0-[region].pooler.supabase.com:5432/postgres"

# Optional: Direct Supabase API access (for future features)
export SUPABASE_URL="https://[project-ref].supabase.co"
export SUPABASE_ANON_KEY="eyJ..."
export SUPABASE_SECRET_KEY="sb_secret_..."
```

## Step 6: Start the Server

```bash
make run-api
```

Or with explicit environment variables:
```bash
DATABASE_URL="..." ./bin/api
```

## Environment Variables Reference

| Variable | Required | Description |
|----------|----------|-------------|
| `DATABASE_URL` | Yes | PostgreSQL connection string |
| `SUPABASE_URL` | No | Supabase project URL (for future features) |
| `SUPABASE_ANON_KEY` | No | Supabase anonymous key |
| `SUPABASE_SECRET_KEY` | No | Supabase service role key |
| `USE_IN_MEMORY_STORE` | No | Set to "true" to use in-memory storage |
| `E2B_DOMAIN` | No | Base domain (default: e2b.local) |
| `E2B_API_PORT` | No | API port (default: 3000) |

## Troubleshooting

### Connection refused
- Check that your connection string is correct
- Ensure your IP is allowed in Supabase (Settings → Database → Network)

### SSL required
- Use the connection string with SSL mode: `?sslmode=require`

### Permission denied
- Make sure you're using the correct password
- Check that the database user has appropriate permissions

## Security Notes

1. **Never commit credentials** to version control
2. Use environment variables or a secrets manager
3. The `service_role` key has full access - keep it secret
4. Enable Row Level Security (RLS) for production use

