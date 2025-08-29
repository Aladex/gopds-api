# Configuration System Migration Guide

## Overview

The configuration system has been completely refactored to provide better structure, validation, and environment variable support.

## Key Improvements

### 1. **Structured Configuration**
- Type-safe configuration with Go structs
- Compile-time validation of config fields
- Better IDE support with autocomplete

### 2. **Environment Variables Support**
- All config values can be overridden with environment variables
- Prefix: `GOPDS_`
- Nested values use underscore notation (e.g., `GOPDS_SERVER_PORT`)

### 3. **Validation & Defaults**
- Automatic validation of required fields
- Sensible default values for optional settings
- Path validation and automatic directory creation

### 4. **Better Error Handling**
- Detailed error messages for configuration issues
- Graceful handling of missing config files
- Connection testing for external services

## Migration Steps

### 1. Update Dependencies
No new dependencies required - the new system still uses viper internally.

### 2. Update Configuration File
Add the new `server` section to your `config.yaml`:

```yaml
server:
  host: "127.0.0.1"
  port: 8085
  read_timeout: 10
  write_timeout: 10
  max_header_bytes: 1048576
```

### 3. Environment Variables (Optional)
Create `.env` file from `.env.example` for local development:
```bash
cp .env.example .env
# Edit .env with your local settings
```

### 4. Code Changes
The main application code has been updated to use the new config system. Key changes:

- `viper.GetString("app.files_path")` → `cfg.App.FilesPath`
- `viper.GetBool("app.devel_mode")` → `cfg.App.DevelMode`
- `viper.GetString("postgres.dbuser")` → `cfg.Postgres.DBUser`

## Environment Variable Examples

```bash
# Override server port
export GOPDS_SERVER_PORT=9000

# Override database settings
export GOPDS_POSTGRES_DBHOST=remote-db:5432
export GOPDS_POSTGRES_DBUSER=production_user

# Enable development mode
export GOPDS_APP_DEVEL_MODE=true
```

## Docker Integration

Environment variables work seamlessly with Docker:

```yaml
# docker-compose.yml
services:
  gopds-api:
    environment:
      - GOPDS_SERVER_PORT=8085
      - GOPDS_POSTGRES_DBHOST=postgres:5432
      - GOPDS_REDIS_HOST=redis
```

## Configuration Validation

The system now validates:
- Required fields (secret keys, database credentials)
- Port ranges (1-65535)
- Directory paths (creates if missing)
- Database and Redis connectivity

## Backward Compatibility

The new system is backward compatible with existing `config.yaml` files. Only the `server` section needs to be added for full functionality.

## Troubleshooting

### Config File Not Found
If `config.yaml` is missing, the system will use defaults and environment variables with a warning.

### Validation Errors
Detailed error messages will indicate which configuration values are invalid or missing.

### Connection Issues
Database and Redis connections are tested during startup with clear error messages.

## Best Practices

1. **Use environment variables for secrets** in production
2. **Keep config.yaml for static settings** like paths and timeouts
3. **Use .env file for local development** to override defaults
4. **Validate configuration** before deployment using the built-in validation

## Future Enhancements

- Configuration hot-reloading
- Web-based configuration interface
- Configuration templates for different environments
- Encrypted configuration values
