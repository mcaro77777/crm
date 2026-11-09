package httpapi

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"crm-api/internal/identificadores"
)

func puedeAdministrarEquipo(rol string) bool { return rol == "owner" || rol == "admin" }
func rolEquipoValido(rol string) bool        { return rol == "owner" || rol == "admin" || rol == "agent" }

func (s *Servidor) listarEquipo(w http.ResponseWriter, r *http.Request) {
	sesion := sesionDe(r.Context())
	rows, err := s.db.QueryContext(r.Context(), `SELECT m.id,u.id,u.name,u.email,m.role,m.created_at FROM member m JOIN user u ON u.id=m.user_id WHERE m.organization_id=? ORDER BY m.created_at`, sesion.OrganizacionID)
	if err != nil {
		s.errorInterno(w, "listar equipo", err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var mid, uid, nombre, email, rol string
		var creado time.Time
		if err := rows.Scan(&mid, &uid, &nombre, &email, &rol, &creado); err != nil {
			s.errorInterno(w, "leer equipo", err)
			return
		}
		items = append(items, map[string]any{"id": mid, "usuarioId": uid, "nombre": nombre, "email": email, "rol": rol, "creadoEn": creado})
	}
	responderJSON(w, 200, map[string]any{"items": items})
}

func (s *Servidor) invitarEquipo(w http.ResponseWriter, r *http.Request) {
	sesion := sesionDe(r.Context())
	if !puedeAdministrarEquipo(sesion.Rol) {
		responderError(w, 403, "forbidden", "No tienes permiso para invitar", nil)
		return
	}
	var e struct {
		Email string `json:"email"`
		Rol   string `json:"rol"`
	}
	if !decodificarJSON(w, r, &e) {
		return
	}
	e.Email = strings.ToLower(strings.TrimSpace(e.Email))
	e.Rol = strings.ToLower(strings.TrimSpace(e.Rol))
	if !strings.Contains(e.Email, "@") || !rolEquipoValido(e.Rol) {
		responderError(w, 422, "validation_error", "Correo o rol inválido", nil)
		return
	}
	id, _ := identificadores.Nuevo("inv")
	vence := time.Now().UTC().Add(7 * 24 * time.Hour)
	_, err := s.db.ExecContext(r.Context(), `INSERT INTO invitation(id,organization_id,email,role,status,expires_at,inviter_id) VALUES (?,?,?,?,'pending',?,?)`, id, sesion.OrganizacionID, e.Email, e.Rol, vence, sesion.UsuarioID)
	if err != nil {
		s.errorInterno(w, "crear invitación", err)
		return
	}
	responderJSON(w, 201, map[string]any{"id": id, "email": e.Email, "rol": e.Rol, "venceEn": vence})
}

func (s *Servidor) aceptarInvitacion(w http.ResponseWriter, r *http.Request) {
	sesion := sesionDe(r.Context())
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		s.errorInterno(w, "aceptar invitación", err)
		return
	}
	defer tx.Rollback()
	var org, email, rol, estado string
	var vence time.Time
	err = tx.QueryRowContext(r.Context(), `SELECT organization_id,email,role,status,expires_at FROM invitation WHERE id=? FOR UPDATE`, r.PathValue("id")).Scan(&org, &email, &rol, &estado, &vence)
	if err != nil {
		s.errorInterno(w, "leer invitación", err)
		return
	}
	if estado != "pending" || time.Now().UTC().After(vence) || !strings.EqualFold(email, sesion.Email) {
		responderError(w, 409, "invitation_invalid", "La invitación no está vigente o no pertenece a tu correo", nil)
		return
	}
	mid, _ := identificadores.Nuevo("mem")
	_, err = tx.ExecContext(r.Context(), `INSERT INTO member(id,organization_id,user_id,role,created_at) VALUES (?,?,?,?,?)`, mid, org, sesion.UsuarioID, rol, time.Now().UTC())
	if err != nil {
		s.errorInterno(w, "agregar integrante", err)
		return
	}
	_, err = tx.ExecContext(r.Context(), `UPDATE invitation SET status='accepted' WHERE id=?`, r.PathValue("id"))
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		s.errorInterno(w, "confirmar invitación", err)
		return
	}
	w.WriteHeader(204)
}

