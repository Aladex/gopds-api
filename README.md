<p align="center">
<img src="https://raw.githubusercontent.com/Aladex/gopds-api/master/logo/logo.png" width="350">
</p>

# gopds-api

A comprehensive book management system and OPDS server built with Go, featuring a modern React frontend and extensive API capabilities for managing digital book libraries.

## Technologies

**Backend:**
- **Go 1.23** - High-performance backend API
- **Gin** - Fast HTTP web framework
- **PostgreSQL** - Primary database with full-text search capabilities
- **Redis** - Session storage and caching
- **JWT** - Secure authentication
- **Swagger/OpenAPI** - Automatic API documentation

**Frontend:**
- **React 18** with TypeScript
- **Material-UI (MUI)** - Modern component library
- **i18next** - Internationalization support
- **Axios** - HTTP client

**Infrastructure:**
- **Docker & Docker Compose** - Containerized deployment
- **WebSocket** - Real-time communication
- **CSRF Protection** - Security middleware

## Features

### Core Functionality
- **Digital Library Management** - Complete book catalog with metadata
- **Multi-format Support** - FB2, EPUB, and MOBI file handling
- **Advanced Search** - Full-text search with PostgreSQL trigrams and fuzzy matching
- **Author Management** - Comprehensive author information and search
- **Language Support** - Multi-language book categorization
- **File Download** - Secure book file serving with signed URLs

### User Features
- **User Authentication** - Registration, login, logout with JWT tokens
- **Personal Libraries** - User-specific book collections and favorites
- **Book Collections** - Create and manage custom book collections
- **Collection Voting** - Community-driven collection rating system
- **Profile Management** - User settings and preferences
- **Password Reset** - Email-based password recovery

### OPDS Server
- **OPDS 1.2 Compliant** - Standard e-reader compatibility
- **Authenticated Access** - Secure access to personal libraries
- **Device Synchronization** - Access favorites across multiple devices
- **Search Integration** - Find books directly from e-readers
- **Download Support** - Direct book downloads via OPDS

### Advanced Features
- **File Conversion** - FB2 to MOBI conversion with automatic cleanup
- **WebSocket Support** - Real-time conversion status updates
- **Book Covers** - Poster/cover image management
- **Content Approval** - Book moderation system
- **Session Management** - Redis-based session store with refresh tokens
- **Email Integration** - Registration confirmation and password reset emails
- **Admin Panel** - Administrative functions and user management

### Modern Web Interface
- **Responsive Design** - Mobile-friendly React frontend
- **Material Design** - Clean, modern UI with MUI components
- **Real-time Updates** - WebSocket integration for live status
- **Internationalization** - Multi-language support
- **Advanced Search UI** - Intuitive search and filtering
- **Collection Management** - Visual collection creation and management

## API Documentation

The API includes comprehensive Swagger documentation available at `/swagger/index.html` when running the server. Key endpoints include:

- `/api/books/*` - Book management and search
- `/api/auth/*` - Authentication and registration
- `/api/authors/*` - Author information
- `/opds/*` - OPDS server endpoints
- `/api/collections/*` - Book collection management

## Deployment

### Using Docker Compose (Recommended)

```bash
# Clone the repository
git clone https://github.com/Aladex/gopds-api.git
cd gopds-api

# Copy and configure settings
cp config.yaml.example config.yaml
# Edit config.yaml with your settings

# Start all services
docker-compose up -d
```

### Manual Installation

1. **Prerequisites:**
   - Go 1.23+
   - PostgreSQL 12+
   - Redis 6+
   - Node.js 16+ (for frontend)

2. **Database Setup:**
   ```bash
   # Run migrations
   psql -d your_database -f database_migrations/01-initial.sql
   # Run additional migrations in order...
   ```

3. **Backend:**
   ```bash
   go mod download
   go build -o gopds-api cmd/main.go
   ./gopds-api
   ```

4. **Frontend:**
   ```bash
   cd booksdump-frontend
   npm install
   npm run build
   ```

## Configuration

Configure the application using `config.yaml`. Key settings include:

- **Database connection** - PostgreSQL credentials and settings
- **Redis connection** - Session store configuration
- **File paths** - Book storage and conversion directories
- **Email settings** - SMTP configuration for notifications
- **Security keys** - JWT and session secrets
- **CDN settings** - File serving configuration

See `config.yaml.example` for all available options.

## Development

### Backend Development
```bash
# Install dependencies
go mod download

# Run with hot reload (install air)
air

# Generate API documentation
swag init -g cmd/main.go
```

### Frontend Development
```bash
cd booksdump-frontend
npm install
npm start
```

### Running Tests
```bash
go test ./...
```

## Roadmap

### Completed Features ✅
- ✅ FB2 and EPUB book management
- ✅ MOBI conversion with automatic cleanup
- ✅ React frontend with Material-UI
- ✅ User authentication and session management
- ✅ Book collections and favorites
- ✅ OPDS server with authentication
- ✅ Full-text search capabilities
- ✅ WebSocket real-time updates
- ✅ Email notifications
- ✅ Docker containerization

### Planned Features 🚧
- 📚 **Enhanced Book Scanner** - Automated library scanning and metadata extraction
- 🤖 **Telegram Bot** - Access library and manage collections via Telegram
- 🌐 **Enhanced OPDS** - OPDS 2.0 support with advanced features
- 🔍 **AI-Powered Recommendations** - Smart book suggestions

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request. For major changes, please open an issue first to discuss what you would like to change.

## License

This project is licensed under the terms specified in the LICENSE file.

