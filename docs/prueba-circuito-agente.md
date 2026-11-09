# Prueba integral del circuito del agente (A7)

`TestIntegracionCircuitoAgenteA7` usa las mismas rutas y servicios que el
contenedor productivo. Solo sustituye por servidores HTTP locales al cerebro y
a Meta Graph. No requiere internet ni entrega secretos a esos cuerpos.

## Qué comprueba

1. Un webhook firmado de WhatsApp se guarda antes de procesarse.
2. El mensaje entrante crea o reutiliza contacto, conversación y outbox.
3. El worker reclama el despacho con lease y firma el JSON exacto con HMAC.
4. El cerebro valida firma y cabeceras y responde mediante
   `POST /api/bot/messages`.
5. La API vuelve a validar organización, agente, handoff y ventana, envía por el
   cliente central de Meta y guarda el saliente como generado por IA.
6. Repetir el webhook no duplica eventos, mensajes, no leídos ni despachos.
7. Dos respuestas 5xx se reintentan con el mismo `dispatchId`; el tercer intento
   entrega correctamente.
8. Un 4xx definitivo no se reintenta y transfiere la conversación a una persona.
9. Un trabajo `en_vuelo` con lease vencido se reclama y termina.
10. API key, token de Meta y App Secret no aparecen en evento ni logs.

## Ejecución reproducible

La base debe contener el esquema de `crm-nuevo/documentacion/vocero_mysql_schema.sql`.
Las pruebas crean organizaciones propias y las eliminan al finalizar.

```powershell
$env:TEST_DATABASE_DSN='root:clave@tcp(host.docker.internal:3306)/crm'
docker run --rm `
  -e "TEST_DATABASE_DSN=$env:TEST_DATABASE_DSN" `
  -v D:/Usuario/Desarrollo/Net/vocero-crm-main/crm-nuevo/crm-api:/src `
  -w /src golang:1.27.3-alpine `
  /usr/local/go/bin/go test -p 1 -count=1 ./...
```

Se usa `-p 1` porque todos los paquetes de integración comparten la misma base
temporal. Sin `TEST_DATABASE_DSN`, las pruebas que requieren MySQL se omiten y
`go test ./...` continúa siendo rápido.

`META_GRAPH_BASE_URL` existe exclusivamente para arneses controlados. Si no se
define, la API usa `https://graph.facebook.com`; no debe cambiarse en producción.

