# Handoff entre el agente y una persona

Una conversación tiene un único responsable efectivo:

- `ai_enabled = 1` y `handoff_at IS NULL`: responde el agente conectado.
- `ai_enabled = 0` y `handoff_at` informado: responde una persona.

El cambio se ejecuta mediante `internal/handoff`, la única puerta para este
invariante. La operación bloquea la conversación y, en la misma transacción,
descarta cualquier `dispatch` pendiente o en vuelo. Si un POST al cerebro ya
estaba fuera del proceso, su respuesta será rechazada por `/api/bot/messages`
con `409 ai_paused`.

## Formas de pasar a una persona

- El cerebro llama `POST /api/bot/handoff`.
- El worker agota los tres intentos de entrega.
- Un usuario pulsa **Tomar control** en la bandeja.
- Un usuario envía un mensaje manual; tomar control ocurre antes de contactar
  a Meta para evitar una respuesta simultánea del agente.

La primera fecha y causa se conservan si el handoff se solicita nuevamente.
Los motivos admitidos por el modelo de datos son `cliente`, `modelo`, `error`,
`ventana`, `hostilidad` y `manual_reply`.

## Devolver al agente

La bandeja llama `PATCH /api/conversations/{id}/agent` con
`{"habilitado":true}`. La API exige:

1. cerebro configurado y saludable;
2. perfil global del agente encendido;
3. contacto sin pausa individual.

Solo entonces limpia `handoff_at` y `handoff_reason`. Los cambios publican
`conversation.updated` por SSE, por lo que todas las bandejas abiertas se
actualizan sin recargar la página.
