# Agenda de Nexora

La Agenda centraliza horarios, citas y conectores. Toda fila pertenece a una
organización y toda consulta usa `organization_id`. La pantalla **Citas** permite
operarla sin editar variables de entorno ni ejecutar SQL.

## Configuración desde la aplicación

En **Citas → Configuración** un owner o admin define:

- uno o más días disponibles con hora inicial y final;
- duración, buffer entre reuniones y aviso mínimo;
- cantidad de días que pueden ofrecerse y zona horaria IANA;
- el conector que creará la reunión: enlace fijo, Google Calendar o Zoom.

Los secretos de Google y Zoom se cifran con AES-256-GCM. La API solo devuelve
si el conector está conectado; nunca devuelve el secreto ni el refresh token.
Cada conector se puede guardar, probar y desconectar desde la misma pantalla.

## Reservas

`GET /api/bookings` lista el período solicitado. `POST /api/bookings` crea una
cita o un bloqueo manual y `PATCH /api/bookings/{id}` cambia fecha, duración,
notas o estado. Estados: `agendada`, `realizada`, `no_show` y `cancelada`.

El motor calcula horas en la zona configurada y guarda UTC. Antes de insertar,
bloquea la configuración de la organización y vuelve a comprobar solapamiento.
Una cita real y una de Laboratorio usan agendas separadas.

## Herramientas del cerebro

Con la herramienta Agenda encendida en **Agente**:

1. `GET /api/bot/availability?conversationId=...` ofrece huecos e IDs efímeros.
2. `POST /api/bot/bookings` acepta `{conversationId, slotId, notes}`.
3. `PATCH /api/bot/bookings` acepta `{conversationId, bookingId, slotId}`.
4. `/api/bot/context` devuelve las próximas citas en `appointments`.

El cerebro no puede inventar una hora: solo puede reservar o reprogramar un ID
que Nexora haya ofrecido a esa conversación durante las últimas 24 horas.

## Conectores

- **Enlace fijo:** copia el mismo enlace a cada cita real.
- **Google Calendar:** renueva OAuth con el refresh token, crea el evento y una
  conferencia Google Meet, y actualiza o elimina el evento al cambiar la cita.
- **Zoom:** usa OAuth Server-to-Server, crea una reunión con sala de espera, y
  actualiza o elimina la reunión al cambiar la cita.

Si un proveedor falla, la reserva local se conserva con `link_pending=true`:
no se pierde la cita y la pantalla deja visible que falta el enlace. Una cita
`is_test` nunca llama a esos conectores y nunca expone un enlace real.

## Pruebas

- `internal/agenda/disponibilidad_test.go`: horario, aviso, buffer y solapes.
- `internal/httpapi/agenda_conectores_test.go`: contratos HTTP Google/Zoom.
- `internal/httpapi/agenda_integration_test.go`: flujo cerebro → oferta → reserva
  → contexto y aislamiento real/Laboratorio contra MySQL 8.4.
