# Estado del despacho en la bandeja

La tabla `dispatch` sigue siendo la fuente de verdad. La interfaz consulta
`GET /api/conversations/{id}/dispatch` y recibe solo un estado público:

| Estado público | Estado persistido | Significado |
|---|---|---|
| `esperando` | `pendiente`, intento 0 | Se agrupa la ráfaga antes del envío. |
| `procesando` | `en_vuelo` | El worker entregó el turno al cerebro. |
| `reintentando` | `pendiente`, intento > 0 | El intento falló y existe una próxima fecha. |
| `entregado` | `entregado` | El cerebro confirmó recepción. |
| `atencion_humana` | `caducado_a_humano` o descarte por handoff | La conversación quedó con una persona. |
| `cancelado` | otro `descartado` | El turno ya no era necesario. |

`last_error` nunca se serializa. El servidor lo transforma en textos definidos
por código, evitando filtrar URLs internas, respuestas del proveedor, tokens o
datos del cliente.

El worker publica `dispatch.updated` al reclamar, entregar, reintentar,
caducar o descartar. La ingesta lo publica al encolar. La bandeja aplica el
evento inmediatamente; al abrir o reconectar el `EventSource` vuelve a consultar
el endpoint, por lo que perder un evento no deja un estado incorrecto.

Los despachos vivos tienen prioridad al consultar el último estado. Esto evita
que un despacho entregado anteriormente oculte un nuevo turno pendiente creado
en el mismo milisegundo.
