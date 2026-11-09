package despacho

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrConversacionAusente = errors.New("la conversacion ya no existe")
	ErrIAPausada           = errors.New("la IA se pauso antes de despachar")
	ErrSinMensajes         = errors.New("sin mensajes por despachar")
	ErrSinDestino          = errors.New("el cerebro no esta configurado")
)

type datosConversacion struct {
	OrgID, ConvID, Canal, ContactoID, Nombre, Identidad string
	Slug                                                sql.NullString
	IAHabilitada, Prueba                                bool
	Handoff                                             sql.NullTime
	UltimoEntrante                                      sql.NullTime
}

type filaMensaje struct {
	ID, Direccion, Tipo                        string
	Texto                                      sql.NullString
	IAGenerada                                 bool
	CreadoEn                                   time.Time
	MedioID, WAMedioID, MIME, Archivo, Leyenda sql.NullString
	Payload                                    []byte
}

func ConstruirEvento(ctx context.Context, db *sql.DB, fila Fila, ahora time.Time) (Evento, error) {
	var d datosConversacion
	err := db.QueryRowContext(ctx, `SELECT o.id,o.slug,cv.id,cv.channel,cv.ai_enabled,cv.is_test,cv.handoff_at,cv.last_inbound_at,c.id,c.name,c.wa_identity FROM conversation cv JOIN organization o ON o.id=cv.organization_id JOIN contact c ON c.id=cv.contact_id AND c.organization_id=cv.organization_id WHERE cv.id=? AND cv.organization_id=?`, fila.ConversacionID, fila.OrganizacionID).Scan(&d.OrgID, &d.Slug, &d.ConvID, &d.Canal, &d.IAHabilitada, &d.Prueba, &d.Handoff, &d.UltimoEntrante, &d.ContactoID, &d.Nombre, &d.Identidad)
	if errors.Is(err, sql.ErrNoRows) {
		return Evento{}, ErrConversacionAusente
	}
	if err != nil {
		return Evento{}, err
	}
	if !d.IAHabilitada || d.Handoff.Valid {
		return Evento{}, ErrIAPausada
	}

	filas, err := leerMensajes(ctx, db, fila)
	if err != nil {
		return Evento{}, err
	}
	entrantes := seleccionarRafaga(filas, fila.MensajeIDs)
	if len(entrantes) == 0 {
		return Evento{}, ErrSinMensajes
	}
	mensajes := make([]MensajeEntrada, 0, len(entrantes))
	for _, m := range entrantes {
		entrada := MensajeEntrada{ID: m.ID, CreadoEn: m.CreadoEn, Tipo: m.Tipo}
		if m.Texto.Valid {
			entrada.Texto = &m.Texto.String
		}
		if m.MedioID.Valid {
			medio := MedioEntrada{}
			if m.WAMedioID.Valid {
				medio.ID = &m.WAMedioID.String
			}
			if m.MIME.Valid {
				medio.MIME = &m.MIME.String
			}
			if m.Archivo.Valid {
				medio.Archivo = &m.Archivo.String
			}
			if m.Leyenda.Valid {
				medio.Leyenda = &m.Leyenda.String
			}
			entrada.Medio = &medio
			aplicarPayload(&entrada, m.Payload)
		}
		mensajes = append(mensajes, entrada)
	}
	var slug *string
	if d.Slug.Valid {
		slug = &d.Slug.String
	}
	var vence *time.Time
	ventanaAbierta := false
	if d.UltimoEntrante.Valid {
		v := d.UltimoEntrante.Time.UTC().Add(24 * time.Hour)
		vence = &v
		ventanaAbierta = ahora.Before(v)
	}
	return ArmarEvento(DatosEvento{
		DespachoID: fila.ID, OrganizacionID: d.OrgID, OrganizacionSlug: slug,
		ConversacionID: d.ConvID, Canal: d.Canal, IAHabilitada: d.IAHabilitada,
		VentanaAbierta: ventanaAbierta, VentanaExpiraEn: vence, Prueba: d.Prueba,
		ContactoID: d.ContactoID, NombreContacto: d.Nombre, Identidad: d.Identidad,
		Mensajes: mensajes, PrimerMensaje: fila.PrimerMensaje, UltimoMensaje: fila.UltimoMensaje,
	})
}

