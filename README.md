# CRM API en Go

API del nuevo CRM. Se conecta exclusivamente a la base MySQL `crm`, sirve el
Angular compilado desde `ClientApp` y está preparada para desplegarse como una
aplicación Docker en Easypanel.

## Qué incluye

- autenticación con sesiones persistidas y cookie segura;
- primer registro: dueño, organización, pipeline y perfil del agente;
- contactos y creación atómica de su lead;
- pipeline con bitácora de movimientos;
- conversaciones y mensajes de solo lectura;
- eventos en tiempo real con SSE;
- healthcheck real contra MySQL;
- aislamiento obligatorio por `organization_id`;
- fallback del router Angular sin ocultar 404 de `/api/*`;
- imagen Docker multi-stage y ejecución sin privilegios.

## Requisitos

- Go 1.27.3 para desarrollo local, o Docker;
- MySQL 8 con la base `crm` y las 36 tablas del esquema;
- Angular compilado solo si se quiere servir la interfaz.

## Configuración local

Copie `.env.example` a `.env` y configure las variables en su terminal. La API
no carga archivos `.env` por sí sola para evitar diferencias con Easypanel.

El DSN debe usar el formato del driver de Go:

```text
usuario:clave@tcp(host:3306)/crm?parseTime=true&loc=UTC&charset=utf8mb4
```

Si la contraseña contiene caracteres especiales, genere el DSN con cuidado o
use una contraseña compatible. Nunca lo escriba en logs ni lo suba al repo.

## Comandos

```bash
go test ./...
go vet ./...
go run ./cmd/api check-db
go run ./cmd/api serve
```

La API queda en `http://localhost:8080` por defecto.

## Angular

Compile el frontend y copie el contenido de `dist/<aplicacion>/browser`:

```powershell
.\scripts\publicar-angular.ps1 -Origen "D:\ruta\dist\crm\browser"
```

En producción el frontend debe usar una URL relativa:

```typescript
apiUrl: '/api'
```

## Docker

```bash
docker build -t crm-api:local .
docker run --rm -p 8080:8080 --env-file .env crm-api:local
```

La imagen escucha en `8080`, sirve `ClientApp` y usa `/data/media` para medios.
Monte un volumen persistente cuando se habilite la gestión de adjuntos.

## Easypanel

1. Cree una aplicación desde este directorio o repositorio.
2. Seleccione el `Dockerfile` incluido.
3. Configure el dominio hacia el puerto interno `8080`.
4. Copie las variables de `.env.example` al panel.
5. Use la conexión interna de MySQL; no exponga el puerto 3306.
6. Configure `APP_ENV=production` y la URL HTTPS real en `APP_BASE_URL`.
7. Genere `AUTH_SECRET` y `ENCRYPTION_KEY`; no reutilice claves de desarrollo.
8. Verifique `/api/health` después del despliegue.

## Rutas implementadas

```text
GET   /api/health
GET   /api/version
POST  /api/auth/register
POST  /api/auth/login
POST  /api/auth/logout
GET   /api/auth/session
GET   /api/contacts
POST  /api/contacts
GET   /api/contacts/{id}
PATCH /api/contacts/{id}
GET   /api/pipeline
PATCH /api/leads/{id}/stage
GET   /api/conversations
GET   /api/conversations/{id}
GET   /api/conversations/{id}/messages
GET   /api/events
```

Las rutas de WhatsApp, `/api/bot/*`, envío de mensajes, medios, agenda y
analítica se implementarán como módulos adicionales sobre esta base. No hay
handlers ficticios que respondan éxito sin ejecutar la operación.

## Seguridad relevante

- El token de sesión enviado al navegador no se guarda en claro: MySQL conserva
  solamente su SHA-256.
- Las contraseñas usan PBKDF2-HMAC-SHA256 con sal aleatoria y 600.000 rondas.
- Los bodies JSON tienen un límite de 1 MiB y rechazan campos desconocidos.
- Las operaciones mutables rechazan orígenes diferentes a `APP_BASE_URL`.
- Las consultas de dominio incluyen siempre la organización de la sesión.
- La API nunca devuelve tokens, hashes ni credenciales cifradas.
