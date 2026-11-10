package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"crm-api/internal/identificadores"
)

type mensajeCanalEntrante struct {
	Canal, CuentaID, Remitente, Nombre, ID, Texto, Tipo, Estado string
	Marca                                                       time.Time
}

type eventoMetaSocial struct {
	Entry []struct {
		ID        string `json:"id"`
		Messaging []struct {
			Sender struct {
				ID string `json:"id"`
			} `json:"sender"`
			Timestamp int64 `json:"timestamp"`
			Message   struct {
				Mid, Text string
				IsEcho    bool `json:"is_echo"`
			} `json:"message"`
			Delivery *struct {
				Mids []string `json:"mids"`
			} `json:"delivery"`
			Read *struct {
				Watermark int64 `json:"watermark"`
			} `json:"read"`
		} `json:"messaging"`
	} `json:"entry"`
}

func (s *Servidor) verificarWebhookSocial(w http.ResponseWriter, r *http.Request) {
	org, err := s.resolverOrganizacionWebhook(r.Context(), r.PathValue("token"))
	if err != nil || r.URL.Query().Get("hub.mode") != "subscribe" || !hmac.Equal([]byte(r.URL.Query().Get("hub.verify_token")), []byte(s.tokenWebhook(org))) {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(r.URL.Query().Get("hub.challenge")))
}

