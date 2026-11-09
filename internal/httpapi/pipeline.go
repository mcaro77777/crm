package httpapi

import (
	"database/sql"
	"net/http"
	"strings"
	"time"

	"crm-api/internal/identificadores"
)

type etapaDTO struct {
	ID       string `json:"id"`
	Nombre   string `json:"nombre"`
	Posicion int    `json:"posicion"`
	Tipo     string `json:"tipo"`
}
type leadDTO struct {
	ID                  string         `json:"id"`
	ContactoID          string         `json:"contactoId"`
	EtapaID             string         `json:"etapaId"`
	Posicion            int            `json:"posicion"`
	Nombre              string         `json:"nombre"`
	Canal               string         `json:"canal"`
	Prioridad           sql.NullString `json:"-"`
	PrioridadJSON       *string        `json:"prioridad"`
	Monto               sql.NullInt64  `json:"-"`
	MontoJSON           *int64         `json:"montoCentavos"`
	Moneda              sql.NullString `json:"-"`
	MonedaJSON          *string        `json:"moneda"`
	UltimaActividad     sql.NullTime   `json:"-"`
	UltimaActividadJSON *time.Time     `json:"ultimaActividad"`
}

func (l *leadDTO) normalizar() {
	if l.Prioridad.Valid {
		v := l.Prioridad.String
		l.PrioridadJSON = &v
	}
	if l.Monto.Valid {
		v := l.Monto.Int64
		l.MontoJSON = &v
	}
	if l.Moneda.Valid {
		v := l.Moneda.String
		l.MonedaJSON = &v
	}
	if l.UltimaActividad.Valid {
		v := l.UltimaActividad.Time
		l.UltimaActividadJSON = &v
	}
}

func (s *Servidor) obtenerPipeline(w http.ResponseWriter, r *http.Request) {
	sesion := sesionDe(r.Context())
	rows, err := s.db.QueryContext(r.Context(), "SELECT id,name,position,kind FROM `pipeline_stage` WHERE organization_id=? ORDER BY position", sesion.OrganizacionID)
	if err != nil {
		s.errorInterno(w, "listar etapas", err)
		return
	}
	defer rows.Close()
	etapas := make([]etapaDTO, 0)
	for rows.Next() {
		var e etapaDTO
		if err := rows.Scan(&e.ID, &e.Nombre, &e.Posicion, &e.Tipo); err != nil {
			s.errorInterno(w, "leer etapas", err)
			return
		}
		etapas = append(etapas, e)
	}
	if err := rows.Err(); err != nil {
		s.errorInterno(w, "recorrer etapas", err)
		return
	}
	leadRows, err := s.db.QueryContext(r.Context(), "SELECT l.id,l.contact_id,l.stage_id,l.position,c.name,c.channel,l.priority,l.amount_cents,l.currency,l.last_activity_at FROM `lead` l JOIN `contact` c ON c.id=l.contact_id AND c.organization_id=l.organization_id WHERE l.organization_id=? AND c.archived_at IS NULL ORDER BY l.stage_id,l.position,l.updated_at DESC", sesion.OrganizacionID)
	if err != nil {
		s.errorInterno(w, "listar leads", err)
		return
	}
	defer leadRows.Close()
	leads := make([]leadDTO, 0)
	for leadRows.Next() {
		var l leadDTO
		if err := leadRows.Scan(&l.ID, &l.ContactoID, &l.EtapaID, &l.Posicion, &l.Nombre, &l.Canal, &l.Prioridad, &l.Monto, &l.Moneda, &l.UltimaActividad); err != nil {
			s.errorInterno(w, "leer leads", err)
			return
		}
		l.normalizar()
		leads = append(leads, l)
	}
	if err := leadRows.Err(); err != nil {
		s.errorInterno(w, "recorrer leads", err)
		return
	}
	responderJSON(w, http.StatusOK, map[string]any{"etapas": etapas, "leads": leads})
}

type moverLeadBody struct {
	EtapaID       string  `json:"etapaId"`
	MotivoPerdida *string `json:"motivoPerdida"`
	NotaPerdida   *string `json:"notaPerdida"`
}

