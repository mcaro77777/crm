package httpapi

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"crm-api/internal/basedatos"
	"crm-api/internal/configuracion"
	"crm-api/internal/despacho"
	"crm-api/internal/eventos"
	"crm-api/internal/seguridad"
	_ "github.com/go-sql-driver/mysql"
)

// TestIntegracionCircuitoAgenteA7 es el arnes de release del circuito real:
// webhook -> MySQL -> outbox -> cerebro firmado -> /api/bot/messages -> Meta.
// Tambien fija reintentos, 4xx definitivo, handoff, lease e idempotencia.
func TestIntegracionCircuitoAgenteA7(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("TEST_DATABASE_DSN no configurada")
	}
	db, err := sql.Open("mysql", agregarParametrosMySQL(dsn))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err := basedatos.AsegurarExtensiones(ctx, db); err != nil {
		t.Fatal(err)
	}

	const (
		org       = "org_test_circuito_a7"
		botKey    = "clave-cerebro-a7-000000000000"
		metaToken = "token-meta-a7-super-secreto"
		appSecret = "app-secret-meta-a7"
	)
	llave := bytes.Repeat([]byte{17}, 32)
	ahora := time.Now().UTC().Truncate(time.Millisecond)
	_, _ = db.ExecContext(ctx, "DELETE FROM organization WHERE id=?", org)
	defer db.ExecContext(ctx, "DELETE FROM organization WHERE id=?", org)

	var metaMu sync.Mutex
	llamadasMeta := 0
	textosMeta := make([]string, 0, 4)
	metaFalso := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v25.0/phone_a7/messages" || r.Header.Get("Authorization") != "Bearer "+metaToken {
			http.Error(w, "peticion Meta invalida", http.StatusUnauthorized)
			return
		}
		var entrada struct {
			To   string `json:"to"`
			Text struct {
				Body string `json:"body"`
			} `json:"text"`
		}
		if json.NewDecoder(r.Body).Decode(&entrada) != nil || entrada.To == "" || entrada.Text.Body == "" {
			http.Error(w, "cuerpo invalido", http.StatusBadRequest)
			return
		}
		metaMu.Lock()
		llamadasMeta++
		textosMeta = append(textosMeta, entrada.Text.Body)
		id := llamadasMeta
		metaMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"messages":[{"id":"wamid.saliente.%d"}]}`, id)
	}))
	defer metaFalso.Close()

	type estadoCerebro struct {
		sync.Mutex
		modo       string
		intentos   int
		idsPorModo map[string][]string
		errores    []string
		apiURL     string
	}
	cerebro := &estadoCerebro{modo: "exito", idsPorModo: map[string][]string{}}
	cerebroFalso := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cuerpo, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		var evento despacho.Evento
		if err := json.Unmarshal(cuerpo, &evento); err != nil {
			http.Error(w, "evento invalido", http.StatusBadRequest)
			return
		}
		cerebro.Lock()
		modo := cerebro.modo
		cerebro.intentos++
		intento := cerebro.intentos
		cerebro.idsPorModo[modo] = append(cerebro.idsPorModo[modo], evento.DespachoID)
		apiURL := cerebro.apiURL
		if !despacho.FirmaValida(cuerpo, botKey, r.Header.Get(despacho.CabeceraFirma)) {
			cerebro.errores = append(cerebro.errores, "firma HMAC invalida")
		}
		if r.Header.Get(despacho.CabeceraDespacho) != evento.DespachoID {
			cerebro.errores = append(cerebro.errores, "cabecera de despacho invalida")
		}
		if r.Header.Get(despacho.CabeceraOrganizacion) != "circuito-a7" || evento.Organizacion.ID != org {
			cerebro.errores = append(cerebro.errores, "organizacion no aislada")
		}
		if bytes.Contains(cuerpo, []byte(botKey)) || bytes.Contains(cuerpo, []byte(metaToken)) || bytes.Contains(cuerpo, []byte(appSecret)) {
			cerebro.errores = append(cerebro.errores, "un secreto aparecio en el evento")
		}
		cerebro.Unlock()

		if modo == "reintento" && intento < 3 {
			http.Error(w, "temporal", http.StatusServiceUnavailable)
			return
		}
		if modo == "definitivo" {
			http.Error(w, "contrato rechazado", http.StatusBadRequest)
			return
		}
		respuesta, _ := json.Marshal(map[string]string{"conversationId": evento.Conversacion.ID, "text": "Respuesta automatica " + modo})
		req, _ := http.NewRequest(http.MethodPost, apiURL+"/api/bot/messages", bytes.NewReader(respuesta))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-API-Key", botKey)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			http.Error(w, "callback imposible", http.StatusBadGateway)
			return
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			detalle, _ := io.ReadAll(res.Body)
			http.Error(w, "callback rechazado: "+string(detalle), http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer cerebroFalso.Close()

	secretoBot, _ := seguridad.CifrarSecreto(llave, botKey)
	secretoMeta, _ := seguridad.CifrarSecreto(llave, metaToken)
	secretoApp, _ := seguridad.CifrarSecreto(llave, appSecret)
	prepararCircuitoA7(t, db, org, ahora, cerebroFalso.URL, secretoBot, secretoMeta, secretoApp)

	var logs bytes.Buffer
	cfg := configuracion.Configuracion{
		URLBase: "http://localhost", SecretoAutenticacion: "auth-secret-a7-con-mas-de-32-caracteres",
		LlaveCifrado: llave, DirectorioCliente: t.TempDir(), MetaGraphBaseURL: metaFalso.URL,
	}
	bus := eventos.NuevoBus()
	api := httptest.NewServer(NuevoServidorConBus(cfg, db, slog.New(slog.NewJSONHandler(&logs, nil)), bus))
	defer api.Close()
	cerebro.Lock()
	cerebro.apiURL = api.URL
	cerebro.Unlock()
	worker := despacho.NuevoWorker(db, llave, bus, slog.New(slog.NewJSONHandler(&logs, nil)))
	tokenWebhook := (&Servidor{cfg: cfg}).tokenWebhook(org)

	t.Run("circuito completo e idempotencia", func(t *testing.T) {
		cuerpo := webhookA7("56911111111", "wamid.a7.exito", "Hola, quiero comprar")
		enviarWebhookA7(t, api.URL, tokenWebhook, appSecret, cuerpo)
		enviarWebhookA7(t, api.URL, tokenWebhook, appSecret, cuerpo)
		conv, dsp := idsCircuitoA7(t, db, org, "wamid.a7.exito")
		forzarDespachoA7(t, db, dsp)
		procesado, err := worker.ProcesarUnaVez(ctx)
		if err != nil || !procesado {
			t.Fatalf("worker no proceso el circuito: procesado=%v err=%v", procesado, err)
		}
		assertDespachoA7(t, db, dsp, "entregado", 1)
		var entrantes, salientes, eventos int
		_ = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM message WHERE organization_id=? AND conversation_id=? AND direction='in'", org, conv).Scan(&entrantes)
		_ = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM message WHERE organization_id=? AND conversation_id=? AND direction='out' AND ai_generated=1 AND origin='ai'", org, conv).Scan(&salientes)
		_ = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM webhook_event WHERE organization_id=?", org).Scan(&eventos)
		if entrantes != 1 || salientes != 1 || eventos != 1 {
			t.Fatalf("idempotencia rota: entrantes=%d salientes=%d webhooks=%d", entrantes, salientes, eventos)
		}
	})

	t.Run("reintenta 5xx con el mismo despacho", func(t *testing.T) {
		cerebro.Lock()
		cerebro.modo, cerebro.intentos = "reintento", 0
		cerebro.Unlock()
		enviarWebhookA7(t, api.URL, tokenWebhook, appSecret, webhookA7("56922222222", "wamid.a7.retry", "¿Siguen atendiendo?"))
		_, dsp := idsCircuitoA7(t, db, org, "wamid.a7.retry")
		for intento := 1; intento <= 3; intento++ {
			forzarDespachoA7(t, db, dsp)
			procesado, err := worker.ProcesarUnaVez(ctx)
			if err != nil || !procesado {
				t.Fatalf("intento %d no procesado: %v", intento, err)
			}
		}
		assertDespachoA7(t, db, dsp, "entregado", 3)
		cerebro.Lock()
		ids := append([]string(nil), cerebro.idsPorModo["reintento"]...)
		cerebro.Unlock()
		if len(ids) != 3 || ids[0] != ids[1] || ids[1] != ids[2] {
			t.Fatalf("el reintento cambio dispatchId: %v", ids)
		}
	})

	t.Run("4xx definitivo entrega a una persona", func(t *testing.T) {
		cerebro.Lock()
		cerebro.modo, cerebro.intentos = "definitivo", 0
		cerebro.Unlock()
		enviarWebhookA7(t, api.URL, tokenWebhook, appSecret, webhookA7("56933333333", "wamid.a7.4xx", "Necesito ayuda"))
		conv, dsp := idsCircuitoA7(t, db, org, "wamid.a7.4xx")
		forzarDespachoA7(t, db, dsp)
		_, err := worker.ProcesarUnaVez(ctx)
		if err != nil {
			t.Fatal(err)
		}
		assertDespachoA7(t, db, dsp, "caducado_a_humano", 1)
		var ia bool
		var motivo sql.NullString
		if err := db.QueryRowContext(ctx, "SELECT ai_enabled,handoff_reason FROM conversation WHERE organization_id=? AND id=?", org, conv).Scan(&ia, &motivo); err != nil || ia || !motivo.Valid || motivo.String != "error" {
			t.Fatalf("handoff incorrecto: ia=%v motivo=%v err=%v", ia, motivo, err)
		}
	})

	t.Run("recupera un lease vencido", func(t *testing.T) {
		cerebro.Lock()
		cerebro.modo, cerebro.intentos = "recuperado", 0
		cerebro.Unlock()
		enviarWebhookA7(t, api.URL, tokenWebhook, appSecret, webhookA7("56944444444", "wamid.a7.lease", "¿Hay alguien?"))
		_, dsp := idsCircuitoA7(t, db, org, "wamid.a7.lease")
		if _, err := db.ExecContext(ctx, "UPDATE dispatch SET status='en_vuelo',attempts=0,leased_until=?,not_before=? WHERE id=?", time.Now().UTC().Add(-time.Minute), time.Now().UTC().Add(-time.Minute), dsp); err != nil {
			t.Fatal(err)
		}
		procesado, err := worker.ProcesarUnaVez(ctx)
		if err != nil || !procesado {
			t.Fatalf("lease no recuperado: %v", err)
		}
		assertDespachoA7(t, db, dsp, "entregado", 1)
	})

	cerebro.Lock()
	erroresCerebro := append([]string(nil), cerebro.errores...)
	cerebro.Unlock()
	metaMu.Lock()
	totalMeta := llamadasMeta
	metaMu.Unlock()
	if len(erroresCerebro) > 0 {
		t.Fatalf("validaciones del cerebro falso: %v", erroresCerebro)
	}
	if totalMeta != 3 {
		t.Fatalf("Meta debio recibir tres respuestas exitosas y recibio %d", totalMeta)
	}
	registro := logs.String()
	if strings.Contains(registro, botKey) || strings.Contains(registro, metaToken) || strings.Contains(registro, appSecret) {
		t.Fatal("un secreto aparecio en los logs")
	}
}

func prepararCircuitoA7(t *testing.T, db *sql.DB, org string, ahora time.Time, urlCerebro string, bot, meta, app seguridad.SecretoCifrado) {
	t.Helper()
	sentencias := []struct {
		q string
		a []any
	}{
		{"INSERT INTO organization(id,name,slug,created_at) VALUES (?,'Circuito A7','circuito-a7',?)", []any{org, ahora}},
		{"INSERT INTO pipeline_stage(id,organization_id,name,position,kind,created_at) VALUES ('st_circuito_a7',?,'Nuevo',0,'open',?)", []any{org, ahora}},
		{"INSERT INTO agent_profile(id,organization_id,enabled,name,created_at,updated_at) VALUES ('ag_circuito_a7',?,1,'Agente A7',?,?)", []any{org, ahora, ahora}},
		{"INSERT INTO app_setting(organization_id,clave,valor,updated_at) VALUES (?,'brain_dispatch_url',?,?)", []any{org, urlCerebro, ahora}},
		{"INSERT INTO app_setting(organization_id,clave,secret_cipher,secret_iv,secret_tag,updated_at) VALUES (?,'brain_api_key',?,?,?,?)", []any{org, bot.Cifrado, bot.IV, bot.Tag, ahora}},
		{"INSERT INTO app_setting(organization_id,clave,secret_cipher,secret_iv,secret_tag,updated_at) VALUES (?,'meta_app_secret',?,?,?,?)", []any{org, app.Cifrado, app.IV, app.Tag, ahora}},
		{"INSERT INTO app_setting(organization_id,clave,valor,updated_at) VALUES (?,'meta_graph_version','v25.0',?)", []any{org, ahora}},
		{"INSERT INTO meta_credentials(id,organization_id,waba_id,phone_number_id,token_cipher,token_iv,token_tag,status,created_at,updated_at) VALUES ('mc_circuito_a7',?,'waba_a7','phone_a7',?,?,?,'connected',?,?)", []any{org, meta.Cifrado, meta.IV, meta.Tag, ahora, ahora}},
	}
	for _, sentencia := range sentencias {
		if _, err := db.Exec(sentencia.q, sentencia.a...); err != nil {
			t.Fatalf("preparar circuito A7: %v\n%s", err, sentencia.q)
		}
	}
}

func webhookA7(identidad, mensajeID, texto string) []byte {
	marca := fmt.Sprintf("%d", time.Now().UTC().Unix())
	cuerpo, _ := json.Marshal(map[string]any{"entry": []any{map[string]any{"changes": []any{map[string]any{"value": map[string]any{
		"metadata": map[string]string{"phone_number_id": "phone_a7"},
		"contacts": []any{map[string]any{"profile": map[string]string{"name": "Cliente " + identidad}, "wa_id": identidad}},
		"messages": []any{map[string]any{"from": identidad, "id": mensajeID, "timestamp": marca, "type": "text", "text": map[string]string{"body": texto}}},
	}}}}}})
	return cuerpo
}

func enviarWebhookA7(t *testing.T, apiURL, token, secreto string, cuerpo []byte) {
	t.Helper()
	mac := hmac.New(sha256.New, []byte(secreto))
	_, _ = mac.Write(cuerpo)
	req, _ := http.NewRequest(http.MethodPost, apiURL+"/api/webhooks/wa/"+token, bytes.NewReader(cuerpo))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		detalle, _ := io.ReadAll(res.Body)
		t.Fatalf("webhook=%d %s", res.StatusCode, detalle)
	}
}

func idsCircuitoA7(t *testing.T, db *sql.DB, org, waID string) (string, string) {
	t.Helper()
	var conv, dsp string
	err := db.QueryRow(`SELECT cv.id,d.id FROM message m JOIN conversation cv ON cv.id=m.conversation_id AND cv.organization_id=m.organization_id JOIN dispatch d ON d.conversation_id=cv.id AND d.organization_id=cv.organization_id WHERE m.organization_id=? AND m.wa_message_id=?`, org, waID).Scan(&conv, &dsp)
	if err != nil {
		t.Fatalf("buscar circuito %s: %v", waID, err)
	}
	return conv, dsp
}

func forzarDespachoA7(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	if _, err := db.Exec("UPDATE dispatch SET not_before=? WHERE id=? AND status='pendiente'", time.Now().UTC().Add(-time.Second), id); err != nil {
		t.Fatal(err)
	}
}

func assertDespachoA7(t *testing.T, db *sql.DB, id, estado string, intentos int) {
	t.Helper()
	var actual string
	var n int
	if err := db.QueryRow("SELECT status,attempts FROM dispatch WHERE id=?", id).Scan(&actual, &n); err != nil || actual != estado || n != intentos {
		t.Fatalf("despacho %s: estado=%s intentos=%d err=%v; esperado %s/%d", id, actual, n, err, estado, intentos)
	}
}

func agregarParametrosMySQL(dsn string) string {
	separador := "?"
	if strings.Contains(dsn, "?") {
		separador = "&"
	}
	return dsn + separador + "parseTime=true&loc=UTC"
}
