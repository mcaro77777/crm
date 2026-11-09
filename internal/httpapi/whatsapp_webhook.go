package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"crm-api/internal/identificadores"
)

type eventoMeta struct {
	Entry []struct {
		Changes []struct {
			Value struct {
				Metadata struct {
					PhoneNumberID string `json:"phone_number_id"`
				} `json:"metadata"`
				Contacts []struct {
					Profile struct {
						Name string `json:"name"`
					} `json:"profile"`
					WAID string `json:"wa_id"`
				} `json:"contacts"`
				Messages []mensajeMeta                            `json:"messages"`
				Statuses []struct{ ID, Status, Timestamp string } `json:"statuses"`
			} `json:"value"`
		} `json:"changes"`
	} `json:"entry"`
}

type mensajeMeta struct {
	From      string `json:"from"`
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
	Type      string `json:"type"`
	Text      struct {
		Body string `json:"body"`
	} `json:"text"`
	Referral *struct {
		SourceID string `json:"source_id"`
		Headline string `json:"headline"`
		Body     string `json:"body"`
		CTWAClid string `json:"ctwa_clid"`
	} `json:"referral"`
}

func (s *Servidor) verificarWebhookWhatsApp(w http.ResponseWriter, r *http.Request) {
	organizacionID, err := s.resolverOrganizacionWebhook(r.Context(), r.PathValue("token"))
	if err != nil || r.URL.Query().Get("hub.mode") != "subscribe" || !hmac.Equal([]byte(r.URL.Query().Get("hub.verify_token")), []byte(s.tokenWebhook(organizacionID))) {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(r.URL.Query().Get("hub.challenge")))
}

