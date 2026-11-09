# Webhooks durables y recuperación SSE (B1–B3)

## Inbox de webhooks

`POST /api/webhooks/wa/{token}` valida organización y firma, guarda el cuerpo
en `webhook_event` y responde 200. El mensaje de negocio ya no se procesa dentro
de la petición HTTP. Solo un fallo al persistir responde 503 para que Meta vuelva
a entregar.

`internal/webhooks/worker.go` reclama filas mediante `FOR UPDATE SKIP LOCKED`:

- `pendiente` y vencida → `en_proceso`, con lease de tres minutos;
- al terminar → `procesado`;
- al fallar → `pendiente` con esperas de 30 s, 2 min, 10 min, 1 h y 6 h;
- al sexto fallo → `agotado`.

El worker ejecuta una tanda apenas arranca. Por eso recupera automáticamente
pendientes y filas `en_proceso` cuyo lease venció después de una caída. Corre en
el mismo contexto de apagado ordenado que el worker de despachos.

La limpieza se ejecuta al arrancar y cada hora: conserva procesados por siete
días y agotados por treinta días. `/api/health` informa `webhooks.pendientes` y
`webhooks.agotados`; esos contadores son diagnósticos y no bajan `ok`.

## SSE recuperable

El bus conserva los últimos 256 eventos por organización con IDs estrictamente
crecientes. `GET /api/events` acepta `Last-Event-ID` —enviado automáticamente por
`EventSource` al reconectar— o `?since=<id>`.

La suscripción y la lectura del historial ocurren bajo el mismo lock, evitando
el hueco clásico entre recuperar y escuchar. Si el ID solicitado ya salió del
búfer, el servidor emite `sync.required`; Angular vuelve a consultar
conversaciones, mensajes y estado del despacho por HTTP.

El historial es deliberadamente local al proceso. Tras reiniciar, el navegador
hace catch-up desde las APIs HTTP; MySQL sigue siendo la autoridad.

## Verificación

```powershell
$env:TEST_DATABASE_DSN='root:clave@tcp(host.docker.internal:3306)/crm'
go test -p 1 -count=1 ./...
```

La prueba MySQL cubre concurrencia entre dos workers, reintentos, agotamiento,
lease vencido y limpieza. A7 vuelve a ejecutar el circuito completo usando el
nuevo inbox asíncrono.

