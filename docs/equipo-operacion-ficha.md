# Equipo, operación y ficha comercial (C2–C4)

El módulo agrega rutas autenticadas y siempre acotadas por organización para:

- listar integrantes, invitar por correo, aceptar invitaciones y cambiar roles;
- impedir degradar al último `owner`;
- asignar o desasignar una conversación a un usuario miembro;
- importar contactos CSV de forma reejecutable y exportarlos;
- buscar contactos globalmente;
- leer y reemplazar una ficha comercial JSON de hasta cien campos.

## Contratos a registrar

`GET /api/settings/team`, `POST /api/settings/team/invitations`,
`POST /api/invitations/{id}/accept`,
`PATCH /api/settings/team/members/{id}`,
`PATCH /api/conversations/{id}/assignment`, `GET /api/search`,
`POST /api/contacts/import`, `GET /api/contacts/export` y
`GET|PATCH /api/contacts/{id}/ficha`.

El CSV exige `nombre` e `identidad`; admite `canal`, `telefono`, `fuente` y
`notas`. Una identidad ya existente se informa como omitida y no crea otra
oportunidad ni conversación.
