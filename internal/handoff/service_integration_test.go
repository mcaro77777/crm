package handoff

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

func TestIntegracionHandoffMySQL(t *testing.T) {
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
	ahora := time.Now().UTC().Truncate(time.Millisecond)
	org, orgAjena := "org_test_handoff", "org_test_handoff_ajena"
	_, _ = db.ExecContext(ctx, "DELETE FROM organization WHERE id IN (?,?)", org, orgAjena)
	defer db.ExecContext(ctx, "DELETE FROM organization WHERE id IN (?,?)", org, orgAjena)
	sentencias := []struct {
		q string
		a []any
	}{
		{"INSERT INTO organization(id,name,slug,created_at) VALUES (?,'Handoff','handoff-test',?)", []any{org, ahora}},
		{"INSERT INTO organization(id,name,slug,created_at) VALUES (?,'Ajena','handoff-test-ajena',?)", []any{orgAjena, ahora}},
		{"INSERT INTO contact(id,organization_id,channel,wa_identity,name,created_at,updated_at) VALUES ('ct_handoff',?,'whatsapp','56910000000','Cliente',?,?)", []any{org, ahora, ahora}},
		{"INSERT INTO conversation(id,organization_id,contact_id,channel,ai_enabled,created_at,updated_at) VALUES ('cv_handoff',?,'ct_handoff','whatsapp',1,?,?)", []any{org, ahora, ahora}},
		{"INSERT INTO agent_profile(id,organization_id,enabled,name,role,created_at,updated_at) VALUES ('ag_handoff',?,1,'Nexa','vendedor',?,?)", []any{org, ahora, ahora}},
		{"INSERT INTO dispatch(id,organization_id,conversation_id,status,not_before,first_message_at,last_message_at,message_ids,created_at,updated_at) VALUES ('dsp_handoff',?,'cv_handoff','pendiente',?,?,?,JSON_ARRAY('msg_x'),?,?)", []any{org, ahora, ahora, ahora, ahora, ahora}},
	}
	for _, sentencia := range sentencias {
		if _, err := db.ExecContext(ctx, sentencia.q, sentencia.a...); err != nil {
			t.Fatalf("preparar handoff: %v\n%s", err, sentencia.q)
		}
	}

	servicio := Nuevo(db)
	estado, cambio, err := servicio.Tomar(ctx, org, "cv_handoff", "hostilidad", ahora.Add(time.Second))
	if err != nil || !cambio || estado.IAHabilitada || estado.Motivo == nil || *estado.Motivo != "hostilidad" {
		t.Fatalf("handoff incorrecto: estado=%+v cambio=%v error=%v", estado, cambio, err)
	}
	var ia bool
	var motivo, despacho string
	if err := db.QueryRowContext(ctx, "SELECT ai_enabled,handoff_reason FROM conversation WHERE id='cv_handoff'").Scan(&ia, &motivo); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, "SELECT status FROM dispatch WHERE id='dsp_handoff'").Scan(&despacho); err != nil {
		t.Fatal(err)
	}
	if ia || motivo != "hostilidad" || despacho != "descartado" {
		t.Fatalf("persistencia incorrecta: ia=%v motivo=%s despacho=%s", ia, motivo, despacho)
	}

	// Repetirlo no cambia la primera causa, que explica por qué tomó control una persona.
	estado, cambio, err = servicio.Tomar(ctx, org, "cv_handoff", "cliente", ahora.Add(2*time.Second))
	if err != nil || cambio || estado.Motivo == nil || *estado.Motivo != "hostilidad" {
		t.Fatalf("handoff no idempotente: estado=%+v cambio=%v error=%v", estado, cambio, err)
	}

	estado, cambio, err = servicio.Reactivar(ctx, org, "cv_handoff", ahora.Add(3*time.Second))
	if err != nil || !cambio || !estado.IAHabilitada || estado.HandoffEn != nil || estado.Motivo != nil {
		t.Fatalf("reactivacion incorrecta: estado=%+v cambio=%v error=%v", estado, cambio, err)
	}
	if _, _, err := servicio.Tomar(ctx, orgAjena, "cv_handoff", "modelo", ahora); !errors.Is(err, ErrConversacionNoEncontrada) {
		t.Fatalf("se cruzo la organizacion: %v", err)
	}
	_, _ = db.ExecContext(ctx, "UPDATE contact SET ai_paused=1 WHERE id='ct_handoff'")
	if _, _, err := servicio.Reactivar(ctx, org, "cv_handoff", ahora.Add(4*time.Second)); !errors.Is(err, ErrContactoPausado) {
		t.Fatalf("se reactivo un contacto pausado: %v", err)
	}
}
