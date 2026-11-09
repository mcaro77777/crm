package httpapi

import (
	"crm-api/internal/identificadores"
	"database/sql"
	"net/http"
	"strings"
	"time"
)

type plantillaDTO struct {
	ID        string `json:"id"`
	Nombre    string `json:"nombre"`
	Idioma    string `json:"idioma"`
	Categoria string `json:"categoria"`
	Cuerpo    string `json:"cuerpo"`
	Estado    string `json:"estado"`
}

func (s *Servidor) ListarPlantillas(w http.ResponseWriter, r *http.Request) {
	ses := sesionDe(r.Context())
	rows, err := s.db.QueryContext(r.Context(), "SELECT id,name,language,category,body,status FROM template WHERE organization_id=? ORDER BY name,language", ses.OrganizacionID)
	if err != nil {
		s.errorInterno(w, "listar plantillas", err)
		return
	}
	defer rows.Close()
	items := []plantillaDTO{}
	for rows.Next() {
		var p plantillaDTO
		if err := rows.Scan(&p.ID, &p.Nombre, &p.Idioma, &p.Categoria, &p.Cuerpo, &p.Estado); err != nil {
			s.errorInterno(w, "leer plantilla", err)
			return
		}
		items = append(items, p)
	}
	responderJSON(w, 200, map[string]any{"items": items})
}

func (s *Servidor) SincronizarPlantillas(w http.ResponseWriter, r *http.Request) {
	ses := sesionDe(r.Context())
	cred, err := s.credencialMeta(r.Context(), ses.OrganizacionID)
	if err != nil {
		responderError(w, 409, "whatsapp_not_configured", "Configura WhatsApp antes de sincronizar", nil)
		return
	}
	items, err := s.clienteMeta(cred.Token, cred.Version).ListarPlantillas(r.Context(), cred.WABAID)
	if err != nil {
		responderError(w, 502, "meta_templates_failed", err.Error(), nil)
		return
	}
	ahora := time.Now().UTC()
	for _, p := range items {
		cuerpo := ""
		for _, c := range p.Componentes {
			if strings.EqualFold(c.Tipo, "BODY") {
				cuerpo = c.Texto
			}
		}
		id, _ := identificadores.Nuevo("tpl")
		estado := strings.ToLower(p.Estado)
		if estado == "disabled" {
			estado = "rejected"
		}
		if estado != "approved" && estado != "pending" && estado != "rejected" {
			estado = "draft"
		}
		_, err = s.db.ExecContext(r.Context(), `INSERT INTO template(id,organization_id,name,language,category,body,status,wa_template_id,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE category=VALUES(category),body=VALUES(body),status=VALUES(status),wa_template_id=VALUES(wa_template_id),updated_at=VALUES(updated_at)`, id, ses.OrganizacionID, p.Nombre, p.Idioma, p.Categoria, cuerpo, estado, p.ID, ahora, ahora)
		if err != nil {
			s.errorInterno(w, "guardar plantilla", err)
			return
		}
	}
	responderJSON(w, 200, map[string]any{"sincronizadas": len(items)})
}

func (s *Servidor) EnviarPlantilla(w http.ResponseWriter, r *http.Request) {
	ses := sesionDe(r.Context())
	var e struct {
		PlantillaID string `json:"plantillaId"`
	}
	if !decodificarJSON(w, r, &e) {
		return
	}
	var nombre, idioma, cuerpo, identidad string
	var prueba bool
	err := s.db.QueryRowContext(r.Context(), `SELECT t.name,t.language,t.body,c.wa_identity,cv.is_test FROM template t JOIN conversation cv ON cv.organization_id=t.organization_id JOIN contact c ON c.id=cv.contact_id AND c.organization_id=cv.organization_id WHERE t.organization_id=? AND t.id=? AND t.status='approved' AND cv.id=?`, ses.OrganizacionID, e.PlantillaID, r.PathValue("id")).Scan(&nombre, &idioma, &cuerpo, &identidad, &prueba)
	if err == sql.ErrNoRows {
		responderError(w, 404, "template_not_found", "Plantilla aprobada no encontrada", nil)
		return
	}
	if err != nil {
		s.errorInterno(w, "leer plantilla", err)
		return
	}
	if prueba {
		responderError(w, 409, "sandbox_violation", "Una conversación de prueba no toca Meta", nil)
		return
	}
	cred, err := s.credencialMeta(r.Context(), ses.OrganizacionID)
	if err != nil {
		responderError(w, 409, "whatsapp_not_configured", "Configura WhatsApp", nil)
		return
	}
	waID, err := s.clienteMeta(cred.Token, cred.Version).EnviarPlantilla(r.Context(), cred.TelefonoID, identidad, nombre, idioma)
	if err != nil {
		responderError(w, 502, "meta_send_failed", err.Error(), nil)
		return
	}
	id, _ := identificadores.Nuevo("msg")
	ahora := time.Now().UTC()
	_, err = s.db.ExecContext(r.Context(), `INSERT INTO message(id,organization_id,conversation_id,wa_message_id,direction,type,text,status,ai_generated,origin,created_at) VALUES (?,?,?,?,'out','template',?,'sent',0,'template',?)`, id, ses.OrganizacionID, r.PathValue("id"), waID, cuerpo, ahora)
	if err != nil {
		s.errorInterno(w, "guardar plantilla enviada", err)
		return
	}
	s.bus.Publicar(ses.OrganizacionID, "message.new", map[string]string{"conversationId": r.PathValue("id"), "messageId": id})
	responderJSON(w, 201, map[string]string{"id": id, "status": "sent"})
}