func (s *Servidor) moverLead(w http.ResponseWriter, r *http.Request) {
	sesion := sesionDe(r.Context())
	var entrada moverLeadBody
	if !decodificarJSON(w, r, &entrada) {
		return
	}
	entrada.EtapaID = strings.TrimSpace(entrada.EtapaID)
	if entrada.EtapaID == "" {
		responderError(w, 422, "validation_error", "etapaId es obligatorio", nil)
		return
	}
	tx, err := s.db.BeginTx(r.Context(), &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		s.errorInterno(w, "iniciar movimiento", err)
		return
	}
	defer tx.Rollback()
	var contactoID, etapaAnteriorID, etapaAnteriorNombre string
	err = tx.QueryRowContext(r.Context(), "SELECT l.contact_id,l.stage_id,ps.name FROM `lead` l JOIN `pipeline_stage` ps ON ps.id=l.stage_id AND ps.organization_id=l.organization_id WHERE l.organization_id=? AND l.id=? FOR UPDATE", sesion.OrganizacionID, r.PathValue("id")).Scan(&contactoID, &etapaAnteriorID, &etapaAnteriorNombre)
	if err != nil {
		s.errorInterno(w, "buscar lead", err)
		return
	}
	var nuevaNombre, nuevoTipo string
	err = tx.QueryRowContext(r.Context(), "SELECT name,kind FROM `pipeline_stage` WHERE organization_id=? AND id=?", sesion.OrganizacionID, entrada.EtapaID).Scan(&nuevaNombre, &nuevoTipo)
	if err != nil {
		s.errorInterno(w, "buscar etapa destino", err)
		return
	}
	if nuevoTipo == "lost" && (entrada.MotivoPerdida == nil || strings.TrimSpace(*entrada.MotivoPerdida) == "") {
		responderError(w, 422, "loss_reason_required", "Debe indicar el motivo de perdida", nil)
		return
	}
	if entrada.MotivoPerdida != nil && !motivoPerdidaValido(*entrada.MotivoPerdida) {
		responderError(w, 422, "validation_error", "Motivo de perdida invalido", nil)
		return
	}
	ahora := time.Now().UTC()
	if _, err = tx.ExecContext(r.Context(), "UPDATE `lead` SET stage_id=?,last_activity_at=?,updated_at=? WHERE organization_id=? AND id=?", entrada.EtapaID, ahora, ahora, sesion.OrganizacionID, r.PathValue("id")); err != nil {
		s.errorInterno(w, "actualizar etapa", err)
		return
	}
	eventoID, _ := identificadores.Nuevo("lse")
	_, err = tx.ExecContext(r.Context(), "INSERT INTO `lead_stage_event` (id,organization_id,lead_id,contact_id,from_stage_id,from_stage_name,to_stage_id,to_stage_name,to_stage_kind,occurred_at,actor_user_id,source,loss_reason,loss_note,created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,'dueno',?,?,?)", eventoID, sesion.OrganizacionID, r.PathValue("id"), contactoID, etapaAnteriorID, etapaAnteriorNombre, entrada.EtapaID, nuevaNombre, nuevoTipo, ahora, sesion.UsuarioID, entrada.MotivoPerdida, entrada.NotaPerdida, ahora)
	if err != nil {
		s.errorInterno(w, "registrar movimiento", err)
		return
	}
	if err = tx.Commit(); err != nil {
		s.errorInterno(w, "confirmar movimiento", err)
		return
	}
	s.bus.Publicar(sesion.OrganizacionID, "lead.stage", map[string]string{"leadId": r.PathValue("id"), "stageId": entrada.EtapaID})
	responderJSON(w, http.StatusOK, map[string]string{"id": r.PathValue("id"), "etapaId": entrada.EtapaID})
}

func motivoPerdidaValido(motivo string) bool {
	switch motivo {
	case "precio", "no_es_perfil", "sin_presupuesto", "eligio_otro", "nunca_contesto", "otro":
		return true
	default:
		return false
	}
}
