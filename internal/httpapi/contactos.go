package httpapi

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"crm-api/internal/identificadores"
	"github.com/go-sql-driver/mysql"
)

type contactoDTO struct {
	ID            string         `json:"id"`
	Canal         string         `json:"canal"`
	Identidad     string         `json:"identidad"`
	Telefono      sql.NullString `json:"-"`
	TelefonoJSON  *string        `json:"telefono"`
	Nombre        string         `json:"nombre"`
	Notas         sql.NullString `json:"-"`
	NotasJSON     *string        `json:"notas"`
	Fuente        sql.NullString `json:"-"`
	FuenteJSON    *string        `json:"fuente"`
	AgentePausado bool           `json:"agentePausado"`
	ArchivadoEn   sql.NullTime   `json:"-"`
	ArchivadoJSON *time.Time     `json:"archivadoEn"`
	CreadoEn      time.Time      `json:"creadoEn"`
	ActualizadoEn time.Time      `json:"actualizadoEn"`
}

func (c *contactoDTO) normalizar() {
	if c.Telefono.Valid {
		v := c.Telefono.String
		c.TelefonoJSON = &v
	}
	if c.Notas.Valid {
		v := c.Notas.String
		c.NotasJSON = &v
	}
	if c.Fuente.Valid {
		v := c.Fuente.String
		c.FuenteJSON = &v
	}
	if c.ArchivadoEn.Valid {
		v := c.ArchivadoEn.Time
		c.ArchivadoJSON = &v
	}
}

func (s *Servidor) listarContactos(w http.ResponseWriter, r *http.Request) {
	sesion := sesionDe(r.Context())
	limite := numeroConsulta(r, "limit", 50, 1, 200)
	desde := numeroConsulta(r, "offset", 0, 0, 100_000)
	busqueda := strings.TrimSpace(r.URL.Query().Get("q"))
	patron := "%" + busqueda + "%"
	rows, err := s.db.QueryContext(r.Context(),
		"SELECT id,channel,wa_identity,phone,name,notes,source,ai_paused,archived_at,created_at,updated_at "+
			"FROM `contact` "+
			"WHERE organization_id=? AND archived_at IS NULL "+
			"AND (?='' OR name LIKE ? OR COALESCE(phone,'') LIKE ? OR wa_identity LIKE ?) "+
			"ORDER BY updated_at DESC LIMIT ? OFFSET ?",
		sesion.OrganizacionID, busqueda, patron, patron, patron, limite, desde,
	)
	if err != nil {
		s.errorInterno(w, "listar contactos", err)
		return
	}
	defer rows.Close()
	contactos := make([]contactoDTO, 0)
	for rows.Next() {
		var c contactoDTO
		if err := rows.Scan(&c.ID, &c.Canal, &c.Identidad, &c.Telefono, &c.Nombre, &c.Notas, &c.Fuente, &c.AgentePausado, &c.ArchivadoEn, &c.CreadoEn, &c.ActualizadoEn); err != nil {
			s.errorInterno(w, "leer contactos", err)
			return
		}
		c.normalizar()
		contactos = append(contactos, c)
	}
	if err := rows.Err(); err != nil {
		s.errorInterno(w, "recorrer contactos", err)
		return
	}
	responderJSON(w, http.StatusOK, map[string]any{"items": contactos, "limit": limite, "offset": desde})
}

func (s *Servidor) obtenerContacto(w http.ResponseWriter, r *http.Request) {
	sesion := sesionDe(r.Context())
	var c contactoDTO
	err := s.db.QueryRowContext(r.Context(), "SELECT id,channel,wa_identity,phone,name,notes,source,ai_paused,archived_at,created_at,updated_at FROM `contact` WHERE organization_id=? AND id=?", sesion.OrganizacionID, r.PathValue("id")).Scan(&c.ID, &c.Canal, &c.Identidad, &c.Telefono, &c.Nombre, &c.Notas, &c.Fuente, &c.AgentePausado, &c.ArchivadoEn, &c.CreadoEn, &c.ActualizadoEn)
	if err != nil {
		s.errorInterno(w, "obtener contacto", err)
		return
	}
	c.normalizar()
	responderJSON(w, http.StatusOK, c)
}

type crearContactoBody struct {
	Nombre        string  `json:"nombre"`
	Canal         string  `json:"canal"`
	Identidad     string  `json:"identidad"`
	Telefono      *string `json:"telefono"`
	Notas         *string `json:"notas"`
	Fuente        *string `json:"fuente"`
	AgentePausado bool    `json:"agentePausado"`
}

