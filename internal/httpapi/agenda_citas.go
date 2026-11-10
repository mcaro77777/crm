package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"crm-api/internal/agenda"
	"crm-api/internal/identificadores"
)

type citaDTO struct {
	ID, Tipo, Estado, Fuente, Conector string
	ContactoID, ConversacionID, LeadID *string
	Contacto                           string
	ProgramadaEn                       time.Time
	DuracionMinutos                    int
	ReferenciaExterna, Enlace, Notas   *string
	EnlacePendiente, Prueba            bool
	CreadaEn, ActualizadaEn            time.Time
}

func (c citaDTO) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{"id": c.ID, "tipo": c.Tipo, "estado": c.Estado, "fuente": c.Fuente, "conector": c.Conector, "contactoId": c.ContactoID, "conversacionId": c.ConversacionID, "leadId": c.LeadID, "contacto": c.Contacto, "programadaEn": c.ProgramadaEn, "duracionMinutos": c.DuracionMinutos, "referenciaExterna": c.ReferenciaExterna, "enlace": c.Enlace, "notas": c.Notas, "enlacePendiente": c.EnlacePendiente, "prueba": c.Prueba, "creadaEn": c.CreadaEn, "actualizadaEn": c.ActualizadaEn})
}

func (s *Servidor) listarCitas(w http.ResponseWriter, r *http.Request) {
	ses := sesionDe(r.Context())
	desde := time.Now().UTC().AddDate(0, -1, 0)
	hasta := time.Now().UTC().AddDate(0, 6, 0)
	if v := r.URL.Query().Get("desde"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			desde = t.UTC()
		}
	}
	if v := r.URL.Query().Get("hasta"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			hasta = t.UTC()
		}
	}
	if !hasta.After(desde) || hasta.Sub(desde) > 366*24*time.Hour {
		responderError(w, 422, "validation_error", "Rango de fechas invalido", nil)
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `SELECT b.id,b.kind,b.status,b.source,b.contact_id,b.conversation_id,b.lead_id,COALESCE(c.name,'Bloqueo'),b.scheduled_at,b.duration_minutes,COALESCE(b.connector,''),b.external_ref,b.meeting_link,b.link_pending,b.is_test,b.notes,b.created_at,b.updated_at FROM booking b LEFT JOIN contact c ON c.id=b.contact_id AND c.organization_id=b.organization_id WHERE b.organization_id=? AND b.scheduled_at>=? AND b.scheduled_at<? ORDER BY b.scheduled_at`, ses.OrganizacionID, desde, hasta)
	if err != nil {
		s.errorInterno(w, "listar citas", err)
		return
	}
	defer rows.Close()
	items := make([]citaDTO, 0)
	for rows.Next() {
		c, err := escanearCita(rows)
		if err != nil {
			s.errorInterno(w, "leer cita", err)
			return
		}
		items = append(items, c)
	}
	responderJSON(w, 200, map[string]any{"items": items, "zonaHoraria": func() string { d, _ := s.leerAjustesAgenda(r.Context(), ses.OrganizacionID); return d.ZonaHoraria }()})
}

type scannerCita interface{ Scan(...any) error }

func escanearCita(row scannerCita) (citaDTO, error) {
	var c citaDTO
	var contactoID, convID, leadID, ref, enlace, notas sql.NullString
	err := row.Scan(&c.ID, &c.Tipo, &c.Estado, &c.Fuente, &contactoID, &convID, &leadID, &c.Contacto, &c.ProgramadaEn, &c.DuracionMinutos, &c.Conector, &ref, &enlace, &c.EnlacePendiente, &c.Prueba, &notas, &c.CreadaEn, &c.ActualizadaEn)
	c.ContactoID = ptrNulo(contactoID)
	c.ConversacionID = ptrNulo(convID)
	c.LeadID = ptrNulo(leadID)
	c.ReferenciaExterna = ptrNulo(ref)
	c.Enlace = ptrNulo(enlace)
	c.Notas = ptrNulo(notas)
	return c, err
}
func ptrNulo(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	x := v.String
	return &x
}

