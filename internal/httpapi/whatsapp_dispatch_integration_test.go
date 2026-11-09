package httpapi

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"crm-api/internal/basedatos"
	"crm-api/internal/despacho"
	"crm-api/internal/eventos"
	_ "github.com/go-sql-driver/mysql"
)

func TestIntegracionWebhookGuardaAntesDeEncolar(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("TEST_DATABASE_DSN no configurada")
	}
	db, err := sql.Open("mysql", dsn+"?parseTime=true&loc=UTC")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err := basedatos.AsegurarExtensiones(ctx, db); err != nil {
		t.Fatal(err)
	}
	org := "org_test_webhook_dispatch"
	_, _ = db.ExecContext(ctx, "DELETE FROM organization WHERE id=?", org)
	defer db.ExecContext(ctx, "DELETE FROM organization WHERE id=?", org)
	ahora := time.Now().UTC().Truncate(time.Millisecond)
	if _, err = db.ExecContext(ctx, "INSERT INTO organization(id,name,created_at) VALUES (?,'Prueba webhook',?)", org, ahora); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, "INSERT INTO pipeline_stage(id,organization_id,name,position,kind,created_at) VALUES ('st_test_webhook',?,'Nuevo',0,'open',?)", org, ahora); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, "INSERT INTO agent_profile(id,organization_id,enabled,name,created_at,updated_at) VALUES ('ag_test_webhook',?,1,'Agente',?,?)", org, ahora, ahora); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, "INSERT INTO app_setting(organization_id,clave,valor,updated_at) VALUES (?,'brain_dispatch_url','http://cerebro/dispatch',?)", org, ahora); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, "INSERT INTO app_setting(organization_id,clave,secret_cipher,secret_iv,secret_tag,updated_at) VALUES (?,'brain_api_key','cifrado','iv','tag',?)", org, ahora); err != nil {
		t.Fatal(err)
	}

	servidor := &Servidor{db: db, logger: slog.New(slog.NewTextHandler(io.Discard, nil)), bus: eventos.NuevoBus(), despachos: despacho.NuevoRepositorio(db)}
	mensaje := mensajeMeta{From: "56911111111", ID: "wamid.integracion.1", Timestamp: "1794225600", Type: "text"}
	mensaje.Text.Body = "Hola desde WhatsApp"
	if err := servidor.guardarMensajeEntrante(ctx, org, "Cliente prueba", mensaje); err != nil {
		t.Fatal(err)
	}

	var conversacionID, mensajeID string
	var noLeidos int
	err = db.QueryRowContext(ctx, `SELECT cv.id,m.id,cv.unread_count FROM message m JOIN conversation cv ON cv.id=m.conversation_id WHERE m.organization_id=? AND m.wa_message_id=?`, org, mensaje.ID).Scan(&conversacionID, &mensajeID, &noLeidos)
	if err != nil {
		t.Fatalf("el mensaje no quedo guardado: %v", err)
	}
	if noLeidos != 1 {
		t.Fatalf("no leidos=%d; se esperaba 1", noLeidos)
	}
	var despachoID string
	var contiene bool
	err = db.QueryRowContext(ctx, `SELECT id,JSON_CONTAINS(message_ids,JSON_QUOTE(?)) FROM dispatch WHERE organization_id=? AND conversation_id=? AND status='pendiente'`, mensajeID, org, conversacionID).Scan(&despachoID, &contiene)
	if err != nil || !contiene {
		t.Fatalf("no se encolo el mensaje confirmado: id=%s contiene=%v err=%v", despachoID, contiene, err)
	}

	// Repetir el mismo wa_message_id no toca no leídos ni duplica el despacho.
	if err := servidor.guardarMensajeEntrante(ctx, org, "Cliente prueba", mensaje); err != nil {
		t.Fatal(err)
	}
	var mensajes, despachos, noLeidosDespues int
	_ = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM message WHERE organization_id=? AND wa_message_id=?", org, mensaje.ID).Scan(&mensajes)
	_ = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM dispatch WHERE organization_id=? AND conversation_id=?", org, conversacionID).Scan(&despachos)
	_ = db.QueryRowContext(ctx, "SELECT unread_count FROM conversation WHERE organization_id=? AND id=?", org, conversacionID).Scan(&noLeidosDespues)
	if mensajes != 1 || despachos != 1 || noLeidosDespues != 1 {
		t.Fatalf("duplicado altero estado: mensajes=%d despachos=%d noLeidos=%d", mensajes, despachos, noLeidosDespues)
	}

	// Con el agente apagado, el mensaje queda en bandeja pero no se anota.
	_, _ = db.ExecContext(ctx, "UPDATE agent_profile SET enabled=0 WHERE organization_id=?", org)
	mensaje.ID = "wamid.integracion.2"
	mensaje.Text.Body = "Segundo mensaje"
	if err := servidor.guardarMensajeEntrante(ctx, org, "Cliente prueba", mensaje); err != nil {
		t.Fatal(err)
	}
	var anotado bool
	_ = db.QueryRowContext(ctx, `SELECT JSON_CONTAINS(message_ids,JSON_QUOTE((SELECT id FROM message WHERE organization_id=? AND wa_message_id=?))) FROM dispatch WHERE id=?`, org, mensaje.ID, despachoID).Scan(&anotado)
	if anotado {
		t.Fatal("se despacho un mensaje con el agente apagado")
	}
}
