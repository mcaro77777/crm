package httpapi

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"crm-api/internal/eventos"
)

type conversacionDTO struct {
	ID                 string         `json:"id"`
	ContactoID         string         `json:"contactoId"`
	Nombre             string         `json:"nombre"`
	Canal              string         `json:"canal"`
	Identidad          string         `json:"identidad"`
	IAActiva           bool           `json:"iaActiva"`
	HandoffEn          sql.NullTime   `json:"-"`
	HandoffJSON        *time.Time     `json:"handoffEn"`
	HandoffMotivo      sql.NullString `json:"-"`
	HandoffMotivoJSON  *string        `json:"handoffMotivo"`
	UltimoEntrante     sql.NullTime   `json:"-"`
	UltimoEntranteJSON *time.Time     `json:"ultimoEntrante"`
	UltimoMensaje      sql.NullTime   `json:"-"`
	UltimoMensajeJSON  *time.Time     `json:"ultimoMensaje"`
	NoLeidos           int            `json:"noLeidos"`
	CreadoEn           time.Time      `json:"creadoEn"`
	ResponsableID      sql.NullString `json:"-"`
	ResponsableIDJSON  *string        `json:"responsableId"`
	Responsable        sql.NullString `json:"-"`
	ResponsableJSON    *string        `json:"responsable"`
	Fuente             sql.NullString `json:"-"`
	FuenteJSON         *string        `json:"fuente"`
	VentanaExpira      sql.NullTime   `json:"-"`
	VentanaExpiraJSON  *time.Time     `json:"ventanaExpiraEn"`
	PorVencer          bool           `json:"porVencer"`
}

func (c *conversacionDTO) normalizar() {
	if c.HandoffEn.Valid {
		v := c.HandoffEn.Time
		c.HandoffJSON = &v
	}
	if c.HandoffMotivo.Valid {
		v := c.HandoffMotivo.String
		c.HandoffMotivoJSON = &v
	}
	if c.UltimoEntrante.Valid {
		v := c.UltimoEntrante.Time
		c.UltimoEntranteJSON = &v
	}
	if c.UltimoMensaje.Valid {
		v := c.UltimoMensaje.Time
		c.UltimoMensajeJSON = &v
	}
	if c.ResponsableID.Valid {
		v := c.ResponsableID.String
		c.ResponsableIDJSON = &v
	}
	if c.Responsable.Valid {
		v := c.Responsable.String
		c.ResponsableJSON = &v
	}
	if c.Fuente.Valid {
		v := c.Fuente.String
		c.FuenteJSON = &v
	}
	if c.VentanaExpira.Valid {
		v := c.VentanaExpira.Time
		c.VentanaExpiraJSON = &v
	}
}