func (s *Servidor) recibirWebhookWhatsApp(w http.ResponseWriter, r *http.Request) {
	organizacionID, err := s.resolverOrganizacionWebhook(r.Context(), r.PathValue("token"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	cuerpo, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		responderError(w, 400, "invalid_body", "No se pudo leer el webhook", nil)
		return
	}
	if secreto, errSecreto := s.leerSecreto(r.Context(), organizacionID, "meta_app_secret"); errSecreto == nil && secreto != "" {
		firma := strings.TrimPrefix(r.Header.Get("X-Hub-Signature-256"), "sha256=")
		mac := hmac.New(sha256.New, []byte(secreto))
		_, _ = mac.Write(cuerpo)
		esperada := hex.EncodeToString(mac.Sum(nil))
		if firma == "" || !hmac.Equal([]byte(firma), []byte(esperada)) {
			responderError(w, 401, "invalid_signature", "Firma de Meta invalida", nil)
			return
		}
	}
	huella := sha256.Sum256(cuerpo)
	id, _ := identificadores.Nuevo("whe")
	ahora := time.Now().UTC()
	_, err = s.db.ExecContext(r.Context(), `INSERT IGNORE INTO webhook_event (id,organization_id,fuente,payload,huella,status,not_before,created_at,updated_at) VALUES (?,?, 'whatsapp', ?,?,'pendiente',?,?,?)`, id, organizacionID, string(cuerpo), hex.EncodeToString(huella[:]), ahora, ahora, ahora)
	if err != nil {
		responderError(w, 503, "webhook_store_failed", "No se pudo guardar el evento", nil)
		return
	}
	responderJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Servidor) procesarEventoWhatsApp(ctx context.Context, organizacionID string, cuerpo []byte) error {
	var evento eventoMeta
	if err := json.Unmarshal(cuerpo, &evento); err != nil {
		return err
	}
	for _, entrada := range evento.Entry {
		for _, cambio := range entrada.Changes {
			valor := cambio.Value
			for _, estado := range valor.Statuses {
				if err := s.actualizarEstadoMensaje(ctx, organizacionID, estado.ID, estado.Status); err != nil {
					return err
				}
			}
			for _, mensaje := range valor.Messages {
				nombre := mensaje.From
				for _, contacto := range valor.Contacts {
					if contacto.WAID == mensaje.From && contacto.Profile.Name != "" {
						nombre = contacto.Profile.Name
					}
				}
				if err := s.guardarMensajeEntrante(ctx, organizacionID, nombre, mensaje); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (s *Servidor) guardarMensajeEntrante(ctx context.Context, organizacionID, nombre string, mensaje mensajeMeta) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var contactoID string
	err = tx.QueryRowContext(ctx, "SELECT id FROM contact WHERE organization_id=? AND channel='whatsapp' AND wa_identity=?", organizacionID, mensaje.From).Scan(&contactoID)
	ahora := time.Now().UTC()
	marca := ahora
	if segundos, e := strconv.ParseInt(mensaje.Timestamp, 10, 64); e == nil {
		marca = time.Unix(segundos, 0).UTC()
	}
	if err == sql.ErrNoRows {
		contactoID, _ = identificadores.Nuevo("ct")
		fuente := "organico"
		if mensaje.Referral != nil {
			fuente = "anuncio"
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO contact (id,organization_id,channel,wa_identity,phone,wa_user_id,name,name_source,source,created_at,updated_at) VALUES (?,?,'whatsapp',?,?,?,?,'perfil',?,?,?)", contactoID, organizacionID, mensaje.From, mensaje.From, mensaje.From, nombre, fuente, ahora, ahora)
		if err != nil {
			return err
		}
		var etapaID, etapaNombre, etapaTipo string
		if err = tx.QueryRowContext(ctx, "SELECT id,name,kind FROM pipeline_stage WHERE organization_id=? AND kind='open' ORDER BY position LIMIT 1", organizacionID).Scan(&etapaID, &etapaNombre, &etapaTipo); err != nil {
			return err
		}
		leadID, _ := identificadores.Nuevo("ld")
		eventoID, _ := identificadores.Nuevo("lse")
		if _, err = tx.ExecContext(ctx, "INSERT INTO `lead` (id,organization_id,contact_id,stage_id,position,last_activity_at,created_at,updated_at) VALUES (?,?,?,?,0,?,?,?)", leadID, organizacionID, contactoID, etapaID, marca, ahora, ahora); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO `lead_stage_event` (id,organization_id,lead_id,contact_id,to_stage_id,to_stage_name,to_stage_kind,occurred_at,source,created_at) VALUES (?,?,?,?,?,?,?,?, 'sistema',?)", eventoID, organizacionID, leadID, contactoID, etapaID, etapaNombre, etapaTipo, marca, ahora); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	var conversacionID string
	err = tx.QueryRowContext(ctx, "SELECT id FROM conversation WHERE organization_id=? AND contact_id=? AND is_test=0", organizacionID, contactoID).Scan(&conversacionID)
	if err == sql.ErrNoRows {
		conversacionID, _ = identificadores.Nuevo("cv")
		if _, err = tx.ExecContext(ctx, "INSERT INTO conversation (id,organization_id,contact_id,is_test,channel,ai_enabled,last_inbound_at,last_message_at,unread_count,created_at,updated_at) VALUES (?,?,?,0,'whatsapp',1,?,?,1,?,?)", conversacionID, organizacionID, contactoID, marca, marca, ahora, ahora); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else {
		if _, err = tx.ExecContext(ctx, "UPDATE conversation SET last_inbound_at=?,last_message_at=?,unread_count=unread_count+1,updated_at=? WHERE id=? AND organization_id=?", marca, marca, ahora, conversacionID, organizacionID); err != nil {
			return err
		}
	}
	mensajeID, _ := identificadores.Nuevo("msg")
	texto := mensaje.Text.Body
	if mensaje.Type != "text" {
		texto = ""
	}
	resultado, err := tx.ExecContext(ctx, "INSERT IGNORE INTO message (id,organization_id,conversation_id,wa_message_id,direction,type,text,status,ai_generated,origin,wa_timestamp,created_at) VALUES (?,?,?,?,'in',?,?,'delivered',0,'manual',?,?)", mensajeID, organizacionID, conversacionID, mensaje.ID, mensaje.Type, texto, marca, ahora)
	if err != nil {
		return err
	}
	filas, _ := resultado.RowsAffected()
	if filas == 0 {
		// El wa_message_id ya existe. El rollback revierte también el aumento
		// de no leídos y cualquier actualización de actividad realizada arriba.
		return nil
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	s.bus.Publicar(organizacionID, "message.new", map[string]string{"conversationId": conversacionID, "messageId": mensajeID})
	ctxDespacho, cancelar := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancelar()
	if err := s.encolarMensajeEntrante(ctxDespacho, organizacionID, conversacionID, mensajeID, ahora); err != nil {
		// Bandeja primero: un fallo posterior al commit nunca convierte el
		// webhook en error ni hace desaparecer el mensaje ya guardado.
		s.logger.Error("mensaje guardado sin poder encolar despacho", "organizacion", organizacionID, "conversacion", conversacionID, "error", err)
	}
	return nil
}

func (s *Servidor) actualizarEstadoMensaje(ctx context.Context, organizacionID, waID, estado string) error {
	orden := map[string]int{"pending": 0, "sent": 1, "delivered": 2, "read": 3, "failed": 4}
	if _, ok := orden[estado]; !ok {
		return nil
	}
	var actual string
	err := s.db.QueryRowContext(ctx, "SELECT status FROM message WHERE organization_id=? AND wa_message_id=?", organizacionID, waID).Scan(&actual)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	if estado != "failed" && orden[estado] < orden[actual] {
		return nil
	}
	_, err = s.db.ExecContext(ctx, "UPDATE message SET status=? WHERE organization_id=? AND wa_message_id=?", estado, organizacionID, waID)
	if err == nil {
		s.bus.Publicar(organizacionID, "message.status", map[string]string{"waMessageId": waID, "status": estado})
	}
	return err
}
