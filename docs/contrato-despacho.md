# Contrato de despacho Nexora → cerebro

Nexora enviará cada ráfaga a la URL configurada en Ajustes → Inteligencia.
El cerebro debe responder `2xx` rápidamente y contestar al cliente mediante
`/api/bot/messages` cuando termine de pensar.

Cabeceras:

- `Content-Type: application/json`
- `X-Vocero-Signature: sha256=<HMAC-SHA256 del cuerpo crudo>`
- `X-Vocero-Dispatch-Id: dsp_...`
- `X-Vocero-Organization: <slug o id>`

Reglas:

- La firma se valida sobre el cuerpo crudo, sin volver a serializarlo.
- `dispatchId` es la clave de idempotencia y no cambia en los reintentos.
- `messages` nunca está vacío y contiene como máximo la ráfaga pendiente.
- El evento no contiene tokens de WhatsApp ni del proveedor de IA.
- Un `2xx` confirma recepción; `429` y `5xx` se reintentan; los demás códigos
  no se reintentan y provocarán traspaso a una persona.

La forma canónica y su orden están implementados únicamente en
`internal/despacho/contrato.go` y fijados byte a byte por
`internal/despacho/contrato_test.go`.
