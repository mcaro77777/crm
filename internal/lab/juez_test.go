package lab

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestInterpretarResultadoNormalizaSalida(t *testing.T) {
	salida := "```json\n" + `{
		"puntuacion": 87,
		"veredicto": "rojo",
		"resumen": " Cumplio el objetivo ",
		"fortalezas": ["Escucho al cliente", "escucho al cliente", ""],
		"problemas": ["No confirmo el siguiente paso"],
		"recomendaciones": ["Confirmar fecha"]
	}` + "\n```"
	resultado, err := InterpretarResultado(salida)
	if err != nil {
		t.Fatal(err)
	}
	if resultado.Puntuacion != 87 || resultado.Veredicto != "verde" || resultado.Resumen != "Cumplio el objetivo" {
		t.Fatalf("resultado inesperado: %#v", resultado)
	}
	if len(resultado.Fortalezas) != 1 {
		t.Fatalf("no elimino duplicados: %#v", resultado.Fortalezas)
	}
}

func TestInterpretarResultadoRechazaSalidasInvalidas(t *testing.T) {
	casos := []string{
		`sin json`,
		`{"puntuacion":101,"resumen":"x","problemas":["x"]}`,
		`{"puntuacion":50,"resumen":"","problemas":["x"]}`,
		`{"puntuacion":50,"resumen":"x","fortalezas":[],"problemas":[],"recomendaciones":[]}`,
	}
	for _, caso := range casos {
		if _, err := InterpretarResultado(caso); !errors.Is(err, ErrSalidaInvalida) {
			t.Fatalf("debio rechazar %q: %v", caso, err)
		}
	}
}

func TestEvaluarConProveedorUsaSandboxYSalidaEstructurada(t *testing.T) {
	var solicitud solicitudChat
	proveedor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("ruta inesperada: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer secreto-prueba" {
			t.Error("no uso el token esperado")
		}
		if err := json.NewDecoder(r.Body).Decode(&solicitud); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"{\"puntuacion\":72,\"veredicto\":\"amarillo\",\"resumen\":\"Buena atencion con una omision\",\"fortalezas\":[\"Fue cordial\"],\"problemas\":[\"No pidio correo\"],\"recomendaciones\":[\"Solicitar el correo\"]}"}}]}`)
	}))
	defer proveedor.Close()

	entrada := EntradaJuez{
		Persona:       "Cliente apurado",
		Objetivo:      "Calificar el interes",
		Criterios:     []string{"Pedir correo"},
		Transcripcion: []MensajeEvaluado{{Rol: "cliente", Contenido: "Necesito precio"}, {Rol: "agente", Contenido: "Claro"}},
	}
	resultado, estado, err := evaluarConProveedor(context.Background(), proveedor.Client(), credencialJuez{
		baseURL: proveedor.URL, modelo: "modelo-juez", token: "secreto-prueba",
	}, entrada)
	if err != nil {
		t.Fatal(err)
	}
	if estado != 200 || resultado.Puntuacion != 72 || resultado.Veredicto != "amarillo" {
		t.Fatalf("resultado inesperado: estado=%d resultado=%#v", estado, resultado)
	}
	if solicitud.Modelo != "modelo-juez" || solicitud.FormatoRespuesta["type"] != "json_object" {
		t.Fatalf("solicitud no estructurada: %#v", solicitud)
	}
	if len(solicitud.Mensajes) != 2 || !strings.Contains(solicitud.Mensajes[0].Contenido, "simulacion aislada") || !strings.Contains(solicitud.Mensajes[0].Contenido, "no envies mensajes") {
		t.Fatalf("faltan guardas de sandbox: %#v", solicitud.Mensajes)
	}
}

func TestEvaluarConProveedorNoFiltraSecretoEnErrores(t *testing.T) {
	const secreto = "token-que-jamas-debe-salir"
	proveedor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `error interno que podria contener `+secreto)
	}))
	defer proveedor.Close()

	_, estado, err := evaluarConProveedor(context.Background(), proveedor.Client(), credencialJuez{
		baseURL: proveedor.URL, modelo: "m", token: secreto,
	}, EntradaJuez{Persona: "p", Objetivo: "o"})
	if estado != http.StatusInternalServerError || !errors.Is(err, ErrProveedor) {
		t.Fatalf("error inesperado: estado=%d err=%v", estado, err)
	}
	if strings.Contains(err.Error(), secreto) {
		t.Fatal("el error filtro el secreto")
	}
}

func TestReintentoDelJuezSoloParaFallosTransitorios(t *testing.T) {
	casos := []struct {
		estado int
		err    error
		espera bool
	}{
		{estado: 0, err: ErrSalidaInvalida, espera: true},
		{estado: 429, err: ErrProveedor, espera: true},
		{estado: 503, err: ErrProveedor, espera: true},
		{estado: 401, err: ErrProveedor, espera: false},
		{estado: 400, err: ErrProveedor, espera: false},
	}
	for _, caso := range casos {
		if obtenido := reintentableJuez(caso.estado, caso.err); obtenido != caso.espera {
			t.Fatalf("estado=%d err=%v: esperado %v, obtenido %v", caso.estado, caso.err, caso.espera, obtenido)
		}
	}
}

func TestCompararResultados(t *testing.T) {
	anterior := ResultadoJuez{Puntuacion: 58, Problemas: []string{"No saludo", "No confirmo fecha"}}
	actual := ResultadoJuez{Puntuacion: 82, Problemas: []string{"No confirmo fecha", "Respuesta extensa"}}
	comparacion := CompararResultados(anterior, actual)
	if comparacion.Delta != 24 || comparacion.Tendencia != "mejora" {
		t.Fatalf("comparacion incorrecta: %#v", comparacion)
	}
	if len(comparacion.ProblemasResueltos) != 1 || comparacion.ProblemasResueltos[0] != "No saludo" {
		t.Fatalf("resueltos incorrectos: %#v", comparacion.ProblemasResueltos)
	}
	if len(comparacion.ProblemasNuevos) != 1 || comparacion.ProblemasNuevos[0] != "Respuesta extensa" {
		t.Fatalf("nuevos incorrectos: %#v", comparacion.ProblemasNuevos)
	}
}

func TestVeredictoParaRespetaUmbrales(t *testing.T) {
	casos := map[int]string{0: "rojo", 59: "rojo", 60: "amarillo", 84: "amarillo", 85: "verde", 100: "verde"}
	for puntuacion, esperado := range casos {
		if obtenido := VeredictoPara(puntuacion); obtenido != esperado {
			t.Fatalf("%d: esperado %s, obtenido %s", puntuacion, esperado, obtenido)
		}
	}
}