func (s *Servidor) listarConversaciones(w http.ResponseWriter, r *http.Request) {
	sesion := sesionDe(r.Context())
	limite := numeroConsulta(r, "limit", 50, 1, 200)
	desde := numeroConsulta(r, "offset", 0, 0, 100000)
	venceSQL := `DATE_ADD(cv.last_inbound_at,INTERVAL (CASE WHEN cv.channel='tiktok' THEN 48 ELSE 24 END) HOUR)`
	consulta := "SELECT cv.id,cv.contact_id,c.name,cv.channel,c.wa_identity,cv.ai_enabled,cv.handoff_at,cv.handoff_reason,cv.last_inbound_at,cv.last_message_at,cv.unread_count,cv.created_at,ca.user_id,u.name,c.source," + venceSQL + ",(" + venceSQL + " BETWEEN UTC_TIMESTAMP(3) AND DATE_ADD(UTC_TIMESTAMP(3),INTERVAL 2 HOUR)) FROM conversation cv JOIN contact c ON c.id=cv.contact_id AND c.organization_id=cv.organization_id LEFT JOIN conversation_assignment ca ON ca.organization_id=cv.organization_id AND ca.conversation_id=cv.id LEFT JOIN user u ON u.id=ca.user_id WHERE cv.organization_id=? AND cv.is_test=0"
	args := []any{sesion.OrganizacionID}
	if canal := strings.TrimSpace(r.URL.Query().Get("canal")); canal != "" {
		if !canalValido(canal) {
			responderError(w, 422, "validation_error", "Canal invalido", nil)
			return
		}
		consulta += " AND cv.channel=?"
		args = append(args, canal)
	}
	if fuente := strings.TrimSpace(r.URL.Query().Get("fuente")); fuente != "" {
		if len(fuente) > 64 {
			responderError(w, 422, "validation_error", "Fuente invalida", nil)
			return
		}
		consulta += " AND COALESCE(c.source,'')=?"
		args = append(args, fuente)
	}
	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		consulta += " AND (c.name LIKE ? OR c.wa_identity LIKE ?)"
		args = append(args, "%"+q+"%", "%"+q+"%")
	}
	responsable := strings.TrimSpace(r.URL.Query().Get("responsable"))
	vista := strings.TrimSpace(r.URL.Query().Get("vista"))
	if vista == "sin_asignar" || responsable == "sin_asignar" {
		consulta += " AND ca.user_id IS NULL"
	} else if responsable == "yo" {
		consulta += " AND ca.user_id=?"
		args = append(args, sesion.UsuarioID)
	} else if responsable != "" {
		consulta += " AND ca.user_id=?"
		args = append(args, responsable)
	}
	if vista == "por_vencer" || r.URL.Query().Get("vencimiento") == "por_vencer" {
		consulta += " AND " + venceSQL + " BETWEEN UTC_TIMESTAMP(3) AND DATE_ADD(UTC_TIMESTAMP(3),INTERVAL 2 HOUR)"
	}
	if valor := r.URL.Query().Get("desde"); valor != "" {
		t, err := time.Parse(time.RFC3339, valor)
		if err != nil {
			responderError(w, 422, "validation_error", "Fecha desde invalida", nil)
			return
		}
		consulta += " AND COALESCE(cv.last_message_at,cv.created_at)>=?"
		args = append(args, t.UTC())
	}
	if valor := r.URL.Query().Get("hasta"); valor != "" {
		t, err := time.Parse(time.RFC3339, valor)
		if err != nil {
			responderError(w, 422, "validation_error", "Fecha hasta invalida", nil)
			return
		}
		consulta += " AND COALESCE(cv.last_message_at,cv.created_at)<?"
		args = append(args, t.UTC())
	}
	consulta += " ORDER BY COALESCE(cv.last_message_at,cv.created_at) DESC LIMIT ? OFFSET ?"
	args = append(args, limite, desde)
	rows, err := s.db.QueryContext(r.Context(), consulta, args...)
	if err != nil {
		s.errorInterno(w, "listar conversaciones", err)
		return
	}
	defer rows.Close()
	items := make([]conversacionDTO, 0)
	for rows.Next() {
		var c conversacionDTO
		if err := rows.Scan(&c.ID, &c.ContactoID, &c.Nombre, &c.Canal, &c.Identidad, &c.IAActiva, &c.HandoffEn, &c.HandoffMotivo, &c.UltimoEntrante, &c.UltimoMensaje, &c.NoLeidos, &c.CreadoEn, &c.ResponsableID, &c.Responsable, &c.Fuente, &c.VentanaExpira, &c.PorVencer); err != nil {
			s.errorInterno(w, "leer conversaciones", err)
			return
		}
		c.normalizar()
		items = append(items, c)
	}
	if err := rows.Err(); err != nil {
		s.errorInterno(w, "recorrer conversaciones", err)
		return
	}
	responderJSON(w, 200, map[string]any{"items": items, "limit": limite, "offset": desde})
}

func (s *Servidor) obtenerConversacion(w http.ResponseWriter, r *http.Request) {
	sesion := sesionDe(r.Context())
	var c conversacionDTO
	err := s.db.QueryRowContext(r.Context(), "SELECT cv.id,cv.contact_id,c.name,cv.channel,c.wa_identity,cv.ai_enabled,cv.handoff_at,cv.handoff_reason,cv.last_inbound_at,cv.last_message_at,cv.unread_count,cv.created_at FROM `conversation` cv JOIN `contact` c ON c.id=cv.contact_id AND c.organization_id=cv.organization_id WHERE cv.organization_id=? AND cv.id=?", sesion.OrganizacionID, r.PathValue("id")).Scan(&c.ID, &c.ContactoID, &c.Nombre, &c.Canal, &c.Identidad, &c.IAActiva, &c.HandoffEn, &c.HandoffMotivo, &c.UltimoEntrante, &c.UltimoMensaje, &c.NoLeidos, &c.CreadoEn)
	if err != nil {
		s.errorInterno(w, "obtener conversacion", err)
		return
	}
	c.normalizar()
	responderJSON(w, 200, c)
}