type entradaCita struct {
	ContactoID      string `json:"contactoId"`
	ConversacionID  string `json:"conversacionId"`
	ProgramadaEn    string `json:"programadaEn"`
	DuracionMinutos int    `json:"duracionMinutos"`
	Notas           string `json:"notas"`
	Tipo            string `json:"tipo"`
}

func (s *Servidor) crearCitaManual(w http.ResponseWriter, r *http.Request) {
	ses := sesionDe(r.Context())
	var e entradaCita
	if !decodificarJSON(w, r, &e) {
		return
	}
	inicio, err := time.Parse(time.RFC3339, e.ProgramadaEn)
	if err != nil {
		responderError(w, 422, "validation_error", "programadaEn debe usar RFC3339", nil)
		return
	}
	if e.Tipo == "" {
		e.Tipo = "session"
	}
	if e.Tipo != "session" && e.Tipo != "block" {
		responderError(w, 422, "validation_error", "tipo invalido", nil)
		return
	}
	contacto := strings.TrimSpace(e.ContactoID)
	conv := strings.TrimSpace(e.ConversacionID)
	if e.Tipo == "session" && contacto == "" && conv == "" {
		responderError(w, 422, "validation_error", "Selecciona un contacto o conversacion", nil)
		return
	}
	cita, err := s.crearReservaAgenda(r.Context(), ses.OrganizacionID, contacto, conv, inicio.UTC(), e.DuracionMinutos, e.Notas, e.Tipo, "manual", false)
	if err != nil {
		s.responderErrorAgenda(w, err)
		return
	}
	responderJSON(w, 201, cita)
}

type errorAgenda struct {
	estado          int
	codigo, mensaje string
}

func (e *errorAgenda) Error() string { return e.mensaje }
func (s *Servidor) responderErrorAgenda(w http.ResponseWriter, err error) {
	var e *errorAgenda
	if errors.As(err, &e) {
		responderError(w, e.estado, e.codigo, e.mensaje, nil)
		return
	}
	s.errorInterno(w, "operacion de agenda", err)
}

