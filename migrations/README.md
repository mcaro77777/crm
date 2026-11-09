# Migraciones

La base inicial `crm` ya fue creada externamente. La API verifica al arrancar
que existan las 36 tablas del contrato y no ejecuta DDL silenciosamente. Las
futuras modificaciones se agregarán aquí como SQL versionado y aditivo, con su
comando explícito de aplicación; nunca se editará una migración ya aplicada.
