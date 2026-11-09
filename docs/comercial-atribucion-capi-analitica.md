# Atribución, CAPI y analítica (D1–D4)

Los módulos viven en `internal/httpapi/comercial.go` y respetan siempre
`organization_id`.

## Integración de rutas

Registrar con sesión:

- `GET /api/analytics` → `obtenerAnalitica`
- `GET /api/analytics/export.csv` → `exportarAnaliticaCSV`
- `GET /api/settings/capi` → `obtenerCAPI`
- `PUT /api/settings/capi` → `guardarCAPI`

`desde` y `hasta` usan `AAAA-MM-DD`, inclusivo/exclusivo, con máximo 366 días.
El CSV lleva BOM UTF-8 para abrir correctamente en Excel.

## Puntos de dominio

Durante la transacción del mensaje entrante, llamar `guardarAtribucionTx` cuando
exista `referral`. Usa `INSERT IGNORE`: la primera atribución de la conversación
es la autoridad y conserva `ctwa_clid`, anuncio y JSON original.

Al crear una oportunidad atribuible o moverla a ganada/calificada, llamar
`encolarConversionTx`. El índice único evita duplicar el evento. El token CAPI
se cifra con AES-256-GCM y el envío pasa por `internal/meta/capi.go`, nunca por
HTTP ad hoc.

La analítica agrega contactos, oportunidades, estados, montos, mensajes,
respuestas IA, handoffs, etapas y fuentes directamente en MySQL.
