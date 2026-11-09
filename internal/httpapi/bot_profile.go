package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
)

func (s *Servidor) perfilBot(w http.ResponseWriter, r *http.Request) {
	org := organizacionBot(r.Context())
	var nombre, rol string
	var tono, instrucciones, reglas, saludo sql.NullString
	var herramientas []byte
	err := s.db.QueryRowContext(r.Context(), `SELECT name,tone,instructions,escalation_rules,greeting,role,COALESCE(tools,JSON_OBJECT()) FROM agent_profile WHERE organization_id=?`, org).Scan(&nombre, &tono, &instrucciones, &reglas, &saludo, &rol, &herramientas)
	if err == sql.ErrNoRows {
		responderError(w, http.StatusNotFound, "no_profile", "No existe un perfil de agente", nil)
		return
	}
	if err != nil {
		s.errorInterno(w, "leer perfil para cerebro", err)
		return
	}
	var configuradas map[string]bool
	_ = json.Unmarshal(herramientas, &configuradas)
	herramientasActivas := []string{"escalar"}
	for _, clave := range []string{"ficha", "etapa", "agendar", "reprogramar", "salida"} {
		valor := configuradas[clave]
		if clave == "agendar" && !valor {
			valor = configuradas["agenda"]
		}
		if valor {
			herramientasActivas = append(herramientasActivas, clave)
		}
	}
	kb, err := s.renderizarConocimiento(r.Context(), org)
	if err != nil {
		s.errorInterno(w, "renderizar conocimiento", err)
		return
	}
	var llm any
	var modelo string
	if err := s.db.QueryRowContext(r.Context(), `SELECT model FROM ai_credentials WHERE organization_id=? AND status='active'`, org).Scan(&modelo); err == nil {
		llm = map[string]string{"path": "/api/bot/llm", "model": modelo}
	}
	responderJSON(w, http.StatusOK, map[string]any{
		"profile": map[string]any{"name": nombre, "tone": nuloString(tono), "instructions": nuloString(instrucciones), "escalationRules": nuloString(reglas), "greeting": nuloString(saludo), "role": rol, "tools": herramientasActivas},
		"kb":      kb, "resources": []any{}, "llm": llm,
	})
}

func (s *Servidor) renderizarConocimiento(ctx context.Context, org string) (string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT kind,question,answer,content FROM kb_entry WHERE organization_id=? ORDER BY created_at,id`, org)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	partes := make([]string, 0)
	for rows.Next() {
		var tipo string
		var pregunta, respuesta, contenido sql.NullString
		if err := rows.Scan(&tipo, &pregunta, &respuesta, &contenido); err != nil {
			return "", err
		}
		if tipo == "qa" && pregunta.Valid && respuesta.Valid {
			partes = append(partes, "P: "+strings.TrimSpace(pregunta.String)+"\nR: "+strings.TrimSpace(respuesta.String))
		} else if tipo == "block" && contenido.Valid && strings.TrimSpace(contenido.String) != "" {
			partes = append(partes, strings.TrimSpace(contenido.String))
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if len(partes) == 0 {
		return "(knowledge base vacío)", nil
	}
	return strings.Join(partes, "\n\n"), nil
}
