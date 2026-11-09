# API pública del cerebro

Esta API permite que un cerebro externo lea el contexto comercial y responda
sin recibir el token de WhatsApp ni las credenciales del proveedor de IA.

## Autenticación

Todas las rutas requieren la clave configurada como `brain_api_key` para la
organización. Se acepta uno de estos encabezados:

```http
X-API-Key: <clave>
Authorization: Bearer <clave>
```

La clave permanece cifrada con AES-256-GCM. La comparación se realiza sobre
huellas SHA-256 en tiempo constante. El límite es de 1.200 solicitudes válidas
por minuto y 30 autenticaciones fallidas por minuto y dirección remota.

## Rutas implementadas

### `GET /api/bot/profile`

Entrega el perfil del agente, sus herramientas, la base de conocimiento
renderizada y la ruta/modelo del proveedor LLM. Nunca entrega secretos.

### `GET /api/bot/context`

Acepta `conversationId`, `identity` o `waIdentity`. Devuelve el contacto, su
ficha, la conversación, el estado de la ventana y el lead con sus próximas
etapas permitidas. Toda búsqueda queda limitada a la organización de la clave.

### `POST /api/bot/messages`

```json
{
  "conversationId": "cv_...",
  "text": "Respuesta para el cliente"
}
```

Reutiliza la única puerta de envío. Respeta pausa, handoff y ventana de
WhatsApp. En conversaciones de Laboratorio persiste el mensaje como generado
por IA, pero no llama a Meta.

### `POST /api/bot/llm/{ruta}`

Reenvía sin reinterpretar el cuerpo al proveedor configurado para la
organización y conserva el código y cuerpo de respuesta. Inyecta la credencial
del proveedor solo en el salto servidor a servidor. Un `401` o `402` pausa la
credencial para impedir reintentos dañinos.

## Errores relevantes

- `401 unauthorized`: falta la clave o no coincide.
- `402 sin_credencial`: no existe un proveedor LLM activo.
- `409 ai_paused`: la conversación está en manos de una persona.
- `409 window_closed`: terminó la ventana de respuesta libre.
- `409 sandbox_violation`: se intentó usar una salida real desde una prueba.
- `429 rate_limited`: se superó el límite aplicable.
- `504 proveedor_sin_respuesta`: el proveedor LLM no respondió a tiempo.
