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

func TestIntegracionAPIBot(t *testing.T) {
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
	llave := bytes.Repeat([]byte{7}, 32)
	botKey := "bot-key-integracion-0000000001"
	tokenIA := "token-proveedor-secreto"
	secretoBot, _ := seguridad.CifrarSecreto(llave, botKey)
	secretoIA, _ := seguridad.CifrarSecreto(llave, tokenIA)
	var cuerpoProveedor []byte
	proveedor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+tokenIA {
			t.Error("token de proveedor incorrecto")
		}
		cuerpoProveedor, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "unauthorized") {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"error":"invalid"}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"respuesta-proveedor"}`))
	}))
	defer proveedor.Close()

	org := "org_test_bot"
	orgAjena := "org_test_bot_ajena"
	ahora := time.Now().UTC().Truncate(time.Millisecond)
	_, _ = db.ExecContext(ctx, "DELETE FROM organization WHERE id IN (?,?)", org, orgAjena)
	defer db.ExecContext(ctx, "DELETE FROM organization WHERE id IN (?,?)", org, orgAjena)
	sentencias := []struct {
		q string
		a []any
	}{
		{"INSERT INTO organization(id,name,slug,created_at) VALUES (?,'Negocio bot','negocio-bot',?)", []any{org, ahora}},
		{"INSERT INTO organization(id,name,slug,created_at) VALUES (?,'Negocio ajeno','negocio-bot-ajeno',?)", []any{orgAjena, ahora}},
		{"INSERT INTO pipeline_stage(id,organization_id,name,position,kind,created_at) VALUES ('st_bot_1',?,'Nuevo',0,'open',?),('st_bot_2',?,'Calificado',1,'open',?)", []any{org, ahora, org, ahora}},
		{"INSERT INTO contact(id,organization_id,channel,wa_identity,phone,name,ficha,created_at,updated_at) VALUES ('ct_bot',?,'whatsapp','56912345678','56912345678','Ana',JSON_OBJECT('interes','azul'),?,?)", []any{org, ahora, ahora}},
		{"INSERT INTO `lead`(id,organization_id,contact_id,stage_id,position,created_at,updated_at) VALUES ('ld_bot',?,'ct_bot','st_bot_1',0,?,?)", []any{org, ahora, ahora}},
		{"INSERT INTO conversation(id,organization_id,contact_id,is_test,channel,ai_enabled,last_inbound_at,created_at,updated_at) VALUES ('cv_bot',?,'ct_bot',0,'whatsapp',1,?,?,?)", []any{org, ahora, ahora, ahora}},
		{"INSERT INTO conversation(id,organization_id,contact_id,is_test,channel,ai_enabled,last_inbound_at,created_at,updated_at) VALUES ('cv_bot_test',?,'ct_bot',1,'whatsapp',1,?,?,?)", []any{org, ahora, ahora, ahora}},
		{"INSERT INTO contact(id,organization_id,channel,wa_identity,name,created_at,updated_at) VALUES ('ct_bot_ajeno',?,'whatsapp','56900000000','Otro',?,?)", []any{orgAjena, ahora, ahora}},
		{"INSERT INTO conversation(id,organization_id,contact_id,is_test,channel,ai_enabled,last_inbound_at,created_at,updated_at) VALUES ('cv_bot_ajena',?,'ct_bot_ajeno',0,'whatsapp',1,?,?,?)", []any{orgAjena, ahora, ahora, ahora}},
		{"INSERT INTO agent_profile(id,organization_id,enabled,name,tone,instructions,role,tools,created_at,updated_at) VALUES ('ag_bot',?,1,'Nexa','Cercano','Ayuda a vender','calificador',JSON_OBJECT('ficha',true,'etapa',true),?,?)", []any{org, ahora, ahora}},
		{"INSERT INTO kb_entry(id,organization_id,kind,question,answer,created_at,updated_at) VALUES ('kb_bot',?,'qa','¿Horario?','De 9 a 18',?,?)", []any{org, ahora, ahora}},
		{"INSERT INTO app_setting(organization_id,clave,secret_cipher,secret_iv,secret_tag,updated_at) VALUES (?,'brain_api_key',?,?,?,?)", []any{org, secretoBot.Cifrado, secretoBot.IV, secretoBot.Tag, ahora}},
		{"INSERT INTO ai_credentials(id,organization_id,provider,base_url,model,token_cipher,token_iv,token_tag,token_last4,status,created_at,updated_at) VALUES ('aic_bot',?,'openai_compatible',?,'modelo-prueba',?,?,?,'reto','active',?,?)", []any{org, proveedor.URL, secretoIA.Cifrado, secretoIA.IV, secretoIA.Tag, ahora, ahora}},
	}
	for _, s := range sentencias {
		if _, err := db.ExecContext(ctx, s.q, s.a...); err != nil {
			t.Fatalf("preparar datos: %v\n%s", err, s.q)
		}
	}

	cfg := configuracion.Configuracion{URLBase: "http://localhost", LlaveCifrado: llave, DirectorioCliente: t.TempDir()}
	api := httptest.NewServer(NuevoServidorConBus(cfg, db, slog.New(slog.NewTextHandler(io.Discard, nil)), eventos.NuevoBus()))
	defer api.Close()
	cliente := api.Client()

	// Sin credencial nunca entra.
	res, err := cliente.Get(api.URL + "/api/bot/profile")
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 401 {
		t.Fatalf("sin key=%d", res.StatusCode)
	}
	res.Body.Close()

	res = peticionBot(t, cliente, http.MethodGet, api.URL+"/api/bot/profile", botKey, nil, false)
	if res.StatusCode != 200 {
		t.Fatalf("perfil=%d %s", res.StatusCode, leer(res))
	}
	var perfil map[string]any
	decodificarRespuesta(t, res, &perfil)
	if perfil["kb"] != "P: ¿Horario?\nR: De 9 a 18" || perfil["llm"] == nil {
		t.Fatalf("perfil incompleto: %#v", perfil)
	}

	res = peticionBot(t, cliente, http.MethodGet, api.URL+"/api/bot/context?identity=56912345678", botKey, nil, false)
	if res.StatusCode != 200 {
		t.Fatalf("contexto=%d %s", res.StatusCode, leer(res))
	}
	var contexto map[string]any
	decodificarRespuesta(t, res, &contexto)
	lead := contexto["lead"].(map[string]any)
	if lead["stageName"] != "Nuevo" || len(lead["nextStages"].([]any)) != 1 {
		t.Fatalf("lead incorrecto: %#v", lead)
	}
	res = peticionBot(t, cliente, http.MethodGet, api.URL+"/api/bot/context?conversationId=cv_bot_ajena", botKey, nil, false)
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("la clave pudo cruzar organizaciones: %d %s", res.StatusCode, leer(res))
	}
	res.Body.Close()

	res = peticionBot(t, cliente, http.MethodPost, api.URL+"/api/bot/messages", botKey, []byte(`{"conversationId":"cv_bot_test","text":"Respuesta segura"}`), false)
	if res.StatusCode != 200 {
		t.Fatalf("mensaje prueba=%d %s", res.StatusCode, leer(res))
	}
	res.Body.Close()
	var generada bool
	var waID sql.NullString
	if err := db.QueryRowContext(ctx, "SELECT ai_generated,wa_message_id FROM message WHERE organization_id=? AND conversation_id='cv_bot_test'", org).Scan(&generada, &waID); err != nil || !generada || waID.Valid {
		t.Fatalf("sandbox incorrecto: ai=%v wa=%v err=%v", generada, waID, err)
	}
	_, _ = db.ExecContext(ctx, "UPDATE conversation SET handoff_at=?,handoff_reason='modelo' WHERE id='cv_bot_test'", ahora)
	res = peticionBot(t, cliente, http.MethodPost, api.URL+"/api/bot/messages", botKey, []byte(`{"conversationId":"cv_bot_test","text":"No debe salir"}`), false)
	if res.StatusCode != 409 || !strings.Contains(leer(res), "ai_paused") {
		t.Fatal("handoff no bloqueo al bot")
	}

	cuerpo := []byte(`{"model":"modelo-prueba","messages":[{"role":"user","content":"hola"}]}`)
	res = peticionBot(t, cliente, http.MethodPost, api.URL+"/api/bot/llm/chat/completions", botKey, cuerpo, true)
	if res.StatusCode != 200 || string(cuerpoProveedor) != string(cuerpo) {
		t.Fatalf("proxy altero la peticion: %d %s", res.StatusCode, cuerpoProveedor)
	}
	res.Body.Close()
	res = peticionBot(t, cliente, http.MethodPost, api.URL+"/api/bot/llm/unauthorized", botKey, []byte(`{}`), true)
	if res.StatusCode != 401 {
		t.Fatalf("estado proveedor no preservado: %d", res.StatusCode)
	}
	res.Body.Close()
	var estado string
	_ = db.QueryRowContext(ctx, "SELECT status FROM ai_credentials WHERE organization_id=?", org).Scan(&estado)
	if estado != "paused_invalid_token" {
		t.Fatalf("credencial no pausada: %s", estado)
	}
}

func peticionBot(t *testing.T, c *http.Client, metodo, url, clave string, cuerpo []byte, bearer bool) *http.Response {
	t.Helper()
	req, err := http.NewRequest(metodo, url, bytes.NewReader(cuerpo))
	if err != nil {
		t.Fatal(err)
	}
	if bearer {
		req.Header.Set("Authorization", "Bearer "+clave)
	} else {
		req.Header.Set("X-API-Key", clave)
	}
	if cuerpo != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func leer(res *http.Response) string {
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return string(b)
}
func decodificarRespuesta(t *testing.T, res *http.Response, destino any) {
	t.Helper()
	defer res.Body.Close()
	if err := json.NewDecoder(res.Body).Decode(destino); err != nil {
		t.Fatal(err)
	}
}
