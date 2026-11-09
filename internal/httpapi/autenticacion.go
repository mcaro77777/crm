package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"crm-api/internal/identificadores"
	"crm-api/internal/seguridad"
)

const cookieSesion = "crm_session"

var emailValido = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)

type Sesion struct {
	UsuarioID      string `json:"usuarioId"`
	OrganizacionID string `json:"organizacionId"`
	Nombre         string `json:"nombre"`
	Email          string `json:"email"`
	Rol            string `json:"rol"`
}

type solicitudRegistro struct {
	NombreNegocio string `json:"nombreNegocio"`
	Nombre        string `json:"nombre"`
	Email         string `json:"email"`
	Contrasena    string `json:"contrasena"`
}

type solicitudLogin struct {
	Email      string `json:"email"`
	Contrasena string `json:"contrasena"`
}

func (s *Servidor) registrar(w http.ResponseWriter, r *http.Request) {
	var entrada solicitudRegistro
	if !decodificarJSON(w, r, &entrada) {
		return
	}
	entrada.Email = strings.ToLower(strings.TrimSpace(entrada.Email))
	entrada.Nombre = strings.TrimSpace(entrada.Nombre)
	entrada.NombreNegocio = strings.TrimSpace(entrada.NombreNegocio)
	if entrada.Nombre == "" || entrada.NombreNegocio == "" || !emailValido.MatchString(entrada.Email) || len(entrada.Contrasena) < 12 {
		responderError(w, http.StatusUnprocessableEntity, "validation_error", "Nombre, negocio, email valido y contrasena de 12+ caracteres son obligatorios", nil)
		return
	}

	tx, err := s.db.BeginTx(r.Context(), &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		s.errorInterno(w, "iniciar registro", err)
		return
	}
	defer tx.Rollback()
	var organizaciones int
	if err := tx.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM `organization`").Scan(&organizaciones); err != nil {
		s.errorInterno(w, "comprobar registro", err)
		return
	}
	if organizaciones > 0 {
		responderError(w, http.StatusForbidden, "signup_closed", "El registro publico esta cerrado", nil)
		return
	}

	usuarioID, _ := identificadores.Nuevo("usr")
	orgID, _ := identificadores.Nuevo("org")
	miembroID, _ := identificadores.Nuevo("mem")
	cuentaID, _ := identificadores.Nuevo("acc")
	perfilID, _ := identificadores.Nuevo("ap")
	hash, err := seguridad.HashContrasena(entrada.Contrasena)
	if err != nil {
		responderError(w, http.StatusUnprocessableEntity, "validation_error", err.Error(), nil)
		return
	}
	slug := "negocio-" + strings.ToLower(orgID[len(orgID)-8:])
	ahora := time.Now().UTC()
	consultas := []struct {
		q    string
		args []any
	}{
		{"INSERT INTO `user` (id,name,email,email_verified,created_at,updated_at) VALUES (?,?,?,0,?,?)", []any{usuarioID, entrada.Nombre, entrada.Email, ahora, ahora}},
		{"INSERT INTO `organization` (id,name,slug,created_at) VALUES (?,?,?,?)", []any{orgID, entrada.NombreNegocio, slug, ahora}},
		{"INSERT INTO `member` (id,organization_id,user_id,role,created_at) VALUES (?,?,?,'owner',?)", []any{miembroID, orgID, usuarioID, ahora}},
		{"INSERT INTO `account` (id,account_id,provider_id,user_id,password,created_at,updated_at) VALUES (?,?,'credential',?,?,?,?)", []any{cuentaID, usuarioID, usuarioID, hash, ahora, ahora}},
		{`INSERT INTO agent_profile (id,organization_id,enabled,name,created_at,updated_at) VALUES (?,?,0,'Asistente',?,?)`, []any{perfilID, orgID, ahora, ahora}},
	}
	for _, consulta := range consultas {
		if _, err := tx.ExecContext(r.Context(), consulta.q, consulta.args...); err != nil {
			s.errorInterno(w, "crear organizacion inicial", err)
			return
		}
	}
	for posicion, etapa := range []struct{ nombre, tipo string }{{"Nuevo", "open"}, {"En conversación", "open"}, {"Interesado", "open"}, {"Cliente", "won"}, {"Perdido", "lost"}} {
		id, _ := identificadores.Nuevo("stg")
		if _, err := tx.ExecContext(r.Context(), `INSERT INTO pipeline_stage (id,organization_id,name,position,kind,created_at) VALUES (?,?,?,?,?,?)`, id, orgID, etapa.nombre, posicion, etapa.tipo, ahora); err != nil {
			s.errorInterno(w, "crear pipeline inicial", err)
			return
		}
	}
	token, err := s.crearSesionTx(r.Context(), tx, usuarioID, orgID)
	if err != nil {
		s.errorInterno(w, "crear sesion", err)
		return
	}
	if err := tx.Commit(); err != nil {
		s.errorInterno(w, "confirmar registro", err)
		return
	}
	s.escribirCookie(w, token)
	responderJSON(w, http.StatusCreated, Sesion{UsuarioID: usuarioID, OrganizacionID: orgID, Nombre: entrada.Nombre, Email: entrada.Email, Rol: "owner"})
}

