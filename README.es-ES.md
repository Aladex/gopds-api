

<p align="center">
  <img src="https://raw.githubusercontent.com/Aladex/gopds-api/master/logo/logo.png" width="350" alt="GoPDS">
</p>

# GoPDS

GoPDS es una biblioteca de libros electrónicos autoalojada con una API en Go, una interfaz en React y feeds OPDS para lectores electrónicos. Gestiona libros FB2 almacenados en archivos ZIP y los convierte a EPUB o MOBI bajo demanda.

## Características

- Navegación del catálogo y búsqueda por libro, autor, serie, género e idioma
- Favoritos personales y colecciones curadas administradas por el administrador
- Registro por invitación, activación por correo electrónico, recuperación de contraseña y sesiones en Redis
- Feeds OPDS estilo 1.x autenticados con búsqueda y OpenSearch
- Escaneo de ZIP/FB2, extracción de portadas y detección de duplicados e idioma
- Progreso de escaneo y conversión mediante WebSocket
- Conversión de FB2 a EPUB 3 en el mismo proceso con compatibilidad NCX para EPUB 2
- Conversión a MOBI mediante el ejecutable KindleGen incluido
- Administración de usuarios, invitaciones, géneros, colecciones, portadas y escaneo
- Bots de Telegram por usuario con búsqueda, favoritos, colecciones y descargas
- Búsqueda en Telegram asistida por OpenAI (opcional) y detección del idioma del libro
- Interfaz adaptable en inglés/ruso con temas claro y oscuro

La búsqueda utiliza coincidencia de subcadenas de PostgreSQL y similitud `pg_trgm`, no la búsqueda de texto completo de PostgreSQL.

## Stack

- Go 1.26, Gin, go-pg, PostgreSQL 15, Redis
- React 19, TypeScript, Vite 8, Tailwind CSS 4, Radix UI
- JWT, protección CSRF, WebSocket, Swagger UI
- Docker y Docker Compose

## Inicio rápido

Requisitos: Docker Engine y el plugin Compose.

Copia la configuración de ejemplo:

```bash
cp config.yaml.example config.yaml
```

Para el archivo Compose proporcionado, actualiza al menos estos campos en `config.yaml`:

```yaml
project_domain: "127.0.0.1"
project_url: "http://localhost:8085"
secret_key: "replace-with-a-random-secret"

server:
  host: "0.0.0.0"
  port: 8085

postgres:
  dbuser: "gopds"
  dbpass: "gopds_password"
  dbname: "gopds"
  dbhost: "postgres:5432"

redis:
  host: "redis"
  port: 6379

sessions:
  key: "replace-with-a-random-session-key"
  refresh: "replace-with-a-random-refresh-key"

app:
  devel_mode: true
  cdn: "http://localhost:8085"
  files_path: "/gopds/books"
  users_path: "/gopds/users"
  book_cdn_key: "replace-with-a-random-book-key"
  posters_path: "/gopds/covers"
  file_book_cdn: "http://localhost:8085"
  mobi_conversion_dir: "/gopds/mobi"
```

Conserva las secciones de ejemplo restantes si necesitas configuraciones de escaneo, SMTP o donaciones. El archivo montado es la fuente de autoridad para la configuración actual de Compose.

Inicia las dependencias primero para evitar una condición de carrera al iniciar la aplicación y la base de datos:

```bash
docker compose up -d --wait postgres redis
docker compose up -d --build gopds-api
curl http://127.0.0.1:8085/api/status
```

Abre <http://127.0.0.1:8085>. Detén el stack con:

```bash
docker compose down
```

PostgreSQL, Redis, los libros y las portadas usan volúmenes con nombre. `/gopds/users` y `/gopds/mobi` son de escritura, pero no se persisten con el archivo Compose actual. No utilices los secretos de ejemplo ni la contraseña de la base de datos en un despliegue público.

## Configuración

GoPDS lee `config.yaml` desde su directorio de trabajo. La configuración también se puede proporcionar mediante variables de entorno usando el prefijo `GOPDS_` y guiones bajos para claves anidadas:

```bash
export GOPDS_POSTGRES_DBHOST=127.0.0.1:5432
export GOPDS_POSTGRES_DBUSER=gopds
export GOPDS_POSTGRES_DBPASS=gopds_password
export GOPDS_POSTGRES_DBNAME=gopds
export GOPDS_REDIS_HOST=127.0.0.1
export GOPDS_SESSIONS_KEY=replace-me
export GOPDS_SESSIONS_REFRESH=replace-me
export GOPDS_SECRET_KEY=replace-me
```

