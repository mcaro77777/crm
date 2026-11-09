// Package webhooks implementa el inbox durable de entregas externas.
package webhooks

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strings"
	"time"
)

const (
	Lease             = 3 * time.Minute
	MaxIntentos       = 6
	intervaloBarrido  = 2 * time.Second
	intervaloLimpieza = time.Hour
)

type Procesador func(context.Context, string, string, []byte) error

type Fila struct {
	ID, OrganizacionID, Fuente, Estado string
	Payload                            []byte
	Intentos                           int
	LeaseHasta                         *time.Time
}

type Worker struct {
	db       *sql.DB
	procesar Procesador
	logger   *slog.Logger
	ahora    func() time.Time
}

func NuevoWorker(db *sql.DB, procesar Procesador, logger *slog.Logger) *Worker {
	return &Worker{db: db, procesar: procesar, logger: logger, ahora: func() time.Time { return time.Now().UTC() }}
}

// Ejecutar procesa de inmediato al arrancar: asi recupera pendientes y leases
// vencidos antes del primer tick. La coordinacion vive completamente en MySQL.
func (w *Worker) Ejecutar(ctx context.Context) {
	barrido := time.NewTicker(intervaloBarrido)
	limpieza := time.NewTicker(intervaloLimpieza)
	defer barrido.Stop()
	defer limpieza.Stop()
	w.tanda(ctx)
	w.limpiar(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-barrido.C:
			w.tanda(ctx)
		case <-limpieza.C:
			w.limpiar(ctx)
		}
	}
}

func (w *Worker) tanda(ctx context.Context) {
	for i := 0; i < 20; i++ {
		ok, err := w.ProcesarUnaVez(ctx)
		if err != nil {
			w.logger.Error("fallo al reclamar webhook", "error", err)
			return
		}
		if !ok {
			return
		}
	}
}

// ProcesarUnaVez permite que el arnes de release ejecute exactamente el mismo
// camino del servicio sin depender del reloj del ticker.
func (w *Worker) ProcesarUnaVez(ctx context.Context) (bool, error) {
	fila, err := w.tomarUno(ctx, w.ahora())
	if err != nil || fila == nil {
		return false, err
	}
	err = w.procesar(ctx, fila.OrganizacionID, fila.Fuente, fila.Payload)
	ahora := w.ahora()
	if err == nil {
		_, err = w.db.ExecContext(ctx, `UPDATE webhook_event SET status='procesado',leased_until=NULL,last_error=NULL,processed_at=?,updated_at=? WHERE id=? AND status='en_proceso' AND attempts=?`, ahora, ahora, fila.ID, fila.Intentos)
		return true, err
	}
	detalle := recortar(err.Error())
	if fila.Intentos >= MaxIntentos {
		_, updateErr := w.db.ExecContext(ctx, `UPDATE webhook_event SET status='agotado',leased_until=NULL,last_error=?,updated_at=? WHERE id=? AND status='en_proceso' AND attempts=?`, detalle, ahora, fila.ID, fila.Intentos)
		return true, updateErr
	}
	_, updateErr := w.db.ExecContext(ctx, `UPDATE webhook_event SET status='pendiente',not_before=?,leased_until=NULL,last_error=?,updated_at=? WHERE id=? AND status='en_proceso' AND attempts=?`, ahora.Add(EsperaReintento(fila.Intentos)), detalle, ahora, fila.ID, fila.Intentos)
	return true, updateErr
}

func (w *Worker) tomarUno(ctx context.Context, ahora time.Time) (*Fila, error) {
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var fila Fila
	var lease sql.NullTime
	err = tx.QueryRowContext(ctx, `SELECT id,organization_id,fuente,payload,status,attempts,leased_until FROM webhook_event WHERE (status='pendiente' AND not_before<=?) OR (status='en_proceso' AND leased_until<=?) ORDER BY not_before,id LIMIT 1 FOR UPDATE SKIP LOCKED`, ahora, ahora).Scan(&fila.ID, &fila.OrganizacionID, &fila.Fuente, &fila.Payload, &fila.Estado, &fila.Intentos, &lease)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	estadoAnterior := fila.Estado
	fila.Intentos++
	hasta := ahora.Add(Lease)
	res, err := tx.ExecContext(ctx, `UPDATE webhook_event SET status='en_proceso',attempts=?,leased_until=?,updated_at=? WHERE id=? AND status=? AND attempts=?`, fila.Intentos, hasta, ahora, fila.ID, estadoAnterior, fila.Intentos-1)
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
	fila.Estado = "en_proceso"
	fila.LeaseHasta = &hasta
	return &fila, nil
}

func (w *Worker) limpiar(ctx context.Context) {
	if _, err := w.Limpiar(ctx, w.ahora()); err != nil {
		w.logger.Error("fallo al limpiar webhooks antiguos", "error", err)
	}
}

// Limpiar conserva siete dias los procesados y treinta dias los agotados.
func (w *Worker) Limpiar(ctx context.Context, ahora time.Time) (int64, error) {
	res, err := w.db.ExecContext(ctx, `DELETE FROM webhook_event WHERE (status='procesado' AND processed_at<?) OR (status='agotado' AND updated_at<?)`, ahora.Add(-7*24*time.Hour), ahora.Add(-30*24*time.Hour))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func EsperaReintento(intentos int) time.Duration {
	esperas := [...]time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute, time.Hour, 6 * time.Hour}
	if intentos < 1 {
		return esperas[0]
	}
	if intentos >= len(esperas) {
		return esperas[len(esperas)-1]
	}
	return esperas[intentos-1]
}

func recortar(valor string) string {
	valor = strings.TrimSpace(valor)
	runas := []rune(valor)
	if len(runas) > 500 {
		runas = runas[:500]
	}
	return string(runas)
}
