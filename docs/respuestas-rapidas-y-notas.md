# Respuestas rápidas y notas internas (C1)

Las respuestas rápidas se administran en **Ajustes → Respuestas rápidas**. El
atajo es único por organización, no contiene espacios y se usa escribiendo `/`
en el compositor de la bandeja.

Las notas se guardan en `conversation_note`, separadas de `message`. Por diseño
no pasan por el cliente Meta, el outbox de despacho ni `/api/bot/*`. Cada
consulta exige `organization_id`; conservan autor y fecha y se actualizan por
SSE mediante `note.created`.

Rutas autenticadas:

- `GET|POST /api/quick-replies`
- `PATCH|DELETE /api/quick-replies/{id}`
- `GET|POST /api/conversations/{id}/notes`

