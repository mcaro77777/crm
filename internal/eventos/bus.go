package eventos

import (
	"sync"
	"time"
)

type Evento struct {
	ID   int64  `json:"id"`
	Tipo string `json:"tipo"`
	Dato any    `json:"dato"`
}

type Bus struct {
	mu           sync.RWMutex
	suscriptores map[string]map[chan Evento]struct{}
}

func NuevoBus() *Bus {
	return &Bus{suscriptores: make(map[string]map[chan Evento]struct{})}
}

func (b *Bus) Publicar(organizacionID, tipo string, dato any) {
	evento := Evento{ID: time.Now().UnixMilli(), Tipo: tipo, Dato: dato}
	b.mu.RLock()
	defer b.mu.RUnlock()
	for canal := range b.suscriptores[organizacionID] {
		select {
		case canal <- evento:
		default:
			// El cliente hará catch-up por HTTP; nunca se bloquea una escritura.
		}
	}
}

func (b *Bus) Suscribir(organizacionID string) (<-chan Evento, func()) {
	canal := make(chan Evento, 32)
	b.mu.Lock()
	if b.suscriptores[organizacionID] == nil {
		b.suscriptores[organizacionID] = make(map[chan Evento]struct{})
	}
	b.suscriptores[organizacionID][canal] = struct{}{}
	b.mu.Unlock()
	return canal, func() {
		b.mu.Lock()
		delete(b.suscriptores[organizacionID], canal)
		if len(b.suscriptores[organizacionID]) == 0 {
			delete(b.suscriptores, organizacionID)
		}
		b.mu.Unlock()
	}
}
