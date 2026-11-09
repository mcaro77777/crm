package despacho

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

type EstadoPublico struct {
	Estado         string     `json:"estado"`
	Etiqueta       string     `json:"etiqueta"`
	Detalle        string     `json:"detalle"`
	Intentos       int        `json:"intentos"`
	ProximoIntento *time.Time `json:"proximoIntento"`
	ActualizadoEn  time.Time  `json:"actualizadoEn"`
}

type estadoPersistido struct {
	Estado, UltimoError string
	Intentos            int
	NoAntes             time.Time
	ActualizadoEn       time.Time
}

// Estado devuelve únicamente información apta para la interfaz. El error
// persistido puede contener detalles técnicos y nunca sale de este paquete.
func (r *Repositorio) Estado(ctx context.Context, organizacionID, conversacionID string) (*EstadoPublico, error) {
	var e estadoPersistido
	var ultimo sql.NullString
	err := r.db.QueryRowContext(ctx, `SELECT status,attempts,not_before,last_error,updated_at FROM dispatch WHERE organization_id=? AND conversation_id=? ORDER BY (status IN ('pendiente','en_vuelo')) DESC,created_at DESC,updated_at DESC,id DESC LIMIT 1`, organizacionID, conversacionID).Scan(&e.Estado, &e.Intentos, &e.NoAntes, &ultimo, &e.ActualizadoEn)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if ultimo.Valid {
		e.UltimoError = ultimo.String
	}
	publico := estadoSeguro(e)
	return &publico, nil
}

func estadoSeguro(e estadoPersistido) EstadoPublico {
	resultado := EstadoPublico{Intentos: e.Intentos, ActualizadoEn: e.ActualizadoEn.UTC()}
	switch e.Estado {
	case "pendiente":
		if e.Intentos == 0 {
			resultado.Estado, resultado.Etiqueta = "esperando", "Esperando al agente"
			resultado.Detalle = "Estamos agrupando los mensajes antes de enviarlos."
		} else {
			resultado.Estado, resultado.Etiqueta = "reintentando", "Reintentando"
			resultado.Detalle = "El cerebro no respondió; volveremos a intentarlo automáticamente."
			proximo := e.NoAntes.UTC()
			resultado.ProximoIntento = &proximo
		}
	case "en_vuelo":
		resultado.Estado, resultado.Etiqueta = "procesando", "El agente está trabajando"
		resultado.Detalle = "El cerebro recibió el turno y está preparando una respuesta."
	case "entregado":
		resultado.Estado, resultado.Etiqueta = "entregado", "Turno entregado"
		resultado.Detalle = "El cerebro recibió correctamente los mensajes."
	case "caducado_a_humano":
		resultado.Estado, resultado.Etiqueta = "atencion_humana", "Atención humana requerida"
		resultado.Detalle = "No fue posible contactar al cerebro después de varios intentos."
	case "descartado":
		if strings.HasPrefix(e.UltimoError, "handoff:") {
			resultado.Estado, resultado.Etiqueta = "atencion_humana", "Atención humana activa"
			resultado.Detalle = "El despacho se canceló porque una persona tomó la conversación."
		} else {
			resultado.Estado, resultado.Etiqueta = "cancelado", "Despacho cancelado"
			resultado.Detalle = "El turno dejó de ser necesario antes de llegar al cerebro."
		}
	default:
		resultado.Estado, resultado.Etiqueta = "desconocido", "Estado no disponible"
		resultado.Detalle = "Actualiza la bandeja para consultar el estado nuevamente."
	}
	return resultado
}
