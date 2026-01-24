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
- **Multi-format Support** - FB2, EPUB, and MOBI file handling with automatic conversion
- **Advanced Search** - Full-text search with PostgreSQL trigrams and fuzzy matching
- **Author & Series Management** - Comprehensive author and series information with search
- **Language Detection** - Automatic language detection and multi-language book categorization
- **File Download** - Secure book file serving with signed URLs
- **Duplicate Detection** - Identify and manage duplicate books across library

### User Features
- **User Authentication** - Registration, login, logout with JWT tokens
- **Personal Libraries** - User-specific favorites and reading lists
- **Profile Management** - User settings and preferences
- **Password Reset** - Email-based password recovery
- **Invite System** - User registration via admin-generated invites

### OPDS Server
- **OPDS 1.2 Compliant** - Standard e-reader compatibility
- **Authenticated Access** - Secure access to personal libraries
- **Device Synchronization** - Access favorites across multiple devices
- **Search Integration** - Find books directly from e-readers
- **Download Support** - Direct book downloads via OPDS
- **OpenSearch Support** - Search descriptor for OPDS clients

### Advanced Features
- **File Conversion** - FB2 to MOBI and EPUB conversion with automatic cleanup via fb2c
- **WebSocket Support** - Real-time updates for book scanning and conversion progress
- **Book Covers** - Automatic cover extraction and CDN serving
- **Content Approval** - Book moderation system for library quality control
- **Session Management** - Redis-based session store with refresh tokens
- **Email Integration** - Registration confirmation and password reset emails
- **Admin Panel** - Administrative functions and user management

### Book Scanning
- **Automated Scanning** - Scan ZIP archives containing FB2 books
- **Progress Tracking** - Real-time WebSocket updates during scanning
- **Error Handling** - Detailed error logging and recovery
- **Selective Scanning** - Scan specific archives on demand
- **Duplicate Detection** - Skip duplicate books during scanning
- **Book Rescan** - Re-extract metadata from existing books with preview
- **Archive Management** - Track scanned and unscanned archives

### Modern Web Interface
- **Responsive Design** - Mobile-friendly React frontend
- **Material Design** - Clean, modern UI with MUI components
- **Real-time Updates** - WebSocket integration for live status
- **Internationalization** - Multi-language support
- **Advanced Search UI** - Intuitive search and filtering
- **Language Rotor** - Visual language switcher for book browsing

### Telegram Bot
- **Personal Bot Support** - Each user can connect their own Telegram bot via BotFather token
- **Natural Language Search** - AI-powered book search with conversation context
- **Multi-format Downloads** - Direct book downloads in FB2, EPUB, MOBI, or ZIP
- **Advanced Search** - Search by title, author, or combined queries
- **Interactive Navigation** - Pagination through results with inline keyboards
- **Favorites Access** - Manage favorite books directly from Telegram
- **Webhook-based** - Efficient real-time message processing

Users create a bot through [@BotFather](https://t.me/BotFather), configure it in their account settings, and link it with `/start` command. The bot provides commands for searching (`/search`, `/b`, `/a`, `/ba`), managing favorites (`/favorites`), and conversation management (`/context`, `/clear`). Each bot is exclusively linked to one user account with secure Redis-based conversation storage.

## API Documentation

The API includes comprehensive Swagger documentation available at `/swagger/index.html` when running the server. Key endpoints include:

- `/api/books/*` - Book management and search
- `/api/auth/*` - Authentication and registration
- `/api/authors/*` - Author information
- `/api/admin/*` - Admin panel operations
- `/opds/*` - OPDS server endpoints
- `/api/telegram/*` - Telegram bot configuration

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
   - fb2c converter (for MOBI/EPUB conversion)

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
- **File paths** - Book storage, archives, and conversion directories
- **Email settings** - SMTP configuration for notifications
- **Security keys** - JWT and session secrets
- **CDN settings** - File serving configuration
- **Telegram settings** - Base URL for webhook configuration
- **Scanning settings** - Archive directories and duplicate handling

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

### Completed Features
- FB2, EPUB, and MOBI book management
- Automatic format conversion (FB2 to MOBI/EPUB)
- React frontend with Material-UI
- User authentication and session management
- Personal favorites and reading lists
- OPDS 1.2 server with authentication
- Full-text search with PostgreSQL trigrams
- WebSocket real-time updates
- Email notifications (registration, password reset)
- Docker containerization
- Telegram Bot with AI-powered search
- Automated book scanning from ZIP archives
- Book rescan with metadata preview
- Duplicate detection and management
- Language detection
- Admin panel with comprehensive management tools

### Planned Features
- **Book Collections** - Create and manage custom book collections with voting
- **Enhanced OPDS** - OPDS 2.0 support with advanced features
- **Reading Statistics** - Track reading progress and statistics
- **Social Features** - Book reviews and recommendations sharing
- **Enhanced Metadata** - Integration with external book databases (Goodreads, LibraryThing)

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request. For major changes, please open an issue first to discuss what you would like to change.
