package despacho

import (
	"encoding/json"
	"testing"
	"time"
)

const cuerpoEsperado = `{"dispatchId":"dsp_prueba0000000000001","organization":{"id":"org_prueba","slug":"mi-negocio"},"conversation":{"id":"cv_prueba","channel":"whatsapp","aiEnabled":true,"windowOpen":true,"windowExpiresAt":"2026-10-09T05:10:11.000Z"},"contact":{"id":"ct_prueba","displayName":"Ana Pérez","identity":"5215512345678"},"messages":[{"id":"msg_1","at":"2026-10-08T05:10:03.000Z","type":"text","text":"hola, ¿tienen el modelo azul?","mediaId":null},{"id":"msg_2","at":"2026-10-08T05:10:11.000Z","type":"image","text":null,"mediaId":"1234567890","mimeType":"image/jpeg","caption":"este"}],"firstMessageAt":"2026-10-08T05:10:03.000Z","lastMessageAt":"2026-10-08T05:10:11.000Z"}`
const firmaEsperada = "sha256=885074513a6fbd7e2d4443e7f45e3837246effed6c9efa596260057f4dd9008d"
const secretoPrueba = "llave-de-prueba-del-contrato-0001"

func puntero[T any](valor T) *T { return &valor }

func eventoPrueba(t *testing.T) Evento {
	t.Helper()
	primero := time.Date(2026, 10, 8, 5, 10, 3, 0, time.UTC)
	ultimo := time.Date(2026, 10, 8, 5, 10, 11, 0, time.UTC)
	vence := time.Date(2026, 10, 9, 5, 10, 11, 0, time.UTC)
	evento, err := ArmarEvento(DatosEvento{
		DespachoID: "dsp_prueba0000000000001", OrganizacionID: "org_prueba", OrganizacionSlug: puntero("mi-negocio"),
		ConversacionID: "cv_prueba", Canal: "whatsapp", IAHabilitada: true, VentanaAbierta: true, VentanaExpiraEn: &vence,
		ContactoID: "ct_prueba", NombreContacto: "Ana Pérez", Identidad: "5215512345678",
		Mensajes: []MensajeEntrada{
			{ID: "msg_1", CreadoEn: primero, Tipo: "text", Texto: puntero("hola, ¿tienen el modelo azul?")},
			{ID: "msg_2", CreadoEn: ultimo, Tipo: "image", Medio: &MedioEntrada{ID: puntero("1234567890"), MIME: puntero("image/jpeg"), Leyenda: puntero("este")}},
		},
		PrimerMensaje: primero, UltimoMensaje: ultimo,
	})
	if err != nil {
		t.Fatalf("armar evento: %v", err)
	}
	return evento
}

func TestContratoSeSerializaYFirmaByteAByte(t *testing.T) {
	cuerpo, firma, err := SerializarYFirmar(eventoPrueba(t), secretoPrueba)
	if err != nil {
		t.Fatal(err)
	}
	if string(cuerpo) != cuerpoEsperado {
		t.Fatalf("cuerpo distinto\nobtenido: %s\nesperado: %s", cuerpo, cuerpoEsperado)
	}
	if firma != firmaEsperada {
		t.Fatalf("firma distinta: %s", firma)
	}
	if !FirmaValida(cuerpo, secretoPrueba, firma) {
		t.Fatal("la firma correcta no fue validada")
	}
	alterado := append([]byte(nil), cuerpo...)
	alterado[len(alterado)-2] ^= 1
	if FirmaValida(alterado, secretoPrueba, firma) {
		t.Fatal("una alteracion del cuerpo fue aceptada")
	}
}

func TestContratoNoExponeCredencialesNiCamposExtra(t *testing.T) {
	cuerpo, err := json.Marshal(eventoPrueba(t))
	if err != nil {
		t.Fatal(err)
	}
	var raiz map[string]any
	if err := json.Unmarshal(cuerpo, &raiz); err != nil {
		t.Fatal(err)
	}
	if len(raiz) != 7 {
		t.Fatalf("se esperaban 7 llaves publicas, llegaron %d", len(raiz))
	}
	for _, prohibida := range []string{"token", "apiKey", "phoneNumberId", "wabaId"} {
		if _, existe := raiz[prohibida]; existe {
			t.Fatalf("el contrato expuso %s", prohibida)
		}
	}
}

func TestContratoRechazaDespachoVacio(t *testing.T) {
	_, err := ArmarEvento(DatosEvento{DespachoID: "dsp_x", OrganizacionID: "org_x", ConversacionID: "cv_x", ContactoID: "ct_x", Identidad: "1"})
	if err == nil {
		t.Fatal("se acepto un despacho sin mensajes")
	}
}

func TestCamposOpcionalesSoloCuandoAplican(t *testing.T) {
	evento := eventoPrueba(t)
	evento.Conversacion.Prueba = true
	evento.Mensajes[0].Ubicacion = &Ubicacion{Latitud: -33.45, Longitud: -70.66, Nombre: "Santiago"}
	evento.Mensajes[0].Contactos = []ContactoCompartido{{Nombre: "Marco"}}
	cuerpo, err := json.Marshal(evento)
	if err != nil {
		t.Fatal(err)
	}
	var raiz map[string]any
	if err := json.Unmarshal(cuerpo, &raiz); err != nil {
		t.Fatal(err)
	}
	conversacion := raiz["conversation"].(map[string]any)
	if conversacion["test"] != true {
		t.Fatal("la conversacion de laboratorio no llevo test=true")
	}
	segundo := raiz["messages"].([]any)[1].(map[string]any)
	if _, existe := segundo["location"]; existe {
		t.Fatal("se agrego ubicacion a un mensaje que no la tenia")
	}
}

func TestClasificacionDeRespuesta(t *testing.T) {
	for _, estado := range []int{200, 201, 202, 204} {
		if r := ClasificarRespuesta(estado); !r.Correcta || r.Reintentable {
			t.Fatalf("%d debio ser correcto: %+v", estado, r)
		}
	}
	for _, estado := range []int{429, 500, 502, 503, 504} {
		if r := ClasificarRespuesta(estado); r.Correcta || !r.Reintentable {
			t.Fatalf("%d debio ser reintentable: %+v", estado, r)
		}
	}
	for _, estado := range []int{301, 302, 400, 401, 404, 409, 422} {
		if r := ClasificarRespuesta(estado); r.Correcta || r.Reintentable {
			t.Fatalf("%d no debio reintentarse: %+v", estado, r)
		}
	}
}