func (s *Servidor) recibirWebhookSocial(w http.ResponseWriter, r *http.Request) {
	org, err := s.resolverOrganizacionWebhook(r.Context(), r.PathValue("token"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	canal := r.PathValue("canal")
	if canal == "ig" {
		canal = "instagram"
	}
	cred, err := s.credencialSocial(r.Context(), org, canal)
	if err != nil {
		responderError(w, 409, "channel_not_configured", "Canal no configurado", nil)
		return
	}
	cuerpo, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		responderError(w, 400, "invalid_body", "No se pudo leer el webhook", nil)
		return
	}
	secreto := cred.WebhookSecret
	header := "X-Zernio-Signature"
	if cred.Fuente == "meta" {
		secreto, _ = s.leerSecreto(r.Context(), org, "meta_app_secret")
		header = "X-Hub-Signature-256"
	}
	if secreto != "" && !firmaWebhookValida(cuerpo, r.Header.Get(header), secreto) {
		responderError(w, 401, "invalid_signature", "Firma del webhook invalida", nil)
		return
	}
	muletilla := cred.Fuente + ":" + canal + ":"
	huella := sha256.Sum256(append([]byte(muletilla), cuerpo...))
	id, _ := identificadores.Nuevo("whe")
	ahora := time.Now().UTC()
	// Zernio usa una fuente común; el propio payload conserva la plataforma.
	fuente := "zernio"
	if cred.Fuente == "meta" && canal == "instagram" {
		fuente = "meta_instagram"
	} else if cred.Fuente == "meta" {
		fuente = "meta_messenger"
	}
	_, err = s.db.ExecContext(r.Context(), `INSERT IGNORE INTO webhook_event(id,organization_id,fuente,payload,huella,status,not_before,created_at,updated_at) VALUES (?,?,?,?,?,'pendiente',?,?,?)`, id, org, fuente, string(cuerpo), hex.EncodeToString(huella[:]), ahora, ahora, ahora)
	if err != nil {
		responderError(w, 503, "webhook_store_failed", "No se pudo guardar el evento", nil)
		return
	}
	responderJSON(w, 200, map[string]bool{"ok": true})
}

func firmaWebhookValida(cuerpo []byte, recibida, secreto string) bool {
	recibida = strings.TrimPrefix(strings.TrimSpace(recibida), "sha256=")
	mac := hmac.New(sha256.New, []byte(secreto))
	_, _ = mac.Write(cuerpo)
	esperada := hex.EncodeToString(mac.Sum(nil))
	return recibida != "" && hmac.Equal([]byte(recibida), []byte(esperada))
}

func (s *Servidor) procesarEventoMetaSocial(ctx context.Context, org, canal string, cuerpo []byte) error {
	var evento eventoMetaSocial
	if err := json.Unmarshal(cuerpo, &evento); err != nil {
		return err
	}
	for _, entry := range evento.Entry {
		for _, item := range entry.Messaging {
			if item.Delivery != nil {
				for _, mid := range item.Delivery.Mids {
					if err := s.actualizarEstadoMensaje(ctx, org, canal+":"+mid, "delivered"); err != nil {
						return err
					}
				}
			}
			if item.Message.IsEcho || item.Message.Mid == "" {
				continue
			}
			marca := time.UnixMilli(item.Timestamp).UTC()
			if item.Timestamp == 0 {
				marca = time.Now().UTC()
			}
			if err := s.guardarMensajeCanal(ctx, org, mensajeCanalEntrante{Canal: canal, CuentaID: entry.ID, Remitente: item.Sender.ID, Nombre: item.Sender.ID, ID: item.Message.Mid, Texto: item.Message.Text, Tipo: "text", Marca: marca}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Servidor) procesarEventoZernio(ctx context.Context, org string, cuerpo []byte) error {
	var raiz map[string]any
	if err := json.Unmarshal(cuerpo, &raiz); err != nil {
		return err
	}
	eventos := []map[string]any{raiz}
	if lista, ok := raiz["events"].([]any); ok {
		eventos = eventos[:0]
		for _, valor := range lista {
			if e, ok := valor.(map[string]any); ok {
				eventos = append(eventos, e)
			}
		}
	}
	for _, e := range eventos {
		canal := textoMapa(e, "platform", "channel", "canal")
		if canal == "ig" {
			canal = "instagram"
		}
		if canal != "instagram" && canal != "messenger" && canal != "tiktok" {
			return fmt.Errorf("plataforma Zernio no soportada: %s", canal)
		}
		remitente := textoMapa(e, "sender_id", "senderId", "from", "contact_id")
		id := textoMapa(e, "message_id", "messageId", "id")
		texto := textoMapa(e, "text", "message", "body")
		cuenta := textoMapa(e, "account_id", "accountId", "account_ref")
		estado := textoMapa(e, "status", "message_status")
		if estado != "" && id != "" {
			if estado == "seen" {
				estado = "read"
			}
			if err := s.actualizarEstadoMensaje(ctx, org, canal+":"+id, estado); err != nil {
				return err
			}
			if texto == "" {
				continue
			}
		}
		if remitente == "" || id == "" {
			continue
		}
		marca := time.Now().UTC()
		if valor := textoMapa(e, "timestamp", "created_at", "createdAt"); valor != "" {
			if n, err := strconv.ParseInt(valor, 10, 64); err == nil {
				if n > 1e12 {
					marca = time.UnixMilli(n).UTC()
				} else {
					marca = time.Unix(n, 0).UTC()
				}
			} else if t, err := time.Parse(time.RFC3339, valor); err == nil {
				marca = t.UTC()
			}
		}
		if err := s.guardarMensajeCanal(ctx, org, mensajeCanalEntrante{Canal: canal, CuentaID: cuenta, Remitente: remitente, Nombre: textoMapa(e, "sender_name", "senderName", "name"), ID: id, Texto: texto, Tipo: "text", Marca: marca}); err != nil {
			return err
		}
	}
	return nil
}

func textoMapa(m map[string]any, claves ...string) string {
	for _, clave := range claves {
		if v, ok := m[clave].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
		if v, ok := m[clave].(json.Number); ok {
			return v.String()
		}
		if v, ok := m[clave].(float64); ok {
			return strconv.FormatInt(int64(v), 10)
		}
	}
	return ""
}

func (s *Servidor) guardarMensajeCanal(ctx context.Context, org string, entrada mensajeCanalEntrante) error {
	if entrada.Marca.IsZero() {
		entrada.Marca = time.Now().UTC()
	}
	prefijo := map[string]string{"instagram": "ig:", "messenger": "fb:", "tiktok": "tt:"}[entrada.Canal]
	identidad := prefijo + entrada.Remitente
	externo := entrada.Canal + ":" + entrada.ID
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var duplicado int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM message WHERE wa_message_id=?`, externo).Scan(&duplicado); err != nil {
		return err
	}
	if duplicado > 0 {
		return nil
	}
	var contactoID string
	err = tx.QueryRowContext(ctx, `SELECT id FROM contact WHERE organization_id=? AND channel=? AND wa_identity=?`, org, entrada.Canal, identidad).Scan(&contactoID)
	ahora := time.Now().UTC()
	if errors.Is(err, sql.ErrNoRows) {
		contactoID, _ = identificadores.Nuevo("ct")
		nombre := strings.TrimSpace(entrada.Nombre)
		if nombre == "" {
			nombre = entrada.Remitente
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO contact(id,organization_id,channel,wa_identity,name,name_source,source,created_at,updated_at) VALUES (?,?,?,?,?,'perfil','organico',?,?)`, contactoID, org, entrada.Canal, identidad, nombre, ahora, ahora); err != nil {
			return err
		}
		var etapaID, etapaNombre, etapaTipo string
		if err = tx.QueryRowContext(ctx, `SELECT id,name,kind FROM pipeline_stage WHERE organization_id=? AND kind='open' ORDER BY position LIMIT 1`, org).Scan(&etapaID, &etapaNombre, &etapaTipo); err != nil {
			return err
		}
		leadID, _ := identificadores.Nuevo("ld")
		eventoID, _ := identificadores.Nuevo("lse")
		if _, err = tx.ExecContext(ctx, "INSERT INTO `lead`(id,organization_id,contact_id,stage_id,position,last_activity_at,created_at,updated_at) VALUES (?,?,?,?,0,?,?,?)", leadID, org, contactoID, etapaID, entrada.Marca, ahora, ahora); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO lead_stage_event(id,organization_id,lead_id,contact_id,to_stage_id,to_stage_name,to_stage_kind,occurred_at,source,created_at) VALUES (?,?,?,?,?,?,?,?,'sistema',?)`, eventoID, org, leadID, contactoID, etapaID, etapaNombre, etapaTipo, entrada.Marca, ahora); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	var convID string
	err = tx.QueryRowContext(ctx, `SELECT id FROM conversation WHERE organization_id=? AND contact_id=? AND is_test=0`, org, contactoID).Scan(&convID)
	if errors.Is(err, sql.ErrNoRows) {
		convID, _ = identificadores.Nuevo("cv")
		if _, err = tx.ExecContext(ctx, `INSERT INTO conversation(id,organization_id,contact_id,is_test,channel,channel_thread_ref,ai_enabled,last_inbound_at,last_message_at,unread_count,created_at,updated_at) VALUES (?,?,?,0,?,?,1,?,?,1,?,?)`, convID, org, contactoID, entrada.Canal, entrada.Remitente, entrada.Marca, entrada.Marca, ahora, ahora); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else if _, err = tx.ExecContext(ctx, `UPDATE conversation SET last_inbound_at=?,last_message_at=?,unread_count=unread_count+1,updated_at=? WHERE organization_id=? AND id=?`, entrada.Marca, entrada.Marca, ahora, org, convID); err != nil {
		return err
	}
	mensajeID, _ := identificadores.Nuevo("msg")
	if _, err = tx.ExecContext(ctx, `INSERT INTO message(id,organization_id,conversation_id,wa_message_id,direction,type,text,status,ai_generated,origin,created_at) VALUES (?,?,?,?,'in',?,?,'delivered',0,'manual',?)`, mensajeID, org, convID, externo, entrada.Tipo, entrada.Texto, ahora); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	s.bus.Publicar(org, "message.new", map[string]string{"conversationId": convID, "messageId": mensajeID})
	ctxDespacho, cancelar := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancelar()
	if err := s.encolarMensajeEntrante(ctxDespacho, org, convID, mensajeID, ahora); err != nil {
		s.logger.Error("mensaje social guardado sin despacho", "canal", entrada.Canal, "error", err)
	}
	return nil
}
