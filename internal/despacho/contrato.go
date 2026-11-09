// Package despacho define el contrato publico entre Nexora y un cerebro externo.
package despacho

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const (
	CabeceraFirma        = "X-Vocero-Signature"
	CabeceraDespacho     = "X-Vocero-Dispatch-Id"
	CabeceraOrganizacion = "X-Vocero-Organization"
)

// Evento es la forma versionable que recibe el cerebro. El orden de los campos
// es parte del contrato porque la firma se calcula sobre el JSON crudo.
type Evento struct {
	DespachoID    string       `json:"dispatchId"`
	Organizacion  Organizacion `json:"organization"`
	Conversacion  Conversacion `json:"conversation"`
	Contacto      Contacto     `json:"contact"`
	Mensajes      []Mensaje    `json:"messages"`
	PrimerMensaje string       `json:"firstMessageAt"`
	UltimoMensaje string       `json:"lastMessageAt"`
}

type Organizacion struct {
	ID   string  `json:"id"`
	Slug *string `json:"slug"`
}

type Conversacion struct {
	ID              string  `json:"id"`
	Canal           string  `json:"channel"`
	IAHabilitada    bool    `json:"aiEnabled"`
	VentanaAbierta  bool    `json:"windowOpen"`
	VentanaExpiraEn *string `json:"windowExpiresAt"`
	Prueba          bool    `json:"test,omitempty"`
}

type Contacto struct {
	ID        string `json:"id"`
	Nombre    string `json:"displayName"`
	Identidad string `json:"identity"`
}

// Mensaje mantiene siempre id, at, type, text y mediaId. Los datos adicionales
// solo aparecen cuando corresponden. Los dobles punteros permiten distinguir
// entre una llave ausente y una llave presente con valor null.
type Mensaje struct {
	ID        string               `json:"id"`
	En        string               `json:"at"`
	Tipo      string               `json:"type"`
	Texto     *string              `json:"text"`
	MedioID   *string              `json:"mediaId"`
	MIME      **string             `json:"mimeType,omitempty"`
	Leyenda   **string             `json:"caption,omitempty"`
	Archivo   *string              `json:"fileName,omitempty"`
	Ubicacion *Ubicacion           `json:"location,omitempty"`
	Contactos []ContactoCompartido `json:"contacts,omitempty"`
}

type Ubicacion struct {
	Latitud   float64 `json:"latitude"`
	Longitud  float64 `json:"longitude"`
	Nombre    string  `json:"name,omitempty"`
	Direccion string  `json:"address,omitempty"`
}

type ContactoCompartido struct {
	Nombre string `json:"name"`
}

type DatosEvento struct {
	DespachoID       string
	OrganizacionID   string
	OrganizacionSlug *string
	ConversacionID   string
	Canal            string
	IAHabilitada     bool
	VentanaAbierta   bool
	VentanaExpiraEn  *time.Time
	Prueba           bool
	ContactoID       string
	NombreContacto   string
	Identidad        string
	Mensajes         []MensajeEntrada
	PrimerMensaje    time.Time
	UltimoMensaje    time.Time
}

type MensajeEntrada struct {
	ID        string
	CreadoEn  time.Time
	Tipo      string
	Texto     *string
	Medio     *MedioEntrada
	Ubicacion *Ubicacion
	Contactos []ContactoCompartido
}

type MedioEntrada struct {
	ID      *string
	MIME    *string
	Leyenda *string
	Archivo *string
}