func (s *Servidor) crearReservaAgenda(ctx context.Context, org, contactoID, convID string, inicio time.Time, duracion int, notas, tipo, fuente string, prueba bool) (citaDTO, error) {
	ajustes, err := s.leerAjustesAgenda(ctx, org)
	if err != nil || !ajustes.Configurada {
		return citaDTO{}, &errorAgenda{409, "agenda_not_configured", "Configura la agenda antes de reservar"}
	}
	if duracion == 0 {
		duracion = ajustes.DuracionMinutos
	}
	if duracion < 5 || duracion > 480 {
		return citaDTO{}, &errorAgenda{422, "validation_error", "Duracion invalida"}
	}
	var nombre = "Bloqueo"
	var lead sql.NullString
	if convID != "" {
		const consulta = `SELECT cv.contact_id,cv.is_test,c.name,(SELECT id FROM ` + "`lead`" + ` WHERE organization_id=cv.organization_id AND contact_id=cv.contact_id LIMIT 1) FROM conversation cv JOIN contact c ON c.id=cv.contact_id AND c.organization_id=cv.organization_id WHERE cv.organization_id=? AND cv.id=?`
		var cID string
		var esPrueba bool
		err = s.db.QueryRowContext(ctx, consulta, org, convID).Scan(&cID, &esPrueba, &nombre, &lead)
		if err != nil {
			return citaDTO{}, &errorAgenda{404, "not_found", "Conversacion no encontrada"}
		}
		contactoID = cID
		prueba = esPrueba
	} else if contactoID != "" {
		const consulta = `SELECT name,(SELECT id FROM ` + "`lead`" + ` WHERE organization_id=? AND contact_id=? LIMIT 1) FROM contact WHERE organization_id=? AND id=? AND archived_at IS NULL`
		if err = s.db.QueryRowContext(ctx, consulta, org, contactoID, org, contactoID).Scan(&nombre, &lead); err != nil {
			return citaDTO{}, &errorAgenda{404, "not_found", "Contacto no encontrado"}
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return citaDTO{}, err
	}
	defer tx.Rollback()
	var lock string
	if err = tx.QueryRowContext(ctx, `SELECT id FROM calendar_settings WHERE organization_id=? FOR UPDATE`, org).Scan(&lock); err != nil {
		return citaDTO{}, err
	}
	fin := inicio.Add(time.Duration(duracion) * time.Minute)
	var ocupada int
	err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM booking WHERE organization_id=? AND is_test=? AND status IN ('agendada','realizada') AND scheduled_at<? AND DATE_ADD(scheduled_at,INTERVAL duration_minutes MINUTE)>?`, org, prueba, fin, inicio).Scan(&ocupada)
	if err != nil {
		return citaDTO{}, err
	}
	if ocupada > 0 {
		return citaDTO{}, &errorAgenda{409, "slot_unavailable", "El horario ya no esta disponible"}
	}
	id, _ := identificadores.Nuevo("bk")
	ahora := time.Now().UTC()
	var contactoArg, convArg, leadArg any
	if contactoID != "" {
		contactoArg = contactoID
	}
	if convID != "" {
		convArg = convID
	}
	if lead.Valid {
		leadArg = lead.String
	}
	conector := ajustes.Conector
	_, err = tx.ExecContext(ctx, `INSERT INTO booking(id,organization_id,kind,status,source,contact_id,conversation_id,lead_id,scheduled_at,duration_minutes,connector,link_pending,is_test,notes,created_at,updated_at) VALUES (?,? ,?,'agendada',?,?,?,?,?,?,?,?,?,?,?,?)`, id, org, tipo, fuente, contactoArg, convArg, leadArg, inicio, duracion, conector, !prueba, prueba, nuloVacio(strings.TrimSpace(notas)), ahora, ahora)
	if err != nil {
		if esDuplicadoMySQL(err) {
			return citaDTO{}, &errorAgenda{409, "slot_unavailable", "El horario ya no esta disponible"}
		}
		return citaDTO{}, err
	}
	if err = tx.Commit(); err != nil {
		return citaDTO{}, err
	}
	resultado := resultadoConectorAgenda{}
	var conexionErr error
	if tipo == "session" {
		resultado, conexionErr = s.crearReunionAgenda(ctx, org, conector, "Cita con "+nombre, inicio, duracion, prueba)
	}
	pendiente := conexionErr != nil && !prueba
	_, _ = s.db.ExecContext(ctx, `UPDATE booking SET external_ref=?,meeting_link=?,link_pending=?,updated_at=? WHERE organization_id=? AND id=?`, nuloVacio(resultado.Referencia), nuloVacio(resultado.Enlace), pendiente, time.Now().UTC(), org, id)
	if conexionErr != nil {
		s.logger.Warn("cita creada con enlace pendiente", "cita", id, "conector", conector, "error", conexionErr)
	}
	return s.obtenerCita(ctx, org, id)
}

func (s *Servidor) obtenerCita(ctx context.Context, org, id string) (citaDTO, error) {
	row := s.db.QueryRowContext(ctx, `SELECT b.id,b.kind,b.status,b.source,b.contact_id,b.conversation_id,b.lead_id,COALESCE(c.name,'Bloqueo'),b.scheduled_at,b.duration_minutes,COALESCE(b.connector,''),b.external_ref,b.meeting_link,b.link_pending,b.is_test,b.notes,b.created_at,b.updated_at FROM booking b LEFT JOIN contact c ON c.id=b.contact_id AND c.organization_id=b.organization_id WHERE b.organization_id=? AND b.id=?`, org, id)
	return escanearCita(row)
}

type cambioCita struct {
	ProgramadaEn    *string `json:"programadaEn"`
	DuracionMinutos *int    `json:"duracionMinutos"`
	Estado          *string `json:"estado"`
	Notas           *string `json:"notas"`
}

func (s *Servidor) actualizarCita(w http.ResponseWriter, r *http.Request) {
	ses := sesionDe(r.Context())
	var e cambioCita
	if !decodificarJSON(w, r, &e) {
		return
	}
	c, err := s.obtenerCita(r.Context(), ses.OrganizacionID, r.PathValue("id"))
	if errors.Is(err, sql.ErrNoRows) {
		responderError(w, 404, "not_found", "Cita no encontrada", nil)
		return
	}
	if err != nil {
		s.errorInterno(w, "leer cita", err)
		return
	}
	inicio := c.ProgramadaEn
	duracion := c.DuracionMinutos
	estado := c.Estado
	notas := c.Notas
	if e.ProgramadaEn != nil {
		t, x := time.Parse(time.RFC3339, *e.ProgramadaEn)
		if x != nil {
			responderError(w, 422, "validation_error", "Fecha invalida", nil)
			return
		}
		inicio = t.UTC()
	}
	if e.DuracionMinutos != nil {
		duracion = *e.DuracionMinutos
	}
	if e.Estado != nil {
		if !estadoCitaValido(*e.Estado) {
			responderError(w, 422, "validation_error", "Estado invalido", nil)
			return
		}
		estado = *e.Estado
	}
	if e.Notas != nil {
		v := strings.TrimSpace(*e.Notas)
		notas = &v
	}
	if inicio != c.ProgramadaEn || duracion != c.DuracionMinutos {
		var n int
		fin := inicio.Add(time.Duration(duracion) * time.Minute)
		_ = s.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM booking WHERE organization_id=? AND is_test=? AND id<>? AND status IN ('agendada','realizada') AND scheduled_at<? AND DATE_ADD(scheduled_at,INTERVAL duration_minutes MINUTE)>?`, ses.OrganizacionID, c.Prueba, c.ID, fin, inicio).Scan(&n)
		if n > 0 {
			responderError(w, 409, "slot_unavailable", "El horario ya no esta disponible", nil)
			return
		}
	}
	_, err = s.db.ExecContext(r.Context(), `UPDATE booking SET scheduled_at=?,duration_minutes=?,status=?,notes=?,link_pending=IF(?='agendada' AND is_test=0,link_pending,0),updated_at=? WHERE organization_id=? AND id=?`, inicio, duracion, estado, notas, estado, time.Now().UTC(), ses.OrganizacionID, c.ID)
	if err != nil {
		s.errorInterno(w, "actualizar cita", err)
		return
	}
	if estado == "cancelada" && !c.Prueba {
		_ = s.cancelarReunionAgenda(r.Context(), ses.OrganizacionID, c.Conector, c.ReferenciaExterna)
	} else if (inicio != c.ProgramadaEn || duracion != c.DuracionMinutos) && !c.Prueba {
		if x := s.actualizarReunionAgenda(r.Context(), ses.OrganizacionID, c.Conector, c.ReferenciaExterna, "Cita con "+c.Contacto, inicio, duracion); x != nil {
			_, _ = s.db.ExecContext(r.Context(), `UPDATE booking SET link_pending=1 WHERE organization_id=? AND id=?`, ses.OrganizacionID, c.ID)
		}
	}
	c, err = s.obtenerCita(r.Context(), ses.OrganizacionID, c.ID)
	if err != nil {
		s.errorInterno(w, "leer cita actualizada", err)
		return
	}
	responderJSON(w, 200, c)
}
func estadoCitaValido(s string) bool {
	return s == "agendada" || s == "realizada" || s == "no_show" || s == "cancelada"
}

