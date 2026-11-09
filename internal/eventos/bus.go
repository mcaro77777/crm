package eventos

import (
	"sync"
	"time"
)

const historialMaximo = 256

type Evento struct {
	ID   int64  `json:"id"`
	Tipo string `json:"tipo"`
	Dato any    `json:"dato"`
}

type Bus struct {
	mu           sync.RWMutex
	suscriptores map[string]map[chan Evento]struct{}
	historial    map[string][]Evento
	ultimoID     int64
}

func NuevoBus() *Bus {
	return &Bus{suscriptores: make(map[string]map[chan Evento]struct{}), historial: make(map[string][]Evento)}
}

func (b *Bus) Publicar(organizacionID, tipo string, dato any) {
	b.mu.Lock()
	defer b.mu.Unlock()
	id := time.Now().UnixMilli()
	if id <= b.ultimoID {
		id = b.ultimoID + 1
	}
	b.ultimoID = id
	evento := Evento{ID: id, Tipo: tipo, Dato: dato}
	historial := append(b.historial[organizacionID], evento)
	if len(historial) > historialMaximo {
		historial = append([]Evento(nil), historial[len(historial)-historialMaximo:]...)
	}
	b.historial[organizacionID] = historial
	for canal := range b.suscriptores[organizacionID] {
		select {
		case canal <- evento:
		default:
			// El cliente hará catch-up por HTTP; nunca se bloquea una escritura.
		}
	}
}

func (b *Bus) Suscribir(organizacionID string) (<-chan Evento, func()) {
	canal, _, _, cancelar := b.suscribir(organizacionID, 0, false)
	return canal, cancelar
}

// SuscribirDesde registra al cliente y obtiene bajo el mismo lock los eventos
// posteriores al ID. No existe una ventana perdida entre catch-up y suscripcion.
func (b *Bus) SuscribirDesde(organizacionID string, desde int64) (<-chan Evento, []Evento, bool, func()) {
	return b.suscribir(organizacionID, desde, true)
}

func (b *Bus) suscribir(organizacionID string, desde int64, recuperar bool) (<-chan Evento, []Evento, bool, func()) {
	canal := make(chan Evento, 32)
	b.mu.Lock()
	if b.suscriptores[organizacionID] == nil {
		b.suscriptores[organizacionID] = make(map[chan Evento]struct{})
	}
	b.suscriptores[organizacionID][canal] = struct{}{}
	var pendientes []Evento
	perdido := false
	if recuperar {
		historial := b.historial[organizacionID]
		if len(historial) > 0 && desde > 0 && desde < historial[0].ID-1 {
			perdido = true
		}
		for _, evento := range historial {
			if evento.ID > desde {
				pendientes = append(pendientes, evento)
			}
		}
	}
	b.mu.Unlock()
	return canal, pendientes, perdido, func() {
		b.mu.Lock()
		delete(b.suscriptores[organizacionID], canal)
		if len(b.suscriptores[organizacionID]) == 0 {
			delete(b.suscriptores, organizacionID)
		}
		b.mu.Unlock()
	}
}
