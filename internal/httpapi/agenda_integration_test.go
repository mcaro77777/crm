package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"crm-api/internal/basedatos"
	"crm-api/internal/configuracion"
	"crm-api/internal/eventos"
	"crm-api/internal/seguridad"
	_ "github.com/go-sql-driver/mysql"
)

func TestIntegracionAgendaCerebroYAislamiento(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("TEST_DATABASE_DSN no configurada")
	}
	separador := "?"
	if strings.Contains(dsn, "?") {
		separador = "&"
	}
	db, err := sql.Open("mysql", dsn+separador+"parseTime=true&loc=UTC")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := basedatos.AsegurarExtensiones(context.Background(), db); err != nil {
		t.Fatal(err)
	}

	const org = "org_test_agenda_e2"
	ctx := context.Background()
	_, _ = db.ExecContext(ctx, "DELETE FROM organization WHERE id=?", org)
	defer db.ExecContext(ctx, "DELETE FROM organization WHERE id=?", org)
	ahora := time.Now().UTC().Truncate(time.Millisecond)
	llave := bytes.Repeat([]byte{4}, 32)
	apiKey := "agenda-bot-key-integracion-0001"
	secreto, _ := seguridad.CifrarSecreto(llave, apiKey)
	horario := `{"lunes":[{"desde":"00:00","hasta":"23:59"}],"martes":[{"desde":"00:00","hasta":"23:59"}],"miercoles":[{"desde":"00:00","hasta":"23:59"}],"jueves":[{"desde":"00:00","hasta":"23:59"}],"viernes":[{"desde":"00:00","hasta":"23:59"}],"sabado":[{"desde":"00:00","hasta":"23:59"}],"domingo":[{"desde":"00:00","hasta":"23:59"}]}`
	sentencias := []struct {
		q string
		a []any
	}{
		{"INSERT INTO organization(id,name,slug,created_at) VALUES (?,'Agenda E2','agenda-e2',?)", []any{org, ahora}},
		{"INSERT INTO contact(id,organization_id,channel,wa_identity,name,created_at,updated_at) VALUES ('ct_agenda_e2',?,'whatsapp','56911112222','Cliente agenda',?,?)", []any{org, ahora, ahora}},
		{"INSERT INTO conversation(id,organization_id,contact_id,is_test,channel,ai_enabled,last_inbound_at,created_at,updated_at) VALUES ('cv_agenda_real',?,'ct_agenda_e2',0,'whatsapp',1,?,?,?),('cv_agenda_test',?,'ct_agenda_e2',1,'whatsapp',1,?,?,?)", []any{org, ahora, ahora, ahora, org, ahora, ahora, ahora}},
		{"INSERT INTO agent_profile(id,organization_id,enabled,name,role,tools,created_at,updated_at) VALUES ('ag_agenda_e2',?,1,'Nexa','calificador',JSON_OBJECT('agenda',true),?,?)", []any{org, ahora, ahora}},
		{"INSERT INTO calendar_settings(id,organization_id,weekly_hours,slot_minutes,buffer_minutes,min_notice_hours,max_days_ahead,timezone,connector,meeting_link,created_at,updated_at) VALUES ('cal_agenda_e2',?,CAST(? AS JSON),30,0,0,2,'UTC','enlace-fijo','https://meet.example/sala',?,?)", []any{org, horario, ahora, ahora}},
		{"INSERT INTO app_setting(organization_id,clave,secret_cipher,secret_iv,secret_tag,updated_at) VALUES (?,'brain_api_key',?,?,?,?)", []any{org, secreto.Cifrado, secreto.IV, secreto.Tag, ahora}},
	}
	for _, sentencia := range sentencias {
		if _, err := db.ExecContext(ctx, sentencia.q, sentencia.a...); err != nil {
			t.Fatalf("preparar agenda: %v\n%s", err, sentencia.q)
		}
	}

	servidor := httptest.NewServer(NuevoServidorConBus(configuracion.Configuracion{URLBase: "http://localhost", LlaveCifrado: llave, DirectorioCliente: t.TempDir()}, db, slog.New(slog.NewTextHandler(io.Discard, nil)), eventos.NuevoBus()))
	defer servidor.Close()
	cliente := servidor.Client()

	primeroReal := reservarPrimerHueco(t, cliente, servidor.URL, apiKey, "cv_agenda_real")
	if primeroReal["prueba"] != false || primeroReal["enlace"] != "https://meet.example/sala" {
		t.Fatalf("reserva real incorrecta: %#v", primeroReal)
	}
	primeroPrueba := reservarPrimerHueco(t, cliente, servidor.URL, apiKey, "cv_agenda_test")
	if primeroPrueba["prueba"] != true || primeroPrueba["enlace"] != nil || primeroPrueba["referenciaExterna"] != nil {
		t.Fatalf("el laboratorio salio del sandbox: %#v", primeroPrueba)
	}
	if primeroPrueba["programadaEn"] != primeroReal["programadaEn"] {
		t.Fatalf("la agenda de prueba fue bloqueada por la real: real=%v test=%v", primeroReal["programadaEn"], primeroPrueba["programadaEn"])
	}

	res := peticionBot(t, cliente, http.MethodGet, servidor.URL+"/api/bot/context?conversationId=cv_agenda_real", apiKey, nil, false)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("contexto=%d %s", res.StatusCode, leer(res))
	}
	var contexto map[string]any
	decodificarRespuesta(t, res, &contexto)
	if len(contexto["appointments"].([]any)) != 1 {
		t.Fatalf("el contexto no expuso la cita: %#v", contexto)
	}
}

func reservarPrimerHueco(t *testing.T, cliente *http.Client, base, apiKey, conversacion string) map[string]any {
	t.Helper()
	res := peticionBot(t, cliente, http.MethodGet, base+"/api/bot/availability?conversationId="+conversacion, apiKey, nil, false)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("disponibilidad %s=%d %s", conversacion, res.StatusCode, leer(res))
	}
	var disponibilidad map[string]any
	decodificarRespuesta(t, res, &disponibilidad)
	huecos := disponibilidad["slots"].([]any)
	if len(huecos) == 0 {
		t.Fatal("la agenda no ofrecio horarios")
	}
	slot := huecos[0].(map[string]any)["id"].(string)
	cuerpo, _ := json.Marshal(map[string]string{"conversationId": conversacion, "slotId": slot})
	res = peticionBot(t, cliente, http.MethodPost, base+"/api/bot/bookings", apiKey, cuerpo, false)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("reservar %s=%d %s", conversacion, res.StatusCode, leer(res))
	}
	var cita map[string]any
	decodificarRespuesta(t, res, &cita)
	return cita
}
