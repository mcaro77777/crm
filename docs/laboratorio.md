# Laboratorio de Nexora

El Laboratorio prueba el cerebro integrado con clientes simulados antes de que
atienda conversaciones reales. Se configura y ejecuta desde **Laboratorio** en
la aplicación; no requiere variables de entorno adicionales.

## Requisitos

- Proveedor de IA configurado y agente encendido.
- Credencial de IA activa en Ajustes → IA para el juez.
- Las tablas base `agent_test_run` y `agent_test_case` del esquema CRM.

La API crea de forma idempotente `lab_scenario` y `lab_case_snapshot` al usar el
módulo. La segunda conserva la fotografía exacta del escenario usado en cada
corrida, aunque luego se edite su configuración.

## Aislamiento

Cada caso crea un contacto sintético archivado y una conversación `is_test=1`.
El mensaje entrante se entrega al cerebro integrado mediante el mismo outbox
durable usado en producción. La respuesta se guarda en MySQL y omite
completamente la llamada a Meta. Estas conversaciones no aparecen en Contactos,
Pipeline ni Bandeja.

La interfaz consulta el estado cada dos segundos e informa si el turno está en
cola, si el cerebro está respondiendo o si el juez está evaluando. Después de
dos minutos ofrece recuperar una corrida que haya quedado abandonada por un
reinicio del servidor.

## Evaluación

Al terminar los turnos, el juez usa la credencial IA cifrada de la organización
y solicita una salida JSON estructurada. Persiste puntuación de 0 a 100,
veredicto, resumen, fortalezas, problemas y recomendaciones. Errores temporales
se reintentan una vez; 401/402 pausa la credencial igual que el proxy del modelo.

## API interna de la aplicación

- `GET|POST /api/lab/scenarios`
- `PATCH|DELETE /api/lab/scenarios/{id}`
- `GET|POST /api/lab/runs`
- `GET /api/lab/runs/{id}`
- `POST /api/lab/runs/{id}/evaluate`
- `GET /api/lab/compare?anterior={id}&actual={id}`

Todas las rutas requieren sesión y limitan sus lecturas y escrituras por
`organization_id`. Solo puede existir una corrida activa por organización.
