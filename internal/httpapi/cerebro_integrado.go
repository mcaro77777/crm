package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"crm-api/internal/cerebro"
	"crm-api/internal/despacho"
)

// procesarTurnoIntegrado ejecuta el razonamiento dentro del proceso Go. El
// outbox conserva la exclusión por conversación y este método solo aplica el
// resultado del modelo mediante las mismas guardas usadas por la API humana.
func (s *Servidor) procesarTurnoIntegrado(ctx context.Context, evento despacho.Evento) despacho.ResultadoEntrega {
	mensajes := make([]cerebro.Mensaje, 0, len(evento.Mensajes))
	for _, mensaje := range evento.Mensajes {
		mensajes = append(mensajes, cerebro.Mensaje{ID: mensaje.ID, Tipo: mensaje.Tipo, Texto: mensaje.Texto})
	}

	resultado, err := s.cerebro.Procesar(ctx, cerebro.Entrada{
		OrganizacionID: evento.Organizacion.ID,
		ConversacionID: evento.Conversacion.ID,
		Canal:          evento.Conversacion.Canal,
		ContactoID:     evento.Contacto.ID,
		ContactoNombre: evento.Contacto.Nombre,
		Identidad:      evento.Contacto.Identidad,
		Mensajes:       mensajes,
	})
	if err != nil {
		s.logger.Warn("fallo del cerebro integrado", "organizacion", evento.Organizacion.ID, "conversacion", evento.Conversacion.ID, "error", err)
		return despacho.ResultadoEntrega{Reintentable: cerebro.EsReintentable(err), Detalle: "el cerebro integrado no pudo completar el turno"}
	}

	if resultado.Accion == "handoff" {
		estado, cambio, err := s.handoffs.Tomar(ctx, evento.Organizacion.ID, evento.Conversacion.ID, resultado.Motivo, time.Now().UTC())
		if err != nil {
			return despacho.ResultadoEntrega{Reintentable: true, Detalle: "no se pudo transferir la conversacion"}
		}
		s.publicarResponsable(evento.Organizacion.ID, evento.Conversacion.ID, estado, cambio)
		return despacho.ResultadoEntrega{Correcta: true}
	}

	_, err = s.enviarTextoConversacion(ctx, evento.Organizacion.ID, evento.Conversacion.ID, resultado.Mensaje, true)
	if err == nil {
		return despacho.ResultadoEntrega{Correcta: true}
	}
	var envio *errorEnvio
	if errors.As(err, &envio) {
		return despacho.ResultadoEntrega{Reintentable: envio.estado == http.StatusTooManyRequests || envio.estado >= http.StatusInternalServerError, Detalle: "no se pudo enviar la respuesta del cerebro integrado"}
	}
	return despacho.ResultadoEntrega{Reintentable: true, Detalle: "no se pudo guardar la respuesta del cerebro integrado"}
}