func (s *Servidor) iniciarSesion(w http.ResponseWriter, r *http.Request) {
	var entrada solicitudLogin
	if !decodificarJSON(w, r, &entrada) {
		return
	}
	entrada.Email = strings.ToLower(strings.TrimSpace(entrada.Email))
	var sesion Sesion
	var hash string
	err := s.db.QueryRowContext(r.Context(),
		"SELECT u.id,u.name,u.email,a.password,m.organization_id,m.role "+
			"FROM `user` u "+
			"JOIN `account` a ON a.user_id=u.id AND a.provider_id='credential' "+
			"JOIN `member` m ON m.user_id=u.id "+
			"WHERE u.email=? LIMIT 1", entrada.Email,
	).Scan(&sesion.UsuarioID, &sesion.Nombre, &sesion.Email, &hash, &sesion.OrganizacionID, &sesion.Rol)
	if err != nil || !seguridad.VerificarContrasena(entrada.Contrasena, hash) {
		responderError(w, http.StatusUnauthorized, "invalid_credentials", "Credenciales invalidas", nil)
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		s.errorInterno(w, "iniciar sesion", err)
		return
	}
	defer tx.Rollback()
	token, err := s.crearSesionTx(r.Context(), tx, sesion.UsuarioID, sesion.OrganizacionID)
	if err != nil {
		s.errorInterno(w, "guardar sesion", err)
		return
	}
	if err := tx.Commit(); err != nil {
		s.errorInterno(w, "confirmar sesion", err)
		return
	}
	s.escribirCookie(w, token)
	responderJSON(w, http.StatusOK, sesion)
}

func (s *Servidor) cerrarSesion(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(cookieSesion); err == nil {
		_, _ = s.db.ExecContext(r.Context(), "DELETE FROM `session` WHERE token=?", seguridad.HashTokenConSecreto(cookie.Value, s.cfg.SecretoAutenticacion))
	}
	http.SetCookie(w, &http.Cookie{Name: cookieSesion, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: s.cfg.Produccion(), SameSite: http.SameSiteLaxMode})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Servidor) sesionActual(w http.ResponseWriter, r *http.Request) {
	responderJSON(w, http.StatusOK, sesionDe(r.Context()))
}

func (s *Servidor) requerirSesion(siguiente http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(cookieSesion)
		if err != nil || cookie.Value == "" {
			responderError(w, http.StatusUnauthorized, "unauthorized", "No autenticado", nil)
			return
		}
		var sesion Sesion
		err = s.db.QueryRowContext(r.Context(),
			"SELECT s.user_id,s.active_organization_id,u.name,u.email,m.role "+
				"FROM `session` s "+
				"JOIN `user` u ON u.id=s.user_id "+
				"JOIN `member` m ON m.user_id=s.user_id AND m.organization_id=s.active_organization_id "+
				"WHERE s.token=? AND s.expires_at > UTC_TIMESTAMP(3) LIMIT 1",
			seguridad.HashTokenConSecreto(cookie.Value, s.cfg.SecretoAutenticacion),
		).Scan(&sesion.UsuarioID, &sesion.OrganizacionID, &sesion.Nombre, &sesion.Email, &sesion.Rol)
		if err != nil {
			responderError(w, http.StatusUnauthorized, "unauthorized", "Sesion invalida o vencida", nil)
			return
		}
		ctx := context.WithValue(r.Context(), claveSesion, sesion)
		siguiente.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Servidor) crearSesionTx(ctx context.Context, tx *sql.Tx, usuarioID, organizacionID string) (string, error) {
	id, err := identificadores.Nuevo("ses")
	if err != nil {
		return "", err
	}
	token, err := seguridad.TokenSeguro(32)
	if err != nil {
		return "", err
	}
	ahora := time.Now().UTC()
	_, err = tx.ExecContext(ctx, "INSERT INTO `session` (id,expires_at,token,created_at,updated_at,user_id,active_organization_id) VALUES (?,?,?,?,?,?,?)", id, ahora.Add(s.cfg.DuracionSesion), seguridad.HashTokenConSecreto(token, s.cfg.SecretoAutenticacion), ahora, ahora, usuarioID, organizacionID)
	return token, err
}

func (s *Servidor) escribirCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{Name: cookieSesion, Value: token, Path: "/", HttpOnly: true, Secure: s.cfg.Produccion(), SameSite: http.SameSiteLaxMode, MaxAge: int(s.cfg.DuracionSesion.Seconds())})
}

func sesionDe(ctx context.Context) Sesion {
	sesion, _ := ctx.Value(claveSesion).(Sesion)
	return sesion
}

func (s *Servidor) errorInterno(w http.ResponseWriter, operacion string, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		responderError(w, http.StatusNotFound, "not_found", "Recurso no encontrado", nil)
		return
	}
	s.logger.Error("error interno", "operacion", operacion, "error", err)
	responderError(w, http.StatusInternalServerError, "internal_error", "Error interno", nil)
}

func validarRol(sesion Sesion, roles ...string) error {
	for _, rol := range roles {
		if sesion.Rol == rol {
			return nil
		}
	}
	return fmt.Errorf("rol %s no autorizado", sesion.Rol)
}
