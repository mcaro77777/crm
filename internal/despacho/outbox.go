package despacho

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"crm-api/internal/identificadores"
)

const (
	Lease             = 180 * time.Second
	MaxIntentos       = 3
	VentanaAgrupacion = 6 * time.Second
)

type Fila struct {
	ID, OrganizacionID, ConversacionID, Estado string
	Intentos                                   int
	NoAntes, PrimerMensaje, UltimoMensaje      time.Time
	LeaseHasta                                 *time.Time
	MensajeIDs                                 []string
	UltimoError                                *string
}

type Repositorio struct{ db *sql.DB }

func NuevoRepositorio(db *sql.DB) *Repositorio { return &Repositorio{db: db} }

// Encolar crea o extiende el unico despacho vivo de la conversacion.
func (r *Repositorio) Encolar(ctx context.Context, organizacionID, conversacionID string, mensajes []string, ahora time.Time, inmediato bool) (Fila, error) {
	noAntes := ahora.Add(VentanaAgrupacion)
	if inmediato {
		noAntes = ahora
	}
	mensajes = unicos(mensajes)
	serializados, _ := json.Marshal(mensajes)

	for intento := 0; intento < 3; intento++ {
		tx, err := r.db.BeginTx(ctx, nil)
		if err != nil {
			return Fila{}, err
		}
		fila, err := buscarVivoTx(ctx, tx, organizacionID, conversacionID)
		if err == nil {
			if fila.Estado == "en_vuelo" {
				_, err = tx.ExecContext(ctx, `UPDATE dispatch SET message_ids=JSON_MERGE_PRESERVE(COALESCE(message_ids,JSON_ARRAY()),CAST(? AS JSON)),updated_at=? WHERE id=? AND status='en_vuelo'`, string(serializados), ahora, fila.ID)
			} else {
				_, err = tx.ExecContext(ctx, `UPDATE dispatch SET not_before=?,last_message_at=?,message_ids=JSON_MERGE_PRESERVE(COALESCE(message_ids,JSON_ARRAY()),CAST(? AS JSON)),updated_at=? WHERE id=? AND status='pendiente'`, noAntes, ahora, string(serializados), ahora, fila.ID)
			}
			if err == nil {
				err = tx.Commit()
			}
			if err != nil {
				_ = tx.Rollback()
				return Fila{}, err
			}
			fila.NoAntes = noAntes
			fila.MensajeIDs = unicos(append(fila.MensajeIDs, mensajes...))
			return fila, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			_ = tx.Rollback()
			return Fila{}, err
		}

		id, err := identificadores.Nuevo("dsp")
		if err != nil {
			_ = tx.Rollback()
			return Fila{}, err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO dispatch (id,organization_id,conversation_id,status,attempts,not_before,first_message_at,last_message_at,message_ids,created_at,updated_at) VALUES (?,?,?,'pendiente',0,?,?,?,?,?,?)`, id, organizacionID, conversacionID, noAntes, ahora, ahora, string(serializados), ahora, ahora)
		if err == nil {
			err = tx.Commit()
			if err == nil {
				return Fila{ID: id, OrganizacionID: organizacionID, ConversacionID: conversacionID, Estado: "pendiente", NoAntes: noAntes, PrimerMensaje: ahora, UltimoMensaje: ahora, MensajeIDs: mensajes}, nil
			}
		}
		_ = tx.Rollback()
		if !esDuplicado(err) {
			return Fila{}, err
		}
	}
	return Fila{}, errors.New("no se pudo asegurar un despacho vivo")
}

func buscarVivoTx(ctx context.Context, tx *sql.Tx, organizacionID, conversacionID string) (Fila, error) {
	row := tx.QueryRowContext(ctx, `SELECT id,organization_id,conversation_id,status,attempts,not_before,first_message_at,last_message_at,leased_until,message_ids,last_error FROM dispatch WHERE organization_id=? AND conversation_id=? AND status IN ('pendiente','en_vuelo') LIMIT 1 FOR UPDATE`, organizacionID, conversacionID)
	return escanearFila(row)
}

// TomarUno reclama atomica y exclusivamente un trabajo vencido.
func (r *Repositorio) TomarUno(ctx context.Context, ahora time.Time) (*Fila, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	row := tx.QueryRowContext(ctx, `SELECT id,organization_id,conversation_id,status,attempts,not_before,first_message_at,last_message_at,leased_until,message_ids,last_error FROM dispatch WHERE (status='pendiente' AND not_before<=?) OR (status='en_vuelo' AND leased_until<=?) ORDER BY not_before LIMIT 1 FOR UPDATE SKIP LOCKED`, ahora, ahora)
	fila, err := escanearFila(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	fila.Intentos++
	fila.Estado = "en_vuelo"
	hasta := ahora.Add(Lease)
	resultado, err := tx.ExecContext(ctx, `UPDATE dispatch SET status='en_vuelo',attempts=?,leased_until=?,updated_at=? WHERE id=? AND status=? AND attempts=?`, fila.Intentos, hasta, ahora, fila.ID, estadoAnterior(fila), fila.Intentos-1)
	if err != nil {
		return nil, err
	}
	n, _ := resultado.RowsAffected()
	if n != 1 {
		return nil, nil
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	fila.LeaseHasta = &hasta
	return &fila, nil
}

func estadoAnterior(f Fila) string {
	// Al reclamar se reemplazo Estado por en_vuelo. Un lease existente indica
	// que la fila ya estaba en vuelo; sin lease era pendiente.
	if f.LeaseHasta != nil {
		return "en_vuelo"
	}
	return "pendiente"
}

func (r *Repositorio) RenovarLease(ctx context.Context, f Fila, ahora time.Time) (bool, error) {
	res, err := r.db.ExecContext(ctx, `UPDATE dispatch SET leased_until=?,updated_at=? WHERE id=? AND status='en_vuelo' AND attempts=?`, ahora.Add(Lease), ahora, f.ID, f.Intentos)
	return unaFila(res, err)
}

func (r *Repositorio) MarcarEntregado(ctx context.Context, f Fila, ahora time.Time) (bool, error) {
	res, err := r.db.ExecContext(ctx, `UPDATE dispatch SET status='entregado',leased_until=NULL,last_error=NULL,updated_at=? WHERE id=? AND status='en_vuelo' AND attempts=?`, ahora, f.ID, f.Intentos)
	return unaFila(res, err)
}

// FinalizarEntregado bloquea la fila mientras lee lo anotado y la termina.
// Si un mensaje se anoto durante el POST, lo devuelve para abrir otro despacho.
// Si el finalizador gana primero el lock, Encolar vera la fila terminada y
// creara una nueva: en ambos ordenes el mensaje queda cubierto.
func (r *Repositorio) FinalizarEntregado(ctx context.Context, f Fila, entregados []string, ahora time.Time) ([]string, bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	var raw []byte
	err = tx.QueryRowContext(ctx, `SELECT message_ids FROM dispatch WHERE id=? AND organization_id=? AND status='en_vuelo' AND attempts=? FOR UPDATE`, f.ID, f.OrganizacionID, f.Intentos).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var anotados []string
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &anotados)
	}
	_, err = tx.ExecContext(ctx, `UPDATE dispatch SET status='entregado',leased_until=NULL,last_error=NULL,updated_at=? WHERE id=? AND status='en_vuelo' AND attempts=?`, ahora, f.ID, f.Intentos)
	if err != nil {
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	return diferencia(unicos(anotados), entregados), true, nil
}

func (r *Repositorio) Descartar(ctx context.Context, f Fila, detalle string, ahora time.Time) (bool, error) {
	res, err := r.db.ExecContext(ctx, `UPDATE dispatch SET status='descartado',leased_until=NULL,last_error=?,updated_at=? WHERE id=? AND status='en_vuelo' AND attempts=?`, recortar(detalle), ahora, f.ID, f.Intentos)
	return unaFila(res, err)
}

// ReintentarOCaducar devuelve true cuando el despacho quedo definitivamente
// caducado y la conversacion debe pasar a una persona.
func (r *Repositorio) ReintentarOCaducar(ctx context.Context, f Fila, detalle string, ahora time.Time) (bool, error) {
	if f.Intentos >= MaxIntentos {
		return r.Caducar(ctx, f, detalle, ahora)
	}
	espera := EsperaReintento(f.Intentos)
	res, err := r.db.ExecContext(ctx, `UPDATE dispatch SET status='pendiente',not_before=?,leased_until=NULL,last_error=?,updated_at=? WHERE id=? AND status='en_vuelo' AND attempts=?`, ahora.Add(espera), recortar(detalle), ahora, f.ID, f.Intentos)
	_, err = unaFila(res, err)
	return false, err
}

func (r *Repositorio) Caducar(ctx context.Context, f Fila, detalle string, ahora time.Time) (bool, error) {
	res, err := r.db.ExecContext(ctx, `UPDATE dispatch SET status='caducado_a_humano',leased_until=NULL,last_error=?,updated_at=? WHERE id=? AND status='en_vuelo' AND attempts=?`, recortar(detalle), ahora, f.ID, f.Intentos)
	return unaFila(res, err)
}

func EsperaReintento(intentos int) time.Duration {
	if intentos <= 1 {
		return time.Second
	}
	return 4 * time.Second
}

func (r *Repositorio) PasarAHumano(ctx context.Context, f Fila, ahora time.Time) (bool, error) {
	res, err := r.db.ExecContext(ctx, `UPDATE conversation SET ai_enabled=0,handoff_at=?,handoff_reason='error',updated_at=? WHERE id=? AND organization_id=? AND handoff_at IS NULL`, ahora, ahora, f.ConversacionID, f.OrganizacionID)
	return unaFila(res, err)
}

type scanner interface{ Scan(...any) error }

func escanearFila(row scanner) (Fila, error) {
	var f Fila
	var lease sql.NullTime
	var raw []byte
	var ultimo sql.NullString
	err := row.Scan(&f.ID, &f.OrganizacionID, &f.ConversacionID, &f.Estado, &f.Intentos, &f.NoAntes, &f.PrimerMensaje, &f.UltimoMensaje, &lease, &raw, &ultimo)
	if err != nil {
		return f, err
	}
	if lease.Valid {
		f.LeaseHasta = &lease.Time
	}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &f.MensajeIDs)
	}
	f.MensajeIDs = unicos(f.MensajeIDs)
	if ultimo.Valid {
		f.UltimoError = &ultimo.String
	}
	return f, nil
}

func unaFila(res sql.Result, err error) (bool, error) {
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

func unicos(ids []string) []string {
	resultado := make([]string, 0, len(ids))
	vistos := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, existe := vistos[id]; existe {
			continue
		}
		vistos[id] = struct{}{}
		resultado = append(resultado, id)
	}
	return resultado
}

func diferencia(candidatos, cubiertos []string) []string {
	vistos := make(map[string]struct{}, len(cubiertos))
	for _, id := range cubiertos {
		vistos[id] = struct{}{}
	}
	resultado := make([]string, 0)
	for _, id := range unicos(candidatos) {
		if _, existe := vistos[id]; !existe {
			resultado = append(resultado, id)
		}
	}
	return resultado
}

func recortar(valor string) string {
	runas := []rune(valor)
	if len(runas) > 300 {
		runas = runas[:300]
	}
	return string(runas)
}

func esDuplicado(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "1062") || strings.Contains(strings.ToLower(err.Error()), "duplicate")
}