func (s *Servidor) crearContacto(w http.ResponseWriter, r *http.Request) {
	sesion := sesionDe(r.Context())
	var entrada crearContactoBody
	if !decodificarJSON(w, r, &entrada) {
		return
	}
	entrada.Nombre = strings.TrimSpace(entrada.Nombre)
	entrada.Canal = strings.ToLower(strings.TrimSpace(entrada.Canal))
	entrada.Identidad = strings.TrimSpace(entrada.Identidad)
	if entrada.Nombre == "" || entrada.Identidad == "" || !canalValido(entrada.Canal) {
		responderError(w, 422, "validation_error", "Nombre, identidad y canal valido son obligatorios", nil)
		return
	}
	if !fuenteValida(entrada.Fuente) {
		responderError(w, 422, "validation_error", "Fuente invalida", nil)
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		s.errorInterno(w, "crear contacto", err)
		return
	}
	defer tx.Rollback()
	var etapaID, etapaNombre, etapaTipo string
	if err := tx.QueryRowContext(r.Context(), "SELECT id,name,kind FROM `pipeline_stage` WHERE organization_id=? AND kind='open' ORDER BY position LIMIT 1", sesion.OrganizacionID).Scan(&etapaID, &etapaNombre, &etapaTipo); err != nil {
		s.errorInterno(w, "buscar etapa inicial", err)
		return
	}
	contactoID, _ := identificadores.Nuevo("ct")
	leadID, _ := identificadores.Nuevo("ld")
	eventoID, _ := identificadores.Nuevo("lse")
	ahora := time.Now().UTC()
	_, err = tx.ExecContext(r.Context(), "INSERT INTO `contact` (id,organization_id,channel,wa_identity,phone,name,name_source,notes,source,ai_paused,created_at,updated_at) VALUES (?,?,?,?,?,?,'manual',?,?,?,?,?)", contactoID, sesion.OrganizacionID, entrada.Canal, entrada.Identidad, entrada.Telefono, entrada.Nombre, entrada.Notas, entrada.Fuente, entrada.AgentePausado, ahora, ahora)
	if err != nil {
		if duplicado(err) {
			responderError(w, 409, "contact_exists", "Ya existe un contacto con esa identidad", nil)
		} else {
			s.errorInterno(w, "insertar contacto", err)
		}
		return
	}
	if _, err = tx.ExecContext(r.Context(), "INSERT INTO `lead` (id,organization_id,contact_id,stage_id,position,last_activity_at,created_at,updated_at) VALUES (?,?,?,?,0,?,?,?)", leadID, sesion.OrganizacionID, contactoID, etapaID, ahora, ahora, ahora); err != nil {
		s.errorInterno(w, "crear lead", err)
		return
	}
	if _, err = tx.ExecContext(r.Context(), "INSERT INTO `lead_stage_event` (id,organization_id,lead_id,contact_id,to_stage_id,to_stage_name,to_stage_kind,occurred_at,actor_user_id,source,created_at) VALUES (?,?,?,?,?,?,?,?,?,'dueno',?)", eventoID, sesion.OrganizacionID, leadID, contactoID, etapaID, etapaNombre, etapaTipo, ahora, sesion.UsuarioID, ahora); err != nil {
		s.errorInterno(w, "registrar etapa", err)
		return
	}
	if err = tx.Commit(); err != nil {
		s.errorInterno(w, "confirmar contacto", err)
		return
	}
	s.bus.Publicar(sesion.OrganizacionID, "contact.created", map[string]string{"contactId": contactoID})
	r.SetPathValue("id", contactoID)
	s.obtenerContacto(w, r)
}

type actualizarContactoBody struct {
	Nombre        *string `json:"nombre"`
	Notas         *string `json:"notas"`
	Fuente        *string `json:"fuente"`
	AgentePausado *bool   `json:"agentePausado"`
	Archivado     *bool   `json:"archivado"`
}

func (s *Servidor) actualizarContacto(w http.ResponseWriter, r *http.Request) {
	sesion := sesionDe(r.Context())
	var entrada actualizarContactoBody
	if !decodificarJSON(w, r, &entrada) {
		return
	}
	partes := []string{"updated_at=?"}
	args := []any{time.Now().UTC()}
	if entrada.Nombre != nil {
		n := strings.TrimSpace(*entrada.Nombre)
		if n == "" {
			responderError(w, 422, "validation_error", "El nombre no puede estar vacio", nil)
			return
		}
		partes = append(partes, "name=?", "name_source='manual'")
		args = append(args, n)
	}
	if entrada.Notas != nil {
		partes = append(partes, "notes=?")
		args = append(args, *entrada.Notas)
	}
	if entrada.Fuente != nil {
		if !fuenteValida(entrada.Fuente) {
			responderError(w, 422, "validation_error", "Fuente invalida", nil)
			return
		}
		partes = append(partes, "source=?")
		args = append(args, *entrada.Fuente)
	}
	if entrada.AgentePausado != nil {
		partes = append(partes, "ai_paused=?")
		args = append(args, *entrada.AgentePausado)
	}
	if entrada.Archivado != nil {
		if *entrada.Archivado {
			partes = append(partes, "archived_at=?")
			args = append(args, time.Now().UTC())
		} else {
			partes = append(partes, "archived_at=NULL")
		}
	}
	args = append(args, sesion.OrganizacionID, r.PathValue("id"))
	resultado, err := s.db.ExecContext(r.Context(), "UPDATE `contact` SET "+strings.Join(partes, ",")+" WHERE organization_id=? AND id=?", args...)
	if err != nil {
		s.errorInterno(w, "actualizar contacto", err)
		return
	}
	n, _ := resultado.RowsAffected()
	if n == 0 {
		responderError(w, 404, "not_found", "Contacto no encontrado", nil)
		return
	}
	s.bus.Publicar(sesion.OrganizacionID, "contact.updated", map[string]string{"contactId": r.PathValue("id")})
	s.obtenerContacto(w, r)
}

func canalValido(c string) bool {
	return c == "whatsapp" || c == "instagram" || c == "messenger" || c == "tiktok"
}
func fuenteValida(fuente *string) bool {
	if fuente == nil {
		return true
	}
	switch *fuente {
	case "anuncio", "organico", "referido", "conocido", "otro":
		return true
	default:
		return false
	}
}
func duplicado(err error) bool {
	var e *mysql.MySQLError
	return errors.As(err, &e) && e.Number == 1062
}
func numeroConsulta(r *http.Request, nombre string, defecto, minimo, maximo int) int {
	v, err := strconv.Atoi(r.URL.Query().Get(nombre))
	if err != nil || v < minimo {
		return defecto
	}
	if v > maximo {
		return maximo
	}
	return v
}
