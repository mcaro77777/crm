package httpapi

import (
	"net"
	"net/http"
	"sync"
	"time"
)

type ventanaIntentos struct {
	inicio time.Time
	cuenta int
}

type limitadorIntentos struct {
	mu       sync.Mutex
	maximo   int
	ventana  time.Duration
	intentos map[string]ventanaIntentos
}

func nuevoLimitadorIntentos(maximo int, ventana time.Duration) *limitadorIntentos {
	return &limitadorIntentos{maximo: maximo, ventana: ventana, intentos: make(map[string]ventanaIntentos)}
}

func (l *limitadorIntentos) permitir(clave string, ahora time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	v := l.intentos[clave]
	if v.inicio.IsZero() || ahora.Sub(v.inicio) >= l.ventana {
		l.intentos[clave] = ventanaIntentos{inicio: ahora, cuenta: 1}
		return true
	}
	if v.cuenta >= l.maximo {
		return false
	}
	v.cuenta++
	l.intentos[clave] = v
	return true
}

func (s *Servidor) limitarAutenticacion(siguiente http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		if !s.limite.permitir(r.URL.Path+":"+host, time.Now()) {
			w.Header().Set("Retry-After", "600")
			responderError(w, http.StatusTooManyRequests, "rate_limited", "Demasiados intentos; espere unos minutos", nil)
			return
		}
		siguiente.ServeHTTP(w, r)
	})
}