type mensajeDTO struct {
	ID          string         `json:"id"`
	Direccion   string         `json:"direccion"`
	Tipo        string         `json:"tipo"`
	Texto       sql.NullString `json:"-"`
	TextoJSON   *string        `json:"texto"`
	Estado      string         `json:"estado"`
	GeneradoIA  bool           `json:"generadoIA"`
	Origen      string         `json:"origen"`
	AdjuntoID   sql.NullString `json:"-"`
	AdjuntoJSON *string        `json:"adjuntoId"`
	CreadoEn    time.Time      `json:"creadoEn"`
}

func (m *mensajeDTO) normalizar() {
	if m.Texto.Valid {
		v := m.Texto.String
		m.TextoJSON = &v
	}
	if m.AdjuntoID.Valid {
		v := m.AdjuntoID.String
		m.AdjuntoJSON = &v
	}
}
func (s *Servidor) listarMensajes(w http.ResponseWriter, r *http.Request) {
	sesion := sesionDe(r.Context())
	limite := numeroConsulta(r, "limit", 100, 1, 500)
	rows, err := s.db.QueryContext(r.Context(), "SELECT m.id,m.direction,m.type,m.text,m.status,m.ai_generated,m.origin,m.media_asset_id,m.created_at FROM `message` m JOIN `conversation` cv ON cv.id=m.conversation_id AND cv.organization_id=m.organization_id WHERE m.organization_id=? AND m.conversation_id=? ORDER BY m.created_at DESC LIMIT ?", sesion.OrganizacionID, r.PathValue("id"), limite)
	if err != nil {
		s.errorInterno(w, "listar mensajes", err)
		return
	}
	defer rows.Close()
	items := make([]mensajeDTO, 0)
	for rows.Next() {
		var m mensajeDTO
		if err := rows.Scan(&m.ID, &m.Direccion, &m.Tipo, &m.Texto, &m.Estado, &m.GeneradoIA, &m.Origen, &m.AdjuntoID, &m.CreadoEn); err != nil {
			s.errorInterno(w, "leer mensajes", err)
			return
		}
		m.normalizar()
		items = append(items, m)
	}
	if err := rows.Err(); err != nil {
		s.errorInterno(w, "recorrer mensajes", err)
		return
	}
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
	responderJSON(w, 200, map[string]any{"items": items})
}

func (s *Servidor) eventosSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		responderError(w, 500, "stream_unsupported", "El servidor no admite streaming", nil)
		return
	}
	sesion := sesionDe(r.Context())
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	desdeTexto := strings.TrimSpace(r.URL.Query().Get("since"))
	if desdeTexto == "" {
		desdeTexto = strings.TrimSpace(r.Header.Get("Last-Event-ID"))
	}
	desde, err := strconv.ParseInt(desdeTexto, 10, 64)
	recuperar := desdeTexto != "" && err == nil && desde >= 0
	var canal <-chan eventos.Evento
	var pendientes []eventos.Evento
	var perdido bool
	var cancelar func()
	if recuperar {
		canal, pendientes, perdido, cancelar = s.bus.SuscribirDesde(sesion.OrganizacionID, desde)
	} else {
		canal, cancelar = s.bus.Suscribir(sesion.OrganizacionID)
	}
	defer cancelar()
	_, _ = fmt.Fprint(w, ": conectado\n\n")
	if perdido {
		_, _ = fmt.Fprint(w, "event: sync.required\ndata: {\"reason\":\"history_expired\"}\n\n")
	} else {
		for _, evento := range pendientes {
			escribirEventoSSE(w, evento)
		}
	}
	flusher.Flush()
	latido := time.NewTicker(25 * time.Second)
	defer latido.Stop()
	for {
		select {
		case evento := <-canal:
			escribirEventoSSE(w, evento)
			flusher.Flush()
		case <-latido.C:
			_, _ = fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func escribirEventoSSE(w http.ResponseWriter, evento eventos.Evento) {
	dato, err := json.Marshal(evento.Dato)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", evento.ID, evento.Tipo, dato)
}

// Hijack se conserva para que el wrapper HTTP no quite capacidades del servidor.
func (w *estadoWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	return h.Hijack()
}