// ArmarEvento es la unica puerta para construir un despacho. Rechaza eventos
// vacios para que el cerebro nunca reciba un turno sin mensajes.
func ArmarEvento(d DatosEvento) (Evento, error) {
	if d.DespachoID == "" || d.OrganizacionID == "" || d.ConversacionID == "" || d.ContactoID == "" || d.Identidad == "" {
		return Evento{}, errors.New("faltan identificadores obligatorios del despacho")
	}
	if len(d.Mensajes) == 0 {
		return Evento{}, errors.New("un despacho debe contener al menos un mensaje")
	}
	if d.PrimerMensaje.IsZero() || d.UltimoMensaje.IsZero() || d.UltimoMensaje.Before(d.PrimerMensaje) {
		return Evento{}, errors.New("el rango de mensajes del despacho es invalido")
	}

	var vencimiento *string
	if d.VentanaExpiraEn != nil {
		valor := fechaContrato(*d.VentanaExpiraEn)
		vencimiento = &valor
	}
	mensajes := make([]Mensaje, 0, len(d.Mensajes))
	for _, entrada := range d.Mensajes {
		if entrada.ID == "" || entrada.Tipo == "" || entrada.CreadoEn.IsZero() {
			return Evento{}, errors.New("un mensaje del despacho esta incompleto")
		}
		mensaje := Mensaje{
			ID: entrada.ID, En: fechaContrato(entrada.CreadoEn), Tipo: entrada.Tipo,
			Texto: entrada.Texto, Ubicacion: entrada.Ubicacion, Contactos: entrada.Contactos,
		}
		if entrada.Medio != nil {
			mensaje.MedioID = entrada.Medio.ID
			if entrada.Medio.ID != nil {
				mensaje.MIME = presente(entrada.Medio.MIME)
				mensaje.Leyenda = presente(entrada.Medio.Leyenda)
				mensaje.Archivo = entrada.Medio.Archivo
			}
		}
		mensajes = append(mensajes, mensaje)
	}

	return Evento{
		DespachoID:   d.DespachoID,
		Organizacion: Organizacion{ID: d.OrganizacionID, Slug: d.OrganizacionSlug},
		Conversacion: Conversacion{
			ID: d.ConversacionID, Canal: d.Canal, IAHabilitada: d.IAHabilitada,
			VentanaAbierta: d.VentanaAbierta, VentanaExpiraEn: vencimiento, Prueba: d.Prueba,
		},
		Contacto: Contacto{ID: d.ContactoID, Nombre: d.NombreContacto, Identidad: d.Identidad},
		Mensajes: mensajes, PrimerMensaje: fechaContrato(d.PrimerMensaje), UltimoMensaje: fechaContrato(d.UltimoMensaje),
	}, nil
}

func presente(valor *string) **string { return &valor }

// fechaContrato usa UTC y siempre conserva milisegundos, igual que ISOString.
func fechaContrato(valor time.Time) string {
	return valor.UTC().Format("2006-01-02T15:04:05.000Z")
}

// SerializarYFirmar serializa exactamente una vez y firma esos mismos bytes.
func SerializarYFirmar(evento Evento, secreto string) ([]byte, string, error) {
	if len(secreto) < 16 {
		return nil, "", errors.New("la API key del cerebro debe tener al menos 16 caracteres")
	}
	cuerpo, err := json.Marshal(evento)
	if err != nil {
		return nil, "", fmt.Errorf("serializar despacho: %w", err)
	}
	return cuerpo, FirmarCuerpo(cuerpo, secreto), nil
}

func FirmarCuerpo(cuerpo []byte, secreto string) string {
	mac := hmac.New(sha256.New, []byte(secreto))
	_, _ = mac.Write(cuerpo)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func FirmaValida(cuerpo []byte, secreto, firma string) bool {
	esperada := FirmarCuerpo(cuerpo, secreto)
	return len(firma) == len(esperada) && hmac.Equal([]byte(firma), []byte(esperada))
}

type ResultadoRespuesta struct {
	Correcta     bool
	Reintentable bool
}

// ClasificarRespuesta aplica el contrato: 2xx entrega; 429 y 5xx reintentan;
// cualquier otro codigo, incluidas redirecciones, cae a humano sin reintentar.
func ClasificarRespuesta(estado int) ResultadoRespuesta {
	if estado >= 200 && estado < 300 {
		return ResultadoRespuesta{Correcta: true}
	}
	return ResultadoRespuesta{Reintentable: estado == 429 || estado >= 500}
}
