package handoff

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

var (
	ErrConversacionNoEncontrada = errors.New("conversacion no encontrada")
	ErrMotivoInvalido           = errors.New("motivo de handoff invalido")
	ErrContactoPausado          = errors.New("el contacto tiene el agente pausado")
	ErrAgenteApagado            = errors.New("el agente de la organizacion esta apagado")
)

var motivos = map[string]bool{
	"cliente": true, "modelo": true, "error": true, "ventana": true,
	"hostilidad": true, "manual_reply": true,
}

type Estado struct {
	IAHabilitada bool
	HandoffEn    *time.Time
	Motivo       *string
}

type Servicio struct{ db *sql.DB }

func Nuevo(db *sql.DB) *Servicio { return &Servicio{db: db} }

// Tomar pasa la conversación a una persona y descarta cualquier trabajo vivo.
// Es idempotente: repetir el mismo handoff conserva la primera fecha y motivo.
func (s *Servicio) Tomar(ctx context.Context, organizacionID, conversacionID, motivo string, ahora time.Time) (Estado, bool, error) {
	if !motivos[motivo] {
		return Estado{}, false, ErrMotivoInvalido
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Estado{}, false, err
	}
	defer tx.Rollback()
	var ia bool
	var fecha sql.NullTime
	var motivoActual sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT ai_enabled,handoff_at,handoff_reason FROM conversation WHERE organization_id=? AND id=? FOR UPDATE`, organizacionID, conversacionID).Scan(&ia, &fecha, &motivoActual)
	if errors.Is(err, sql.ErrNoRows) {
		return Estado{}, false, ErrConversacionNoEncontrada
	}
	if err != nil {
		return Estado{}, false, err
	}
	cambio := ia || !fecha.Valid
	if !fecha.Valid {
		fecha = sql.NullTime{Time: ahora, Valid: true}
	}
	if !motivoActual.Valid {
		motivoActual = sql.NullString{String: motivo, Valid: true}
		cambio = true
	}
	if _, err = tx.ExecContext(ctx, `UPDATE conversation SET ai_enabled=0,handoff_at=?,handoff_reason=?,updated_at=? WHERE organization_id=? AND id=?`, fecha.Time, motivoActual.String, ahora, organizacionID, conversacionID); err != nil {
		return Estado{}, false, err
	}
	resultado, err := tx.ExecContext(ctx, `UPDATE dispatch SET status='descartado',leased_until=NULL,last_error=?,updated_at=? WHERE organization_id=? AND conversation_id=? AND status IN ('pendiente','en_vuelo')`, "handoff: "+motivoActual.String, ahora, organizacionID, conversacionID)
	if err != nil {
		return Estado{}, false, err
	}
	if n, _ := resultado.RowsAffected(); n > 0 {
		cambio = true
	}
	if err := tx.Commit(); err != nil {
		return Estado{}, false, err
	}
	fechaJSON, motivoJSON := fecha.Time.UTC(), motivoActual.String
	return Estado{IAHabilitada: false, HandoffEn: &fechaJSON, Motivo: &motivoJSON}, cambio, nil
}

// Reactivar devuelve la conversación al agente. La salud del cerebro se
// comprueba en la frontera HTTP antes de entrar aquí; este método preserva los
// invariantes transaccionales de organización, perfil y contacto.
func (s *Servicio) Reactivar(ctx context.Context, organizacionID, conversacionID string, ahora time.Time) (Estado, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Estado{}, false, err
	}
	defer tx.Rollback()
	var ia, contactoPausado bool
	var fecha sql.NullTime
	err = tx.QueryRowContext(ctx, `SELECT cv.ai_enabled,cv.handoff_at,c.ai_paused FROM conversation cv JOIN contact c ON c.id=cv.contact_id AND c.organization_id=cv.organization_id WHERE cv.organization_id=? AND cv.id=? FOR UPDATE`, organizacionID, conversacionID).Scan(&ia, &fecha, &contactoPausado)
	if errors.Is(err, sql.ErrNoRows) {
		return Estado{}, false, ErrConversacionNoEncontrada
	}
	if err != nil {
		return Estado{}, false, err
	}
	if contactoPausado {
		return Estado{}, false, ErrContactoPausado
	}
	var agenteHabilitado bool
	if err := tx.QueryRowContext(ctx, `SELECT enabled FROM agent_profile WHERE organization_id=?`, organizacionID).Scan(&agenteHabilitado); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Estado{}, false, ErrAgenteApagado
		}
		return Estado{}, false, err
	}
	if !agenteHabilitado {
		return Estado{}, false, ErrAgenteApagado
	}
	resultado, err := tx.ExecContext(ctx, `UPDATE conversation SET ai_enabled=1,handoff_at=NULL,handoff_reason=NULL,updated_at=? WHERE organization_id=? AND id=?`, ahora, organizacionID, conversacionID)
	if err != nil {
		return Estado{}, false, err
	}
	if _, err := resultado.RowsAffected(); err != nil {
		return Estado{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return Estado{}, false, err
	}
	return Estado{IAHabilitada: true}, !ia || fecha.Valid, nil
}
