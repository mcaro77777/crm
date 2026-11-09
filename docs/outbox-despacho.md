# Outbox de despacho

El worker de despacho se inicia junto con la API y barre MySQL cada dos
segundos. La tabla `dispatch`, y no la memoria del proceso, es la fuente de
verdad.

Garantías implementadas:

- Un único despacho vivo por conversación, protegido por el índice
  `dispatch_conversation_vivo_uq`.
- Agrupación de mensajes durante seis segundos.
- Reclamo mediante `SELECT ... FOR UPDATE SKIP LOCKED`.
- Lease de tres minutos y recuperación automática de leases vencidos.
- Tres intentos, con esperas de 1 y 4 segundos.
- `429` y `5xx` se reintentan; otros errores HTTP caducan inmediatamente.
- Al caducar, la conversación pasa a una persona con motivo `error`.
- Un mensaje anotado mientras el POST está en vuelo abre un despacho nuevo y
  no se pierde al terminar el anterior.
- Una configuración eliminada, una conversación inexistente o una IA pausada
  descartan el trabajo sin atribuir un fallo al cerebro.

La prueba `TestIntegracionOutboxMySQL` se activa con `TEST_DATABASE_DSN` y
comprueba las consultas específicas de MySQL sobre un esquema real.

El siguiente paso es llamar a `Repositorio.Encolar` después de confirmar el
mensaje entrante en la base de datos.