func (s *Servidor) cargarConfigMotor(ctx context.Context, org string) (agenda.Configuracion, error) {
	d, err := s.leerAjustesAgenda(ctx, org)
	if err != nil || !d.Configurada {
		return agenda.Configuracion{}, errors.New("agenda no configurada")
	}
	zona, err := time.LoadLocation(d.ZonaHoraria)
	if err != nil {
		return agenda.Configuracion{}, err
	}
	return agenda.Configuracion{Horario: d.Horario, Zona: zona, Duracion: time.Duration(d.DuracionMinutos) * time.Minute, Buffer: time.Duration(d.BufferMinutos) * time.Minute, AvisoMinimo: time.Duration(d.AvisoMinimoHoras) * time.Hour, DiasAdelante: d.DiasAdelante}, nil
}
func (s *Servidor) calcularHuecos(ctx context.Context, org string, ahora time.Time, limite int, prueba bool) ([]agenda.Hueco, error) {
	cfg, err := s.cargarConfigMotor(ctx, org)
	if err != nil {
		return nil, err
	}
	hasta := ahora.AddDate(0, 0, cfg.DiasAdelante+1)
	rows, err := s.db.QueryContext(ctx, `SELECT scheduled_at,duration_minutes FROM booking WHERE organization_id=? AND status IN ('agendada','realizada') AND is_test=? AND scheduled_at>=? AND scheduled_at<?`, org, prueba, ahora.Add(-24*time.Hour), hasta)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	reservas := []agenda.Reserva{}
	for rows.Next() {
		var t time.Time
		var m int
		if err := rows.Scan(&t, &m); err != nil {
			return nil, err
		}
		reservas = append(reservas, agenda.Reserva{Inicio: t, Duracion: time.Duration(m) * time.Minute})
	}
	return agenda.Huecos(cfg, ahora, reservas, limite)
}

