package despacho

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestEntregarMandaElCuerpoFirmadoYLasCabeceras(t *testing.T) {
	evento := eventoPrueba(t)
	var cuerpo []byte
	servidor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		cuerpo, err = io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		if r.Header.Get(CabeceraDespacho) != evento.DespachoID {
			t.Error("dispatch id incorrecto")
		}
		if r.Header.Get(CabeceraOrganizacion) != "mi-negocio" {
			t.Error("organizacion incorrecta")
		}
		if !FirmaValida(cuerpo, secretoPrueba, r.Header.Get(CabeceraFirma)) {
			t.Error("firma invalida")
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer servidor.Close()

	resultado := NuevoEntregador().Entregar(context.Background(), evento, Destino{URL: servidor.URL, Secreto: secretoPrueba})
	if !resultado.Correcta {
		t.Fatalf("entrega fallida: %+v", resultado)
	}
	if string(cuerpo) != cuerpoEsperado {
		t.Fatal("el cuerpo enviado no fue el cuerpo firmado del contrato")
	}
}

func TestEntregarClasificaRespuestasSinSeguirRedirecciones(t *testing.T) {
	for _, caso := range []struct {
		estado              int
		correcta, reintenta bool
	}{{204, true, false}, {429, false, true}, {503, false, true}, {302, false, false}, {422, false, false}} {
		t.Run(http.StatusText(caso.estado), func(t *testing.T) {
			servidor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(caso.estado) }))
			defer servidor.Close()
			r := NuevoEntregador().Entregar(context.Background(), eventoPrueba(t), Destino{URL: servidor.URL, Secreto: secretoPrueba})
			if r.Correcta != caso.correcta || r.Reintentable != caso.reintenta {
				t.Fatalf("clasificacion incorrecta: %+v", r)
			}
		})
	}
}

func TestEntregarOcultaURLYSecretoEnError(t *testing.T) {
	destino := Destino{URL: "http://127.0.0.1:1/ruta-secreta", Secreto: secretoPrueba}
	ctx, cancelar := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancelar()
	r := NuevoEntregador().Entregar(ctx, eventoPrueba(t), destino)
	if !r.Reintentable || r.Detalle != "no se pudo conectar con el cerebro" {
		t.Fatalf("error inseguro: %+v", r)
	}
}

func TestSeleccionarRafagaRespetaRespuestaHumanaYDebidos(t *testing.T) {
	base := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	filas := []filaMensaje{
		{ID: "m1", Direccion: "in", Tipo: "text", CreadoEn: base},
		{ID: "s1", Direccion: "out", Tipo: "text", CreadoEn: base.Add(time.Second), IAGenerada: false},
		{ID: "m2", Direccion: "in", Tipo: "text", CreadoEn: base.Add(2 * time.Second)},
		{ID: "s2", Direccion: "out", Tipo: "text", CreadoEn: base.Add(3 * time.Second), IAGenerada: true},
		{ID: "m3", Direccion: "in", Tipo: "text", CreadoEn: base.Add(4 * time.Second)},
	}
	r := seleccionarRafaga(filas, []string{"m2"})
	if len(r) != 2 || r[0].ID != "m2" || r[1].ID != "m3" {
		t.Fatalf("rafaga incorrecta: %+v", r)
	}
	if r := seleccionarRafaga(filas, []string{"m1"}); len(r) != 1 || r[0].ID != "m3" {
		t.Fatalf("una respuesta humana no saldo lo anterior: %+v", r)
	}
}

func TestEsperaDeReintento(t *testing.T) {
	if EsperaReintento(1) != time.Second || EsperaReintento(2) != 4*time.Second || EsperaReintento(3) != 4*time.Second {
		t.Fatal("backoff incorrecto")
	}
}

func TestDiferenciaConservaSoloMensajesNoEntregados(t *testing.T) {
	r := diferencia([]string{"m1", "m2", "m2", "m3"}, []string{"m1", "m3"})
	if len(r) != 1 || r[0] != "m2" {
		t.Fatalf("diferencia incorrecta: %v", r)
	}
}