func (s *Servidor) cambiarRolEquipo(w http.ResponseWriter, r *http.Request) {
	sesion := sesionDe(r.Context())
	if !puedeAdministrarEquipo(sesion.Rol) {
		responderError(w, 403, "forbidden", "No tienes permiso", nil)
		return
	}
	var e struct {
		Rol string `json:"rol"`
	}
	if !decodificarJSON(w, r, &e) {
		return
	}
	if !rolEquipoValido(e.Rol) {
		responderError(w, 422, "validation_error", "Rol inválido", nil)
		return
	}
	var actual string
	err := s.db.QueryRowContext(r.Context(), `SELECT role FROM member WHERE organization_id=? AND id=?`, sesion.OrganizacionID, r.PathValue("id")).Scan(&actual)
	if err != nil {
		s.errorInterno(w, "leer integrante", err)
		return
	}
	if actual == "owner" && e.Rol != "owner" {
		var n int
		_ = s.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM member WHERE organization_id=? AND role='owner'`, sesion.OrganizacionID).Scan(&n)
		if n <= 1 {
			responderError(w, 409, "last_owner", "La organización debe conservar un owner", nil)
			return
		}
	}
	_, err = s.db.ExecContext(r.Context(), `UPDATE member SET role=? WHERE organization_id=? AND id=?`, e.Rol, sesion.OrganizacionID, r.PathValue("id"))
	if err != nil {
		s.errorInterno(w, "cambiar rol", err)
		return
	}
	w.WriteHeader(204)
}

func (s *Servidor) asignarConversacion(w http.ResponseWriter, r *http.Request) {
	sesion := sesionDe(r.Context())
	var e struct {
		UsuarioID *string `json:"usuarioId"`
	}
	if !decodificarJSON(w, r, &e) {
		return
	}
	if _, err := s.db.ExecContext(r.Context(), `CREATE TABLE IF NOT EXISTS conversation_assignment(organization_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,conversation_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,user_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,updated_at DATETIME(3) NOT NULL,PRIMARY KEY(organization_id,conversation_id),FOREIGN KEY(organization_id) REFERENCES organization(id) ON DELETE CASCADE,FOREIGN KEY(conversation_id) REFERENCES conversation(id) ON DELETE CASCADE,FOREIGN KEY(user_id) REFERENCES user(id) ON DELETE SET NULL) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`); err != nil {
		s.errorInterno(w, "preparar asignaciones", err)
		return
	}
	if e.UsuarioID != nil {
		var pertenece bool
		_ = s.db.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM member WHERE organization_id=? AND user_id=?)`, sesion.OrganizacionID, *e.UsuarioID).Scan(&pertenece)
		if !pertenece {
			responderError(w, 422, "invalid_assignee", "El responsable no pertenece al equipo", nil)
			return
		}
	}
	var existe bool
	_ = s.db.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM conversation WHERE organization_id=? AND id=?)`, sesion.OrganizacionID, r.PathValue("id")).Scan(&existe)
	if !existe {
		responderError(w, 404, "not_found", "Conversación no encontrada", nil)
		return
	}
	_, err := s.db.ExecContext(r.Context(), `INSERT INTO conversation_assignment(organization_id,conversation_id,user_id,updated_at) VALUES (?,?,?,?) ON DUPLICATE KEY UPDATE user_id=VALUES(user_id),updated_at=VALUES(updated_at)`, sesion.OrganizacionID, r.PathValue("id"), e.UsuarioID, time.Now().UTC())
	if err != nil {
		s.errorInterno(w, "asignar conversación", err)
		return
	}
	s.bus.Publicar(sesion.OrganizacionID, "conversation.updated", map[string]any{"conversationId": r.PathValue("id"), "assignedUserId": e.UsuarioID})
	w.WriteHeader(204)
}

func (s *Servidor) guardarFichaContacto(w http.ResponseWriter, r *http.Request) {
	sesion := sesionDe(r.Context())
	var entrada map[string]any
	if !decodificarJSON(w, r, &entrada) {
		return
	}
	if len(entrada) > 100 {
		responderError(w, 422, "validation_error", "La ficha admite hasta 100 campos", nil)
		return
	}
	for k := range entrada {
		if strings.TrimSpace(k) == "" || len(k) > 64 {
			responderError(w, 422, "validation_error", "Nombre de campo inválido", nil)
			return
		}
	}
	raw, _ := json.Marshal(entrada)
	res, err := s.db.ExecContext(r.Context(), `UPDATE contact SET ficha=?,updated_at=? WHERE organization_id=? AND id=?`, raw, time.Now().UTC(), sesion.OrganizacionID, r.PathValue("id"))
	if err != nil {
		s.errorInterno(w, "guardar ficha", err)
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		responderError(w, 404, "not_found", "Contacto no encontrado", nil)
		return
	}
	s.bus.Publicar(sesion.OrganizacionID, "contact.updated", map[string]string{"contactId": r.PathValue("id")})
	responderJSON(w, 200, entrada)
}

func (s *Servidor) obtenerFichaContacto(w http.ResponseWriter, r *http.Request) {
	sesion := sesionDe(r.Context())
	var raw []byte
	err := s.db.QueryRowContext(r.Context(), `SELECT COALESCE(ficha,JSON_OBJECT()) FROM contact WHERE organization_id=? AND id=?`, sesion.OrganizacionID, r.PathValue("id")).Scan(&raw)
	if err != nil {
		s.errorInterno(w, "leer ficha", err)
		return
	}
	var ficha map[string]any
	_ = json.Unmarshal(raw, &ficha)
	responderJSON(w, 200, ficha)
}

func (s *Servidor) exportarContactosCSV(w http.ResponseWriter, r *http.Request) {
	sesion := sesionDe(r.Context())
	rows, err := s.db.QueryContext(r.Context(), `SELECT name,channel,wa_identity,COALESCE(phone,''),COALESCE(source,''),COALESCE(notes,'') FROM contact WHERE organization_id=? ORDER BY created_at`, sesion.OrganizacionID)
	if err != nil {
		s.errorInterno(w, "exportar contactos", err)
		return
	}
	defer rows.Close()
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="contactos.csv"`)
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"nombre", "canal", "identidad", "telefono", "fuente", "notas"})
	for rows.Next() {
		fila := make([]string, 6)
		if rows.Scan(&fila[0], &fila[1], &fila[2], &fila[3], &fila[4], &fila[5]) == nil {
			_ = cw.Write(fila)
		}
	}
	cw.Flush()
}