func (s *Servidor) disponibilidadBot(w http.ResponseWriter, r *http.Request) {
	org := organizacionBot(r.Context())
	conv := strings.TrimSpace(r.URL.Query().Get("conversationId"))
	if conv == "" {
		responderError(w, 422, "validation_error", "conversationId es obligatorio", nil)
		return
	}
	var habilitada, prueba bool
	err := s.db.QueryRowContext(r.Context(), `SELECT (COALESCE(JSON_UNQUOTE(JSON_EXTRACT(ap.tools,'$.agenda')),'false')='true' OR COALESCE(JSON_UNQUOTE(JSON_EXTRACT(ap.tools,'$.agendar')),'false')='true'),cv.is_test FROM agent_profile ap JOIN conversation cv ON cv.organization_id=ap.organization_id WHERE ap.organization_id=? AND cv.id=? AND cv.ai_enabled=1 AND cv.handoff_at IS NULL`, org, conv).Scan(&habilitada, &prueba)
	if err != nil {
		responderError(w, 404, "not_found", "Conversacion no encontrada", nil)
		return
	}
	if !habilitada {
		responderError(w, 409, "herramienta_apagada", "La herramienta de agenda esta apagada", nil)
		return
	}
	huecos, err := s.calcularHuecos(r.Context(), org, time.Now().UTC(), 12, prueba)
	if err != nil {
		responderError(w, 409, "agenda_not_configured", err.Error(), nil)
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		s.errorInterno(w, "ofrecer horarios", err)
		return
	}
	defer tx.Rollback()
	_, _ = tx.ExecContext(r.Context(), `DELETE FROM offered_slot WHERE organization_id=? AND conversation_id=?`, org, conv)
	items := make([]map[string]any, 0, len(huecos))
	for _, h := range huecos {
		id, _ := identificadores.Nuevo("slot")
		_, err = tx.ExecContext(r.Context(), `INSERT INTO offered_slot(id,organization_id,conversation_id,start_utc,label,offered_at) VALUES (?,?,?,?,?,?)`, id, org, conv, h.Inicio, h.Label, time.Now().UTC())
		if err != nil {
			s.errorInterno(w, "guardar horario ofrecido", err)
			return
		}
		items = append(items, map[string]any{"id": id, "inicio": h.Inicio, "fin": h.Fin, "label": h.Label})
	}
	if err = tx.Commit(); err != nil {
		s.errorInterno(w, "confirmar horarios", err)
		return
	}
	cfg, _ := s.leerAjustesAgenda(r.Context(), org)
	responderJSON(w, 200, map[string]any{"conversationId": conv, "zonaHoraria": cfg.ZonaHoraria, "duracionMinutos": cfg.DuracionMinutos, "prueba": prueba, "slots": items})
}

type entradaCitaBot struct {
	ConversacionID string `json:"conversationId"`
	SlotID         string `json:"slotId"`
	Notas          string `json:"notes"`
}

