package httpapi

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"crm-api/internal/basedatos"
	"crm-api/internal/configuracion"
	"crm-api/internal/despacho"
	"crm-api/internal/eventos"
	"crm-api/internal/handoff"
	_ "github.com/go-sql-driver/mysql"
)

func TestIntegracionCanalesE3MetaZernioYFiltros(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("TEST_DATABASE_DSN no configurada")
	}
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	db, err := sql.Open("mysql", dsn+sep+"parseTime=true&loc=UTC")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = basedatos.AsegurarExtensiones(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	var llamadasMeta, llamadasZernio atomic.Int32
	metaFalso := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			llamadasMeta.Add(1)
			_, _ = w.Write([]byte(`{"message_id":"mid-out"}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"ig_e3","username":"negocio"}`))
	}))
	defer metaFalso.Close()
	zernioFalso := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token-zernio" {
			t.Error("token Zernio ausente")
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			llamadasZernio.Add(1)
			_, _ = w.Write([]byte(`{"id":"z-out"}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"tt_e3"}`))
	}))
	defer zernioFalso.Close()

	const org = "org_test_canales_e3"
	ctx := context.Background()
	_, _ = db.ExecContext(ctx, "DELETE FROM organization WHERE id=?", org)
	defer db.ExecContext(ctx, "DELETE FROM organization WHERE id=?", org)
	ahora := time.Now().UTC().Truncate(time.Millisecond)
	sentencias := []struct {
		q string
		a []any
	}{
		{"INSERT INTO organization(id,name,slug,created_at) VALUES (?,'Canales E3','canales-e3',?)", []any{org, ahora}},
		{"INSERT INTO pipeline_stage(id,organization_id,name,position,kind,created_at) VALUES ('st_e3',?,'Nuevo',0,'open',?)", []any{org, ahora}},
		{"INSERT INTO agent_profile(id,organization_id,enabled,name,role,tools,created_at,updated_at) VALUES ('ag_e3',?,0,'Nexa','calificador',JSON_OBJECT(),?,?)", []any{org, ahora, ahora}},
	}
	for _, x := range sentencias {
		if _, err = db.ExecContext(ctx, x.q, x.a...); err != nil {
			t.Fatal(err)
		}
	}
	s := &Servidor{cfg: configuracion.Configuracion{LlaveCifrado: bytes.Repeat([]byte{9}, 32), MetaGraphBaseURL: metaFalso.URL, ZernioBaseURL: zernioFalso.URL}, db: db, logger: slog.New(slog.NewTextHandler(io.Discard, nil)), bus: eventos.NuevoBus(), despachos: despacho.NuevoRepositorio(db), handoffs: handoff.Nuevo(db)}
	if err = s.persistirCredencialSocial(ctx, org, credencialCanal{Canal: "instagram", Fuente: "meta", CuentaID: "ig_e3", Nombre: "Negocio", Token: "token-meta"}); err != nil {
		t.Fatal(err)
	}
	if err = s.persistirCredencialSocial(ctx, org, credencialCanal{Canal: "tiktok", Fuente: "zernio", CuentaID: "tt_e3", Nombre: "TikTok", Token: "token-zernio", WebhookSecret: "secreto-zernio"}); err != nil {
		t.Fatal(err)
	}
	for clave, valor := range map[string]string{"whatsapp_source": "zernio", "whatsapp_zernio_account_id": "wa_e3", "whatsapp_zernio_name": "WhatsApp Zernio"} {
		if err = s.guardarAjuste(ctx, org, clave, valor); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.guardarSecreto(ctx, org, "whatsapp_zernio_token", "token-zernio"); err != nil {
		t.Fatal(err)
	}
	if err = s.guardarSecreto(ctx, org, "whatsapp_zernio_webhook_secret", "secreto-zernio"); err != nil {
		t.Fatal(err)
	}
	if err = s.guardarMensajeCanal(ctx, org, mensajeCanalEntrante{Canal: "instagram", Remitente: "persona-ig", Nombre: "Ana IG", ID: "in-ig", Texto: "Hola", Tipo: "text", Marca: ahora}); err != nil {
		t.Fatal(err)
	}
	if err = s.procesarEventoZernio(ctx, org, []byte(`{"platform":"tiktok","account_id":"tt_e3","sender_id":"persona-tt","sender_name":"Luis TT","message_id":"in-tt","text":"Precio","timestamp":"`+ahora.Format(time.RFC3339)+`"}`)); err != nil {
		t.Fatal(err)
	}
	if err = s.procesarEventoZernio(ctx, org, []byte(`{"platform":"whatsapp","account_id":"wa_e3","sender_id":"56911111111","sender_name":"Marta WA","message_id":"in-wa","text":"Hola","timestamp":"`+ahora.Format(time.RFC3339)+`"}`)); err != nil {
		t.Fatal(err)
	}
	for _, caso := range []struct{ identidad, canal string }{{"ig:persona-ig", "instagram"}, {"tt:persona-tt", "tiktok"}, {"56911111111", "whatsapp"}} {
		var conv string
		if err = db.QueryRowContext(ctx, `SELECT cv.id FROM conversation cv JOIN contact c ON c.id=cv.contact_id AND c.organization_id=cv.organization_id WHERE cv.organization_id=? AND c.wa_identity=?`, org, caso.identidad).Scan(&conv); err != nil {
			t.Fatal(err)
		}
		if _, err = s.enviarTextoConversacion(ctx, org, conv, "Respuesta", false); err != nil {
			t.Fatalf("enviar %s: %v", caso.canal, err)
		}
	}
	if llamadasMeta.Load() != 1 || llamadasZernio.Load() != 2 {
		t.Fatalf("transportes incorrectos: meta=%d zernio=%d", llamadasMeta.Load(), llamadasZernio.Load())
	}
	var tokenCifrado string
	if err = db.QueryRowContext(ctx, `SELECT token_cipher FROM tiktok_credentials WHERE organization_id=?`, org).Scan(&tokenCifrado); err != nil || strings.Contains(tokenCifrado, "token-zernio") {
		t.Fatalf("token no cifrado: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/conversations?canal=instagram&vista=sin_asignar", nil).WithContext(context.WithValue(ctx, claveSesion, Sesion{UsuarioID: "u_e3", OrganizacionID: org, Rol: "owner"}))
	res := httptest.NewRecorder()
	s.listarConversaciones(res, req)
	if res.Code != 200 {
		t.Fatalf("filtros=%d %s", res.Code, res.Body.String())
	}
	var lista struct {
		Items []conversacionDTO `json:"items"`
	}
	if json.Unmarshal(res.Body.Bytes(), &lista) != nil || len(lista.Items) != 1 || lista.Items[0].Canal != "instagram" {
		t.Fatalf("filtro incorrecto: %s", res.Body.String())
	}
}

func TestFirmaWebhookZernio(t *testing.T) {
	cuerpo := []byte(`{"id":"1"}`)
	mac := hmac.New(sha256.New, []byte("secreto"))
	_, _ = mac.Write(cuerpo)
	firma := hex.EncodeToString(mac.Sum(nil))
	if !firmaWebhookValida(cuerpo, "sha256="+firma, "secreto") {
		t.Fatal("rechazo firma valida")
	}
	if firmaWebhookValida(cuerpo, firma, "otro") {
		t.Fatal("acepto firma invalida")
	}
}