func (s *Servidor) importarContactosCSV(w http.ResponseWriter, r *http.Request) {
	sesion := sesionDe(r.Context())
	lector := csv.NewReader(io.LimitReader(r.Body, 5<<20))
	lector.FieldsPerRecord = -1
	cab, err := lector.Read()
	if err != nil {
		responderError(w, 422, "csv_invalid", "CSV vacío o inválido", nil)
		return
	}
	indices := map[string]int{}
	for i, c := range cab {
		indices[strings.ToLower(strings.TrimSpace(c))] = i
	}
	for _, c := range []string{"nombre", "identidad"} {
		if _, ok := indices[c]; !ok {
			responderError(w, 422, "csv_invalid", "Faltan columnas nombre e identidad", nil)
			return
		}
	}
	var etapa string
	if err := s.db.QueryRowContext(r.Context(), `SELECT id FROM pipeline_stage WHERE organization_id=? AND kind='open' ORDER BY position LIMIT 1`, sesion.OrganizacionID).Scan(&etapa); err != nil {
		s.errorInterno(w, "buscar etapa", err)
		return
	}
	creados, omitidos := 0, 0
	for linea := 2; ; linea++ {
		fila, err := lector.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			omitidos++
			continue
		}
		valor := func(k string) string {
			i, ok := indices[k]
			if !ok || i >= len(fila) {
				return ""
			}
			return strings.TrimSpace(fila[i])
		}
		nombre, idn := valor("nombre"), valor("identidad")
		canal := valor("canal")
		if canal == "" {
			canal = "whatsapp"
		}
		if nombre == "" || idn == "" || !canalValido(canal) {
			omitidos++
			continue
		}
		ct, _ := identificadores.Nuevo("ct")
		ahora := time.Now().UTC()
		res, err := s.db.ExecContext(r.Context(), `INSERT IGNORE INTO contact(id,organization_id,channel,wa_identity,phone,name,name_source,source,created_at,updated_at) VALUES (?,?,?,?,?,?,'manual',NULLIF(?,''),?,?)`, ct, sesion.OrganizacionID, canal, idn, valor("telefono"), nombre, valor("fuente"), ahora, ahora)
		if err != nil {
			omitidos++
			continue
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			omitidos++
			continue
		}
		ld, _ := identificadores.Nuevo("ld")
		_, _ = s.db.ExecContext(r.Context(), `INSERT INTO lead(id,organization_id,contact_id,stage_id,position,created_at,updated_at) VALUES (?,?,?,?,0,?,?)`, ld, sesion.OrganizacionID, ct, etapa, ahora, ahora)
		creados++
	}
	responderJSON(w, 200, map[string]int{"creados": creados, "omitidos": omitidos})
}

func (s *Servidor) buscarGlobal(w http.ResponseWriter, r *http.Request) {
	sesion := sesionDe(r.Context())
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q) < 2 {
		responderJSON(w, 200, map[string]any{"items": []any{}})
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `SELECT id,name,channel,wa_identity FROM contact WHERE organization_id=? AND archived_at IS NULL AND (name LIKE ? OR wa_identity LIKE ?) ORDER BY updated_at DESC LIMIT 20`, sesion.OrganizacionID, "%"+q+"%", "%"+q+"%")
	if err != nil {
		s.errorInterno(w, "búsqueda global", err)
		return
	}
	defer rows.Close()
	items := []map[string]string{}
	for rows.Next() {
		var id, nombre, canal, identidad string
		if rows.Scan(&id, &nombre, &canal, &identidad) == nil {
			items = append(items, map[string]string{"tipo": "contacto", "id": id, "titulo": nombre, "detalle": canal + " · " + identidad})
		}
	}
	responderJSON(w, 200, map[string]any{"items": items})
}

func clavesOrdenadas(m map[string]any) []string {
	r := make([]string, 0, len(m))
	for k := range m {
		r = append(r, k)
	}
	sort.Strings(r)
	return r
}
