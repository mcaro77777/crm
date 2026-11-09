package despacho

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

// Se ejecuta solo cuando se entrega TEST_DATABASE_DSN. Permite validar las
// consultas especificas de MySQL sin volver obligatoria una base para go test.
func TestIntegracionOutboxMySQL(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("TEST_DATABASE_DSN no configurada")
	}
	db, err := sql.Open("mysql", dsn+"?parseTime=true&loc=UTC")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	ahora := time.Now().UTC().Truncate(time.Millisecond)
	org, contacto, conversacion := "org_test_dispatch", "ct_test_dispatch", "cv_test_dispatch"
	_, _ = db.ExecContext(ctx, "DELETE FROM organization WHERE id=?", org)
	if _, err = db.ExecContext(ctx, "INSERT INTO organization(id,name,created_at) VALUES (?, 'Prueba dispatch', ?)", org, ahora); err != nil {
		t.Fatal(err)
	}
	defer db.ExecContext(ctx, "DELETE FROM organization WHERE id=?", org)
	if _, err = db.ExecContext(ctx, "INSERT INTO contact(id,organization_id,channel,wa_identity,name,created_at,updated_at) VALUES (?,?,'whatsapp','56900000000','Prueba',?,?)", contacto, org, ahora, ahora); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, "INSERT INTO conversation(id,organization_id,contact_id,channel,ai_enabled,created_at,updated_at) VALUES (?,?,?,'whatsapp',1,?,?)", conversacion, org, contacto, ahora, ahora); err != nil {
		t.Fatal(err)
	}

	repo := NuevoRepositorio(db)
	primero, err := repo.Encolar(ctx, org, conversacion, []string{"msg_1", "msg_1"}, ahora, false)
	if err != nil {
		t.Fatal(err)
	}
	segundo, err := repo.Encolar(ctx, org, conversacion, []string{"msg_2"}, ahora.Add(time.Second), false)
	if err != nil {
		t.Fatal(err)
	}
	if primero.ID != segundo.ID {
		t.Fatal("se crearon dos despachos vivos para una conversacion")
	}
	estado, err := repo.Estado(ctx, org, conversacion)
	if err != nil || estado == nil || estado.Estado != "esperando" {
		t.Fatalf("estado inicial incorrecto: estado=%+v error=%v", estado, err)
	}

	tomado, err := repo.TomarUno(ctx, ahora.Add(20*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if tomado == nil || tomado.ID != primero.ID || tomado.Intentos != 1 {
		t.Fatalf("reclamo incorrecto: %+v", tomado)
	}
	estado, _ = repo.Estado(ctx, org, conversacion)
	if estado == nil || estado.Estado != "procesando" {
		t.Fatalf("estado en vuelo incorrecto: %+v", estado)
	}
	if _, err := repo.RenovarLease(ctx, *tomado, ahora.Add(21*time.Second)); err != nil {
		t.Fatal(err)
	}
	pendientes, propio, err := repo.FinalizarEntregado(ctx, *tomado, []string{"msg_1"}, ahora.Add(22*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if !propio || len(pendientes) != 1 || pendientes[0] != "msg_2" {
		t.Fatalf("mensajes llegados durante el vuelo incorrectos: %v", pendientes)
	}

	if _, err := repo.Encolar(ctx, org, conversacion, pendientes, ahora.Add(23*time.Second), true); err != nil {
		t.Fatal(err)
	}
	segundoTurno, err := repo.TomarUno(ctx, ahora.Add(24*time.Second))
	if err != nil || segundoTurno == nil {
		t.Fatalf("no se recupero el nuevo despacho: %v", err)
	}
	if _, err := repo.ReintentarOCaducar(ctx, *segundoTurno, "detalle interno que no debe salir", ahora.Add(25*time.Second)); err != nil {
		t.Fatal(err)
	}
	estado, _ = repo.Estado(ctx, org, conversacion)
	if estado == nil || estado.Estado != "reintentando" || estado.ProximoIntento == nil {
		t.Fatalf("estado de reintento incorrecto: %+v", estado)
	}
	ultimoTurno, err := repo.TomarUno(ctx, ahora.Add(30*time.Second))
	if err != nil || ultimoTurno == nil {
		t.Fatalf("no se reclamo el reintento: %v", err)
	}
	caduco, err := repo.Caducar(ctx, *ultimoTurno, "respuesta definitiva", ahora.Add(31*time.Second))
	if err != nil || !caduco {
		t.Fatalf("no se pudo caducar: %v", err)
	}
	estado, _ = repo.Estado(ctx, org, conversacion)
	if estado == nil || estado.Estado != "atencion_humana" {
		t.Fatalf("estado final incorrecto: %+v", estado)
	}
}
