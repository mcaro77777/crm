# Cerebro integrado en la API Go

## Decisión

El agente se ejecuta dentro del proceso `crm-api`. Ya no requiere una URL de despacho, una URL de salud ni una API key compartida con un servicio externo. Usa la credencial activa configurada en **Proveedor de IA**, que permanece cifrada en MySQL.

## Flujo

1. El webhook se confirma y persiste antes de procesarlo.
2. El mensaje entrante abre o amplía un único despacho vivo por conversación.
3. Un pool acotado de workers reclama trabajos con `FOR UPDATE SKIP LOCKED`.
4. El cerebro carga perfil, conocimiento, ficha, etapa y credencial dentro de la organización.
5. El proveedor devuelve una acción estructurada: responder o transferir a una persona.
6. La respuesta pasa por las mismas guardas de canal, ventana, pausa y handoff que un envío normal.
7. Los fallos transitorios se reintentan; los definitivos terminan en handoff humano.

## Concurrencia y seguridad

- `BRAIN_WORKERS` define el paralelismo y vale `8` por defecto.
- MySQL garantiza exclusión por despacho y la restricción de un único despacho vivo evita respuestas simultáneas en la misma conversación.
- El lease de tres minutos permite recuperar trabajos después de una caída sin liberar prematuramente una llamada al modelo.
- El cliente HTTP reutiliza conexiones y limita tiempos, redirecciones y tamaño de respuesta.
- El token nunca se incorpora al prompt, a eventos, respuestas ni mensajes de error.
- El contenido del cliente se envía como entrada no confiable, separado de las instrucciones del sistema.

## Operación

Para habilitar el agente se requiere:

- proveedor de IA activo con URL base, modelo y token válidos;
- perfil de agente encendido;
- conversación con IA habilitada y contacto no pausado.

No se deben volver a configurar `brain_dispatch_url`, `brain_health_url` ni `brain_api_key`. Las rutas antiguas permanecen solo como compatibilidad de lectura y rechazan nuevas escrituras.