func leerMensajes(ctx context.Context, db *sql.DB, fila Fila) ([]filaMensaje, error) {
	rows, err := db.QueryContext(ctx, `SELECT m.id,m.direction,m.type,m.text,m.ai_generated,m.created_at,ma.id,ma.wa_media_id,ma.mime_type,ma.file_name,ma.caption,ma.payload FROM message m LEFT JOIN media_asset ma ON ma.id=m.media_asset_id AND ma.organization_id=m.organization_id WHERE m.organization_id=? AND m.conversation_id=? ORDER BY m.created_at DESC LIMIT 50`, fila.OrganizacionID, fila.ConversacionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	invertidas := make([]filaMensaje, 0, 50)
	for rows.Next() {
		var m filaMensaje
		if err := rows.Scan(&m.ID, &m.Direccion, &m.Tipo, &m.Texto, &m.IAGenerada, &m.CreadoEn, &m.MedioID, &m.WAMedioID, &m.MIME, &m.Archivo, &m.Leyenda, &m.Payload); err != nil {
			return nil, err
		}
		invertidas = append(invertidas, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i, j := 0, len(invertidas)-1; i < j; i, j = i+1, j-1 {
		invertidas[i], invertidas[j] = invertidas[j], invertidas[i]
	}
	return invertidas, nil
}

func seleccionarRafaga(filas []filaMensaje, debidos []string) []filaMensaje {
	ultimoSaliente := -1
	ultimoHumano := time.Time{}
	for i, m := range filas {
		if m.Direccion == "out" {
			ultimoSaliente = i
			if !m.IAGenerada && m.CreadoEn.After(ultimoHumano) {
				ultimoHumano = m.CreadoEn
			}
		}
	}
	debe := make(map[string]struct{}, len(debidos))
	for _, id := range debidos {
		debe[id] = struct{}{}
	}
	resultado := make([]filaMensaje, 0, 20)
	vistos := map[string]struct{}{}
	for i, m := range filas {
		if m.Direccion != "in" {
			continue
		}
		_, anotado := debe[m.ID]
		if i <= ultimoSaliente && (!anotado || (!ultimoHumano.IsZero() && !m.CreadoEn.After(ultimoHumano))) {
			continue
		}
		if _, existe := vistos[m.ID]; existe {
			continue
		}
		vistos[m.ID] = struct{}{}
		resultado = append(resultado, m)
	}
	if len(resultado) > 20 {
		resultado = resultado[len(resultado)-20:]
	}
	return resultado
}

func aplicarPayload(entrada *MensajeEntrada, raw []byte) {
	if len(raw) == 0 {
		return
	}
	if entrada.Tipo == "location" {
		var p struct {
			Latitude, Longitude float64
			Name, Address       string
		}
		if json.Unmarshal(raw, &p) == nil {
			entrada.Ubicacion = &Ubicacion{Latitud: p.Latitude, Longitud: p.Longitude, Nombre: strings.TrimSpace(p.Name), Direccion: strings.TrimSpace(p.Address)}
		}
	}
	if entrada.Tipo == "contacts" {
		var p []struct {
			Name struct {
				FormattedName string `json:"formatted_name"`
				FirstName     string `json:"first_name"`
			} `json:"name"`
		}
		if json.Unmarshal(raw, &p) == nil {
			for _, c := range p {
				nombre := strings.TrimSpace(c.Name.FormattedName)
				if nombre == "" {
					nombre = strings.TrimSpace(c.Name.FirstName)
				}
				if nombre != "" {
					entrada.Contactos = append(entrada.Contactos, ContactoCompartido{Nombre: nombre})
				}
			}
		}
	}
}

func DetalleError(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("%v", err)
}
