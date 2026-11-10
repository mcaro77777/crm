package httpapi

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"crm-api/internal/configuracion"
	"crm-api/internal/eventos"
	_ "github.com/go-sql-driver/mysql"
)

func TestIntegracionAislamientoLaboratorioE1(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("TEST_DATABASE_DSN no configurada")
	}
	separador := "?"
	if strings.Contains(dsn, "?") {
		separador = "&"
	}
	db, err := sql.Open("mysql", dsn+separador+"parseTime=true&loc=UTC")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var llamadasMeta atomic.Int32
	metaFalso := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		llamadasMeta.Add(1)
		http.Error(w, "una prueba nunca debe llegar a Meta", http.StatusInternalServerError)
	}))
	defer metaFalso.Close()

	ctx := context.Background()
	servidor := &Servidor{
		cfg: configuracion.Configuracion{MetaGraphBaseURL: metaFalso.URL},
		db:  db, logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		bus: eventos.NuevoBus(),
	}
	if err := servidor.asegurarEsquemaLaboratorio(ctx); err != nil {
		t.Fatal(err)
	}
	const org = "org_test_laboratorio_e1"
	_, _ = db.ExecContext(ctx, "DELETE FROM organization WHERE id=?", org)
	defer db.ExecContext(ctx, "DELETE FROM organization WHERE id=?", org)

	ahora := time.Now().UTC().Truncate(time.Millisecond)
	if _, err := db.ExecContext(ctx, "INSERT INTO organization(id,name,slug,created_at) VALUES (?,'Laboratorio E1','laboratorio-e1',?)", org, ahora); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO agent_test_run(id,organization_id,status,started_at) VALUES ('run_lab_e1',?,'running',?)", org, ahora); err != nil {
		t.Fatal(err)
	}
	escenario := escenarioLaboratorio{ID: "scenario_lab_e1", Nombre: "Consulta de precio", Persona: "Cliente cauteloso", Objetivo: "Responder sin inventar", Turnos: []string{"¿Cuánto cuesta?"}, Criterios: []string{"No inventa precios"}, Activo: true, CreadoEn: ahora, ActualizadoEn: ahora}
	if _, err := db.ExecContext(ctx, `INSERT INTO lab_scenario(id,organization_id,nombre,persona,objetivo,turnos,criterios,activo,created_at,updated_at) VALUES (?,?,?,?,?,CAST(? AS JSON),CAST(? AS JSON),1,?,?)`, escenario.ID, org, escenario.Nombre, escenario.Persona, escenario.Objetivo, marshalJSON(escenario.Turnos), marshalJSON(escenario.Criterios), ahora, ahora); err != nil {
		t.Fatal(err)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	trabajo, err := crearCasoLaboratorioTx(ctx, tx, org, "run_lab_e1", escenario, ahora)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	var prueba bool
	var archivado sql.NullTime
	var waID sql.NullString
	var snapshots int
	err = db.QueryRowContext(ctx, `SELECT cv.is_test,c.archived_at,m.wa_message_id,(SELECT COUNT(*) FROM lab_case_snapshot s WHERE s.organization_id=cv.organization_id AND s.case_id=?) FROM conversation cv JOIN contact c ON c.id=cv.contact_id AND c.organization_id=cv.organization_id JOIN message m ON m.conversation_id=cv.id AND m.organization_id=cv.organization_id WHERE cv.organization_id=? AND cv.id=? AND m.id=?`, trabajo.CasoID, org, trabajo.ConversacionID, trabajo.PrimerMensajeID).Scan(&prueba, &archivado, &waID, &snapshots)
	if err != nil || !prueba || !archivado.Valid || waID.Valid || snapshots != 1 {
		t.Fatalf("sandbox incompleto: prueba=%v archivado=%v wa=%v snapshots=%d err=%v", prueba, archivado.Valid, waID.Valid, snapshots, err)
	}
	if _, err := servidor.enviarTextoConversacion(ctx, org, trabajo.ConversacionID, "Respuesta del cerebro", true); err != nil {
		t.Fatal(err)
	}
	if llamadasMeta.Load() != 0 {
		t.Fatalf("la conversacion de prueba hizo %d llamadas a Meta", llamadasMeta.Load())
	}
	var generada bool
	if err := db.QueryRowContext(ctx, "SELECT ai_generated FROM message WHERE organization_id=? AND conversation_id=? AND direction='out'", org, trabajo.ConversacionID).Scan(&generada); err != nil || !generada {
		t.Fatalf("respuesta del cerebro no persistida como IA: %v", err)
	}
}