Consulta [`config.yaml.example`](config.yaml.example) para ver la estructura de la configuración.

- SMTP es obligatorio para los correos de activación y recuperación de contraseña.
- `OPENAI_API_KEY` habilita las funciones de OpenAI; `OPENAI_MODEL` selecciona el modelo y por defecto se establece en `gpt-4o-mini`.
- Los webhooks de Telegram requieren una URL base HTTPS accesible públicamente.
- `app.allowed_origins` añade orígenes de navegador aceptados por CORS y las comprobaciones de origen de WebSocket.

## Desarrollo

Requisitos: Go 1.26.5, Node.js 24, Yarn Classic, PostgreSQL y Redis.

Prepara los archivos generados de Swagger y el marcador de posición del frontend integrado, luego ejecuta el backend:

```bash
make bootstrap
make dev
```

Ejecuta el frontend de Vite en otra terminal:

```bash
cd booksdump-frontend
yarn install --frozen-lockfile
VITE_API_URL=http://127.0.0.1:8085 yarn start
```

Abre <http://127.0.0.1:3000> para coincidir con el origen CORS predeterminado del backend. `VITE_API_URL` configura las solicitudes HTTP, pero las conexiones WebSocket usan el origen de la página; por lo tanto, el progreso en tiempo real necesita una compilación del mismo origen o un proxy de desarrollo.

Compila el frontend, el paquete de Swagger y `bin/gopds`:

```bash
make build
```

## Pruebas y calidad

```bash
make test-backend       # Suite corta de Go; no requiere base de datos
make test-frontend      # Suite de Vitest
make verify             # Compilación de frontend, compilación de Go, pruebas y cobertura
make test-integration   # Suite completa de Go contra PostgreSQL
make lint-new           # Lint de Go para cambios relativos a la base de lint
make lint-frontend-new  # Errores de ESLint en archivos de frontend modificados
make fmt-frontend-check # Verificación de Prettier
make security           # gosec
```

`make lint` y `make lint-frontend` inspeccionan todo el repositorio y pueden reportar un acumulado de errores preexistentes. La CI utiliza los objetivos de lint acotados a los cambios.

Las utilidades opcionales para conjuntos de datos de desarrollo son:

```bash
make db-dump  # Lee el catálogo de producción; requiere acceso esperado a kubectl
make db-reset # Reemplaza la base de datos local de Compose y crea usuarios sintéticos
```

Inspecciona sus scripts y la configuración del objetivo antes de ejecutar cualquiera de los comandos.

## Migraciones de base de datos

Los archivos en `database_migrations/` se ejecutan en orden de nombre de archivo. Los volúmenes de base de datos de Compose recién creados los aplican a través de la inicialización de PostgreSQL. Para una base de datos existente:

```bash
make migrate-plan # Vista previa de archivos pendientes
make migrate-up   # Aplica archivos pendientes
```

El ejecutor registra los archivos aplicados en `schema_migrations` y ejecuta cada nuevo archivo en su propia transacción. Una base de datos creada antes de que exista el registro se establece como línea base en la primera ejecución en lugar de reproducir su esquema. Las migraciones son solo hacia adelante; para revertir un cambio, añade y prueba una nueva migración.

## API y OPDS

- Estado: <http://127.0.0.1:8085/api/status>
- Swagger UI: <http://127.0.0.1:8085/swagger/index.html>
- Raíz OPDS: <http://127.0.0.1:8085/opds/>
- OpenSearch: <http://127.0.0.1:8085/opds-opensearch.xml>

OPDS utiliza autenticación básica HTTP. Swagger cubre solo los controladores REST anotados; el registro de rutas bajo `cmd/gopds/` y los paquetes individuales es la fuente de autoridad completa.

## Estructura del repositorio

```text
cmd/gopds/              Punto de entrada de la aplicación y rutas
cmd/migrate/            Comando de migración de base de datos
api/                    Controladores REST y WebSocket
opds/                   Feeds OPDS
services/               Servicios de la aplicación
database/               Acceso a la base de datos
database_migrations/    Migraciones SQL ordenadas
internal/converter/     Convertidor FB2 a EPUB
internal/swaggerdocs/   Paquete generado de Swagger
telegram/               Integración con Telegram
booksdump-frontend/     Aplicación React
scripts/                Utilidades de base de datos para desarrollo
```

## Licencia

[MIT](LICENSE)