func (s *Servidor) crearCitaBot(w http.ResponseWriter, r *http.Request) {
	org := organizacionBot(r.Context())
	var e entradaCitaBot
	if !decodificarJSON(w, r, &e) {
		return
	}
	var inicio time.Time
	var prueba bool
	err := s.db.QueryRowContext(r.Context(), `SELECT os.start_utc,cv.is_test FROM offered_slot os JOIN conversation cv ON cv.id=os.conversation_id AND cv.organization_id=os.organization_id WHERE os.organization_id=? AND os.id=? AND os.conversation_id=? AND os.offered_at>=?`, org, e.SlotID, e.ConversacionID, time.Now().UTC().Add(-24*time.Hour)).Scan(&inicio, &prueba)
	if err != nil {
		responderError(w, 409, "slot_not_offered", "El horario no fue ofrecido o vencio", nil)
		return
	}
	cita, err := s.crearReservaAgenda(r.Context(), org, "", e.ConversacionID, inicio, 0, e.Notas, "session", "ai", prueba)
	if err != nil {
		s.responderErrorAgenda(w, err)
		return
	}
	_, _ = s.db.ExecContext(r.Context(), `DELETE FROM offered_slot WHERE organization_id=? AND conversation_id=?`, org, e.ConversacionID)
	responderJSON(w, 201, cita)
}

type reprogramarBot struct {
	BookingID      string `json:"bookingId"`
	ConversacionID string `json:"conversationId"`
	SlotID         string `json:"slotId"`
}

func (s *Servidor) reprogramarCitaBot(w http.ResponseWriter, r *http.Request) {
	org := organizacionBot(r.Context())
	var e reprogramarBot
	if !decodificarJSON(w, r, &e) {
		return
	}
	var inicio time.Time
	err := s.db.QueryRowContext(r.Context(), `SELECT start_utc FROM offered_slot WHERE organization_id=? AND id=? AND conversation_id=? AND offered_at>=?`, org, e.SlotID, e.ConversacionID, time.Now().UTC().Add(-24*time.Hour)).Scan(&inicio)
	if err != nil {
		responderError(w, 409, "slot_not_offered", "El horario no fue ofrecido o vencio", nil)
		return
	}
	c, err := s.obtenerCita(r.Context(), org, e.BookingID)
	if errors.Is(err, sql.ErrNoRows) || c.ConversacionID == nil || *c.ConversacionID != e.ConversacionID || c.Estado != "agendada" {
		responderError(w, 404, "not_found", "Reserva no encontrada", nil)
		return
	}
	fin := inicio.Add(time.Duration(c.DuracionMinutos) * time.Minute)
	var ocupada int
	_ = s.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM booking WHERE organization_id=? AND is_test=? AND id<>? AND status IN ('agendada','realizada') AND scheduled_at<? AND DATE_ADD(scheduled_at,INTERVAL duration_minutes MINUTE)>?`, org, c.Prueba, c.ID, fin, inicio).Scan(&ocupada)
	if ocupada > 0 {
		responderError(w, 409, "slot_unavailable", "El horario ya no esta disponible", nil)
		return
	}
	_, err = s.db.ExecContext(r.Context(), `UPDATE booking SET scheduled_at=?,link_pending=IF(is_test=0,1,0),updated_at=? WHERE organization_id=? AND id=?`, inicio, time.Now().UTC(), org, c.ID)
	if err != nil {
		s.errorInterno(w, "reprogramar cita", err)
		return
	}
	if !c.Prueba {
		if err = s.actualizarReunionAgenda(r.Context(), org, c.Conector, c.ReferenciaExterna, "Cita con "+c.Contacto, inicio, c.DuracionMinutos); err == nil {
			_, _ = s.db.ExecContext(r.Context(), `UPDATE booking SET link_pending=0 WHERE organization_id=? AND id=?`, org, c.ID)
		}
	}
	_, _ = s.db.ExecContext(r.Context(), `DELETE FROM offered_slot WHERE organization_id=? AND conversation_id=?`, org, e.ConversacionID)
	c, err = s.obtenerCita(r.Context(), org, c.ID)
	if err != nil {
		s.errorInterno(w, "leer cita reprogramada", err)
		return
	}
	responderJSON(w, 200, c)
}
