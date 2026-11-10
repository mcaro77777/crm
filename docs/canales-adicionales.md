# Canales adicionales y operación avanzada

Nexora puede atender Instagram, Messenger y TikTok desde la misma bandeja. La
configuración se realiza en **Ajustes → Otros canales**; no es necesario editar
la base de datos ni exponer secretos en el frontend.

## Transportes soportados

| Canal | Meta oficial | Zernio opcional | Ventana del agente | Límite de texto |
|---|---:|---:|---:|---:|
| Instagram | Sí | Sí | 24 h (7 días para una persona) | 1.000 caracteres |
| Messenger | Sí | Sí | 24 h (7 días para una persona) | 2.000 caracteres |
| TikTok | No | Sí | 48 h | 2.000 caracteres |

Cada canal declara además su límite de mensajes consecutivos. El envío valida
la ventana, longitud y límite antes de llamar al proveedor. El cerebro nunca
usa la extensión humana de siete días.

## Configuración y secretos

`GET /api/settings/channels` devuelve el estado, URL del webhook y capacidades.
Los endpoints `PUT`, `POST .../test` y `DELETE` bajo
`/api/settings/channels/{canal}` permiten conectar, probar y desconectar.

El token y el secreto de firma se cifran con AES-256-GCM usando
`ENCRYPTION_KEY`. La API y la interfaz muestran únicamente los últimos cuatro
caracteres del token. Para Zernio se puede cambiar el transporte con
`ZERNIO_BASE_URL`; el valor normal es `https://api.zernio.com/v1`.

## Webhooks e identidad

- Instagram: `/api/webhooks/ig/{verifyToken}`.
- Messenger: `/api/webhooks/messenger/{verifyToken}`.
- TikTok: `/api/webhooks/tiktok/{verifyToken}`.

Meta se valida con `X-Hub-Signature-256` cuando hay App Secret. Zernio se valida
con `X-Zernio-Signature` y el secreto cifrado del canal. Primero se guarda el
evento en `webhook_event`; el worker lo procesa con reintentos. Los mensajes se
deduplican por canal e identificador externo.

Las identidades son estables y no se mezclan: `ig:<id>`, `fb:<id>` y `tt:<id>`.
Un mensaje nuevo crea contacto, lead, conversación y mensaje en una sola
transacción y luego publica SSE y encola el despacho al cerebro.

## Operación de la bandeja

`GET /api/conversations` acepta:

- `responsable=sin_asignar|yo|<user_id>`;
- `canal=whatsapp|instagram|messenger|tiktok`;
- `fuente=organico|anuncio|referido|otro`;
- `desde` y `hasta` en RFC 3339;
- `vista=sin_asignar|por_vencer` o `vencimiento=por_vencer`.

“Por vencer” significa que la ventana del canal expira dentro de las próximas
dos horas. La selección se conserva durante las actualizaciones SSE y los
filtros de fecha no se pierden. La conversación puede asignarse o dejarse sin
responsable desde su panel lateral.

## Importación CSV

El resultado informa total, creados, duplicados, omitidos e inválidos. Incluye
hasta cien detalles con número de línea, motivo e identidad normalizada; si hay
más, `detallesTruncados` indica cuántos no se mostraron. Repetir un archivo no
crea contactos duplicados ni conversaciones.
