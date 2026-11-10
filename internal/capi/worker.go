package capi

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"crm-api/internal/meta"
	"crm-api/internal/seguridad"
)

const (
	Lease       = 3 * time.Minute
	MaxIntentos = 6
	intervalo   = 3 * time.Second
)

type Worker struct {
	db     *sql.DB
	llave  []byte
	graph  string
	logger *slog.Logger
	ahora  func() time.Time
}

func NuevoWorker(db *sql.DB, llave []byte, graph string, logger *slog.Logger) *Worker {
	return &Worker{db: db, llave: llave, graph: graph, logger: logger, ahora: func() time.Time { return time.Now().UTC() }}
}

// AsegurarEsquema agrega solamente metadatos de cola. Es idempotente y permite
// actualizar instalaciones existentes sin recrear conversion_event.
func AsegurarEsquema(ctx context.Context, db *sql.DB) error {
	columnas := []struct{ nombre, ddl string }{{"attempts", "INT UNSIGNED NOT NULL DEFAULT 0"}, {"not_before", "DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3)"}, {"leased_until", "DATETIME(3) NULL"}, {"updated_at", "DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3)"}}
	for _, c := range columnas {
		var existe int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='conversion_event' AND column_name=?`, c.nombre).Scan(&existe); err != nil {
			return err
		}
		if existe == 0 {
			if _, err := db.ExecContext(ctx, "ALTER TABLE conversion_event ADD COLUMN `"+c.nombre+"` "+c.ddl); err != nil {
				return fmt.Errorf("agregar %s a conversion_event: %w", c.nombre, err)
			}
		}
	}
	return nil
}

func (w *Worker) Ejecutar(ctx context.Context) {
	ticker := time.NewTicker(intervalo)
	defer ticker.Stop()
	w.tanda(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.tanda(ctx)
		}
	}
}
func (w *Worker) tanda(ctx context.Context) {
	for i := 0; i < 10; i++ {
		ok, err := w.ProcesarUnaVez(ctx)
		if err != nil {
			w.logger.Error("fallo worker CAPI", "error", err)
			return
		}
		if !ok {
			return
		}
	}
}

type fila struct {
	id, org  string
	intentos int
}

func (w *Worker) ProcesarUnaVez(ctx context.Context) (bool, error) {
	f, err := w.tomar(ctx, w.ahora())
	if err != nil || f == nil {
		return false, err
	}
	trace, err := w.enviar(ctx, *f)
	ahora := w.ahora()
	if err == nil {
		res, e := w.db.ExecContext(ctx, `UPDATE conversion_event SET status='sent',error=NULL,fb_trace_id=?,sent_at=?,leased_until=NULL,updated_at=? WHERE id=? AND organization_id=? AND status='pending' AND attempts=?`, trace, ahora, ahora, f.id, f.org, f.intentos)
		if e != nil {
			return true, e
		}
		filas, e := res.RowsAffected()
		if e != nil {
			return true, e
		}
		if filas != 1 {
			return true, fmt.Errorf("se perdio el arrendamiento del evento CAPI %s", f.id)
		}
		return true, nil
	}
	detalle := recortar(err.Error())
	if f.intentos >= MaxIntentos {
		_, e := w.db.ExecContext(ctx, `UPDATE conversion_event SET status='failed',error=?,leased_until=NULL,updated_at=? WHERE id=? AND organization_id=? AND status='pending' AND attempts=?`, detalle, ahora, f.id, f.org, f.intentos)
		return true, e
	}
	_, e := w.db.ExecContext(ctx, `UPDATE conversion_event SET error=?,not_before=?,leased_until=NULL,updated_at=? WHERE id=? AND organization_id=? AND status='pending' AND attempts=?`, detalle, ahora.Add(espera(f.intentos)), ahora, f.id, f.org, f.intentos)
	return true, e
}

func (w *Worker) tomar(ctx context.Context, ahora time.Time) (*fila, error) {
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var f fila
	err = tx.QueryRowContext(ctx, `SELECT id,organization_id,attempts FROM conversion_event WHERE status='pending' AND not_before<=? AND (leased_until IS NULL OR leased_until<=?) ORDER BY not_before,id LIMIT 1 FOR UPDATE SKIP LOCKED`, ahora, ahora).Scan(&f.id, &f.org, &f.intentos)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	f.intentos++
	res, err := tx.ExecContext(ctx, `UPDATE conversion_event SET attempts=?,leased_until=?,updated_at=? WHERE id=? AND organization_id=? AND status='pending' AND attempts=?`, f.intentos, ahora.Add(Lease), ahora, f.id, f.org, f.intentos-1)
	if err != nil {
		return nil, err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return nil, nil
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &f, nil
}

func (w *Worker) enviar(ctx context.Context, f fila) (string, error) {
	var dataset, cifrado, iv, tag, nombre, ctwa, moneda string
	var creado time.Time
	var valor sql.NullInt64
	const consulta = `SELECT cs.dataset_id,cs.token_cipher,cs.token_iv,cs.token_tag,ce.event_name,ce.created_at,COALESCE(aa.ctwa_clid,''),l.amount_cents,COALESCE(l.currency,'USD') FROM conversion_event ce JOIN capi_settings cs ON cs.organization_id=ce.organization_id AND cs.status='connected' LEFT JOIN ad_attribution aa ON aa.id=ce.attribution_id AND aa.organization_id=ce.organization_id JOIN conversation cv ON cv.id=ce.conversation_id AND cv.organization_id=ce.organization_id LEFT JOIN ` + "`lead`" + ` l ON l.contact_id=cv.contact_id AND l.organization_id=cv.organization_id WHERE ce.organization_id=? AND ce.id=?`
	err := w.db.QueryRowContext(ctx, consulta, f.org, f.id).Scan(&dataset, &cifrado, &iv, &tag, &nombre, &creado, &ctwa, &valor, &moneda)
	if err != nil {
		return "", err
	}
	token, err := seguridad.DescifrarSecreto(w.llave, seguridad.SecretoCifrado{Cifrado: cifrado, IV: iv, Tag: tag})
	if err != nil {
		return "", err
	}
	evento := map[string]any{"event_name": nombre, "event_time": creado.Unix(), "action_source": "business_messaging", "event_id": f.id, "user_data": map[string]any{"ctwa_clid": ctwa}}
	if valor.Valid && valor.Int64 > 0 {
		evento["custom_data"] = map[string]any{"value": float64(valor.Int64) / 100, "currency": moneda}
	}
	return meta.NuevoConBaseURL(token, "v25.0", w.graph).EnviarEventosCAPI(ctx, dataset, []any{evento})
}

func espera(n int) time.Duration {
	e := []time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute, time.Hour, 6 * time.Hour}
	if n < 1 {
		return e[0]
	}
	if n >= len(e) {
		return e[len(e)-1]
	}
	return e[n-1]
}
func recortar(s string) string {
	r := []rune(s)
	if len(r) > 500 {
		r = r[:500]
	}
	return string(r)
}
