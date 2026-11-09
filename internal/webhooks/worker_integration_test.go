package webhooks

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

func TestIntegracionWorkerWebhooksMySQL(t *testing.T) {
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
	ctx := context.Background()
	const org = "org_test_webhook_worker"
	_, _ = db.ExecContext(ctx, "DELETE FROM organization WHERE id=?", org)
	defer db.ExecContext(ctx, "DELETE FROM organization WHERE id=?", org)
	ahora := time.Now().UTC().Truncate(time.Millisecond)
	if _, err := db.ExecContext(ctx, "INSERT INTO organization(id,name,created_at) VALUES (?,'Worker webhook',?)", org, ahora); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	intentos := map[string]int{}
	procesador := func(_ context.Context, organizacion, fuente string, payload []byte) error {
		if organizacion != org || fuente != "whatsapp" {
			return errors.New("se cruzo la organizacion o fuente")
		}
		var entrada struct {
			Caso string `json:"caso"`
		}
		if err := json.Unmarshal(payload, &entrada); err != nil {
			return err
		}
		caso := entrada.Caso
		mu.Lock()
		intentos[caso]++
		n := intentos[caso]
		mu.Unlock()
		if caso == "reintento" && n < 3 {
			return errors.New("fallo temporal")
		}
		if caso == "agotado" {
			return errors.New("fallo permanente con detalle interno")
		}
		return nil
	}
	worker := NuevoWorker(db, procesador, slog.New(slog.NewTextHandler(io.Discard, nil)))

	insertarEvento(t, db, org, "whe_retry", `{"caso":"reintento"}`, "pendiente", 0, ahora.Add(-time.Second), nil, ahora)
	for i := 1; i <= 3; i++ {
		if _, err := db.ExecContext(ctx, "UPDATE webhook_event SET not_before=? WHERE id=?", ahora.Add(-time.Second), "whe_retry"); err != nil {
			t.Fatal(err)
		}
		ok, err := worker.ProcesarUnaVez(ctx)
		if err != nil || !ok {
			t.Fatalf("reintento %d: ok=%v err=%v", i, ok, err)
		}
	}
	assertEvento(t, db, "whe_retry", "procesado", 3)

	leaseVencido := ahora.Add(-time.Minute)
	insertarEvento(t, db, org, "whe_lease", `{"caso":"lease"}`, "en_proceso", 0, ahora.Add(-time.Minute), &leaseVencido, ahora)
	ok, err := worker.ProcesarUnaVez(ctx)
	if err != nil || !ok {
		t.Fatalf("recuperar lease: ok=%v err=%v", ok, err)
	}
	assertEvento(t, db, "whe_lease", "procesado", 1)

	insertarEvento(t, db, org, "whe_agotado", `{"caso":"agotado"}`, "pendiente", 0, ahora.Add(-time.Second), nil, ahora)
	for i := 0; i < MaxIntentos; i++ {
		_, _ = db.ExecContext(ctx, "UPDATE webhook_event SET not_before=? WHERE id=?", ahora.Add(-time.Second), "whe_agotado")
		if ok, err := worker.ProcesarUnaVez(ctx); err != nil || !ok {
			t.Fatalf("agotamiento %d: ok=%v err=%v", i+1, ok, err)
		}
	}
	assertEvento(t, db, "whe_agotado", "agotado", MaxIntentos)

	insertarEvento(t, db, org, "whe_concurrente", `{"caso":"concurrente"}`, "pendiente", 0, ahora.Add(-time.Second), nil, ahora)
	var procesados atomic.Int32
	workerConcurrente := NuevoWorker(db, func(context.Context, string, string, []byte) error {
		procesados.Add(1)
		time.Sleep(30 * time.Millisecond)
		return nil
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = workerConcurrente.ProcesarUnaVez(ctx)
		}()
	}
	wg.Wait()
	if procesados.Load() != 1 {
		t.Fatalf("procesamiento doble: %d", procesados.Load())
	}
	assertEvento(t, db, "whe_concurrente", "procesado", 1)

	insertarEvento(t, db, org, "whe_old_ok", `{"caso":"viejo-ok"}`, "procesado", 1, ahora.Add(-10*24*time.Hour), nil, ahora.Add(-8*24*time.Hour))
	_, _ = db.ExecContext(ctx, "UPDATE webhook_event SET processed_at=?,updated_at=? WHERE id='whe_old_ok'", ahora.Add(-8*24*time.Hour), ahora.Add(-8*24*time.Hour))
	insertarEvento(t, db, org, "whe_old_bad", `{"caso":"viejo-bad"}`, "agotado", 6, ahora.Add(-40*24*time.Hour), nil, ahora.Add(-31*24*time.Hour))
	borrados, err := worker.Limpiar(ctx, ahora)
	if err != nil || borrados != 2 {
		t.Fatalf("limpieza: borrados=%d err=%v", borrados, err)
	}
}

func insertarEvento(t *testing.T, db *sql.DB, org, id, payload, estado string, intentos int, noAntes time.Time, lease *time.Time, actualizado time.Time) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO webhook_event(id,organization_id,fuente,payload,huella,status,attempts,not_before,leased_until,created_at,updated_at) VALUES (?,?,'whatsapp',?,?,?, ?,?,?,?,?)`, id, org, payload, id, estado, intentos, noAntes, lease, actualizado, actualizado)
	if err != nil {
		t.Fatal(err)
	}
}

func assertEvento(t *testing.T, db *sql.DB, id, estado string, intentos int) {
	t.Helper()
	var actual string
	var n int
	if err := db.QueryRow("SELECT status,attempts FROM webhook_event WHERE id=?", id).Scan(&actual, &n); err != nil || actual != estado || n != intentos {
		t.Fatalf("evento %s: %s/%d err=%v; esperado %s/%d", id, actual, n, err, estado, intentos)
	}
}
