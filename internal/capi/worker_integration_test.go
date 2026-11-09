package capi

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"crm-api/internal/seguridad"
	_ "github.com/go-sql-driver/mysql"
)

func TestIntegracionWorkerCAPI(t *testing.T) {
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
	ctx := context.Background()
	if err := AsegurarEsquema(ctx, db); err != nil {
		t.Fatal(err)
	}
	const org = "org_test_capi_worker"
	_, _ = db.Exec("DELETE FROM organization WHERE id=?", org)
	defer db.Exec("DELETE FROM organization WHERE id=?", org)
	ahora := time.Now().UTC().Truncate(time.Millisecond)
	llave := bytes.Repeat([]byte{9}, 32)
	token := "token-capi-integracion-123"
	sec, _ := seguridad.CifrarSecreto(llave, token)
	sentencias := []struct {
		q string
		a []any
	}{{"INSERT INTO organization(id,name,created_at) VALUES (?,'CAPI',?)", []any{org, ahora}}, {"INSERT INTO pipeline_stage(id,organization_id,name,position,kind,created_at) VALUES ('st_capi',?,'Ganado',0,'won',?)", []any{org, ahora}}, {"INSERT INTO contact(id,organization_id,channel,wa_identity,name,created_at,updated_at) VALUES ('ct_capi',?,'whatsapp','5691','Ana',?,?)", []any{org, ahora, ahora}}, {"INSERT INTO conversation(id,organization_id,contact_id,channel,created_at,updated_at) VALUES ('cv_capi',?,'ct_capi','whatsapp',?,?)", []any{org, ahora, ahora}}, {"INSERT INTO `lead`(id,organization_id,contact_id,stage_id,amount_cents,currency,created_at,updated_at) VALUES ('ld_capi',?,'ct_capi','st_capi',15990,'CLP',?,?)", []any{org, ahora, ahora}}, {"INSERT INTO capi_settings(id,organization_id,dataset_id,token_cipher,token_iv,token_tag,status,created_at,updated_at) VALUES ('cap_capi',?,'dataset',?,?,?,'connected',?,?)", []any{org, sec.Cifrado, sec.IV, sec.Tag, ahora, ahora}}, {"INSERT INTO conversion_event(id,organization_id,conversation_id,event_name,status,created_at,attempts,not_before,updated_at) VALUES ('ce_capi',?,'cv_capi','Purchase','pending',?,0,?,?)", []any{org, ahora, ahora.Add(-time.Second), ahora}}}
	for _, s := range sentencias {
		if _, err := db.Exec(s.q, s.a...); err != nil {
			t.Fatalf("%v: %s", err, s.q)
		}
	}
	var llamadas atomic.Int32
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "auth", 401)
			return
		}
		if llamadas.Add(1) == 1 {
			http.Error(w, "temporal", 503)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"fbtrace_id":"trace-ok"}`)
	}))
	defer fake.Close()
	worker := NuevoWorker(db, llave, fake.URL, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if ok, err := worker.ProcesarUnaVez(ctx); err != nil || !ok {
		t.Fatalf("primer intento: %v %v", ok, err)
	}
	_, _ = db.Exec("UPDATE conversion_event SET not_before=? WHERE id='ce_capi'", ahora.Add(-time.Second))
	if ok, err := worker.ProcesarUnaVez(ctx); err != nil || !ok {
		t.Fatalf("segundo intento: %v %v", ok, err)
	}
	var estado, trace string
	var intentos int
	if err := db.QueryRow("SELECT status,attempts,fb_trace_id FROM conversion_event WHERE id='ce_capi'").Scan(&estado, &intentos, &trace); err != nil || estado != "sent" || intentos != 2 || trace != "trace-ok" {
		t.Fatalf("final: %s %d %s %v", estado, intentos, trace, err)
	}
}
