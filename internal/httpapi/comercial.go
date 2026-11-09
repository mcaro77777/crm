package httpapi

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"crm-api/internal/identificadores"
	"crm-api/internal/meta"
	"crm-api/internal/seguridad"
)

type atribucionEntrada struct {
	CTWAClid, SourceID, Headline, Body string
}

// guardarAtribucionTx conserva la primera atribución de la conversación. Debe
// llamarse dentro de la misma transacción que confirma el mensaje entrante.
func guardarAtribucionTx(ctx context.Context, tx *sql.Tx, org, contactoID, conversacionID string, a *atribucionEntrada, ahora time.Time) error {
	if a == nil {
		return nil
	}
	id, _ := identificadores.Nuevo("ad")
	raw, _ := json.Marshal(a)
	_, err := tx.ExecContext(ctx, `INSERT IGNORE INTO ad_attribution(id,organization_id,contact_id,conversation_id,ctwa_clid,source_id,headline,body,raw,created_at) VALUES (?,?,?,?,?,?,?,?,?,?)`, id, org, contactoID, conversacionID, nuloVacio(a.CTWAClid), nuloVacio(a.SourceID), nuloVacio(a.Headline), nuloVacio(a.Body), raw, ahora)
	return err
}

// encolarConversionTx es idempotente por conversación y nombre. Se invoca al
// crear un lead atribuible y al moverlo a la etapa ganada/calificada.
func encolarConversionTx(ctx context.Context, tx *sql.Tx, org, contactoID, nombre string, ahora time.Time) error {
	var conversacionID string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM conversation WHERE organization_id=? AND contact_id=? AND is_test=0 LIMIT 1`, org, contactoID).Scan(&conversacionID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	var atribucion sql.NullString
	_ = tx.QueryRowContext(ctx, `SELECT id FROM ad_attribution WHERE organization_id=? AND conversation_id=?`, org, conversacionID).Scan(&atribucion)
	var capiExiste bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM capi_settings WHERE organization_id=? AND status='connected')`, org).Scan(&capiExiste); err != nil || !capiExiste {
		return err
	}
	id, _ := identificadores.Nuevo("ce")
	_, err := tx.ExecContext(ctx, `INSERT IGNORE INTO conversion_event(id,organization_id,conversation_id,attribution_id,event_name,status,created_at) VALUES (?,?,?,?,?,'pending',?)`, id, org, conversacionID, atribucion, nombre, ahora)
	return err
}

type resumenComercial struct {
	Desde, Hasta                                                 time.Time
	Contactos, Oportunidades, Abiertas, Ganadas, Perdidas        int
	Conversaciones, Entrantes, Salientes, RespuestasIA, Handoffs int
	ValorGanadoCentavos, TiempoPrimeraRespuestaSegundos          int64
	PorEtapa, PorFuente                                          []map[string]any
}

func (s *Servidor) obtenerAnalitica(w http.ResponseWriter, r *http.Request) {
	sesion := sesionDe(r.Context())
	desde, hasta, err := periodoAnalitica(r)
	if err != nil {
		responderError(w, 422, "validation_error", err.Error(), nil)
		return
	}
	x, err := calcularAnalitica(r.Context(), s.db, sesion.OrganizacionID, desde, hasta)
	if err != nil {
		s.errorInterno(w, "calcular analítica", err)
		return
	}
	responderJSON(w, http.StatusOK, x)
}

func calcularAnalitica(ctx context.Context, db *sql.DB, org string, desde, hasta time.Time) (resumenComercial, error) {
	x := resumenComercial{Desde: desde, Hasta: hasta, PorEtapa: []map[string]any{}, PorFuente: []map[string]any{}}
	err := db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT c.id),COUNT(DISTINCT l.id),COUNT(DISTINCT CASE WHEN ps.kind='open' THEN l.id END),COUNT(DISTINCT CASE WHEN ps.kind='won' THEN l.id END),COUNT(DISTINCT CASE WHEN ps.kind='lost' THEN l.id END),COALESCE(SUM(CASE WHEN ps.kind='won' THEN l.amount_cents ELSE 0 END),0) FROM contact c LEFT JOIN lead l ON l.contact_id=c.id AND l.organization_id=c.organization_id LEFT JOIN pipeline_stage ps ON ps.id=l.stage_id AND ps.organization_id=l.organization_id WHERE c.organization_id=? AND c.created_at>=? AND c.created_at<?`, org, desde, hasta).Scan(&x.Contactos, &x.Oportunidades, &x.Abiertas, &x.Ganadas, &x.Perdidas, &x.ValorGanadoCentavos)
	if err != nil {
		return x, err
	}
	err = db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT cv.id),COALESCE(SUM(m.direction='in'),0),COALESCE(SUM(m.direction='out'),0),COALESCE(SUM(m.ai_generated=1),0),COUNT(DISTINCT CASE WHEN cv.handoff_at IS NOT NULL THEN cv.id END) FROM conversation cv LEFT JOIN message m ON m.conversation_id=cv.id AND m.organization_id=cv.organization_id AND m.created_at>=? AND m.created_at<? WHERE cv.organization_id=? AND cv.created_at<?`, desde, hasta, org, hasta).Scan(&x.Conversaciones, &x.Entrantes, &x.Salientes, &x.RespuestasIA, &x.Handoffs)
	if err != nil {
		return x, err
	}
	rows, err := db.QueryContext(ctx, `SELECT ps.id,ps.name,ps.kind,COUNT(l.id),COALESCE(SUM(l.amount_cents),0) FROM pipeline_stage ps LEFT JOIN lead l ON l.stage_id=ps.id AND l.organization_id=ps.organization_id AND l.created_at>=? AND l.created_at<? WHERE ps.organization_id=? GROUP BY ps.id,ps.name,ps.kind,ps.position ORDER BY ps.position`, desde, hasta, org)
	if err != nil {
		return x, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, nombre, tipo string
		var cantidad int
		var monto int64
		if err := rows.Scan(&id, &nombre, &tipo, &cantidad, &monto); err != nil {
			return x, err
		}
		x.PorEtapa = append(x.PorEtapa, map[string]any{"id": id, "nombre": nombre, "tipo": tipo, "cantidad": cantidad, "montoCentavos": monto})
	}
	rows2, err := db.QueryContext(ctx, `SELECT COALESCE(NULLIF(c.source,''),'sin_fuente'),COUNT(*),COALESCE(SUM(l.amount_cents),0) FROM contact c LEFT JOIN lead l ON l.contact_id=c.id AND l.organization_id=c.organization_id WHERE c.organization_id=? AND c.created_at>=? AND c.created_at<? GROUP BY COALESCE(NULLIF(c.source,''),'sin_fuente') ORDER BY COUNT(*) DESC`, org, desde, hasta)
	if err != nil {
		return x, err
	}
	defer rows2.Close()
	for rows2.Next() {
		var fuente string
		var cantidad int
		var monto int64
		if err := rows2.Scan(&fuente, &cantidad, &monto); err != nil {
			return x, err
		}
		x.PorFuente = append(x.PorFuente, map[string]any{"fuente": fuente, "cantidad": cantidad, "montoCentavos": monto})
	}
	return x, rows2.Err()
}

func periodoAnalitica(r *http.Request) (time.Time, time.Time, error) {
	hasta := time.Now().UTC()
	desde := hasta.AddDate(0, 0, -30)
	var err error
	if v := strings.TrimSpace(r.URL.Query().Get("desde")); v != "" {
		desde, err = time.Parse("2006-01-02", v)
		if err != nil {
			return desde, hasta, errors.New("desde debe usar AAAA-MM-DD")
		}
	}
	if v := strings.TrimSpace(r.URL.Query().Get("hasta")); v != "" {
		hasta, err = time.Parse("2006-01-02", v)
		if err != nil {
			return desde, hasta, errors.New("hasta debe usar AAAA-MM-DD")
		}
		hasta = hasta.Add(24 * time.Hour)
	}
	if !desde.Before(hasta) || hasta.Sub(desde) > 366*24*time.Hour {
		return desde, hasta, errors.New("el período debe ser válido y no superar 366 días")
	}
	return desde.UTC(), hasta.UTC(), nil
}

func (s *Servidor) exportarAnaliticaCSV(w http.ResponseWriter, r *http.Request) {
	sesion := sesionDe(r.Context())
	desde, hasta, err := periodoAnalitica(r)
	if err != nil {
		responderError(w, 422, "validation_error", err.Error(), nil)
		return
	}
	x, err := calcularAnalitica(r.Context(), s.db, sesion.OrganizacionID, desde, hasta)
	if err != nil {
		s.errorInterno(w, "exportar analítica", err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="resultados.csv"`)
	_, _ = w.Write([]byte{0xEF, 0xBB, 0xBF})
	c := csv.NewWriter(w)
	_ = c.Write([]string{"indicador", "valor"})
	filas := [][]string{{"contactos", strconv.Itoa(x.Contactos)}, {"oportunidades", strconv.Itoa(x.Oportunidades)}, {"ganadas", strconv.Itoa(x.Ganadas)}, {"perdidas", strconv.Itoa(x.Perdidas)}, {"valor_ganado_centavos", strconv.FormatInt(x.ValorGanadoCentavos, 10)}, {"respuestas_ia", strconv.Itoa(x.RespuestasIA)}, {"handoffs", strconv.Itoa(x.Handoffs)}}
	_ = c.WriteAll(filas)
	c.Flush()
}

func (s *Servidor) obtenerCAPI(w http.ResponseWriter, r *http.Request) {
	sesion := sesionDe(r.Context())
	var dataset, stage, estado string
	var cifrado sql.NullString
	err := s.db.QueryRowContext(r.Context(), `SELECT dataset_id,COALESCE(qualified_stage_id,''),status,token_cipher FROM capi_settings WHERE organization_id=?`, sesion.OrganizacionID).Scan(&dataset, &stage, &estado, &cifrado)
	if errors.Is(err, sql.ErrNoRows) {
		responderJSON(w, 200, map[string]any{"configurada": false})
		return
	}
	if err != nil {
		s.errorInterno(w, "leer CAPI", err)
		return
	}
	responderJSON(w, 200, map[string]any{"configurada": true, "datasetId": dataset, "qualifiedStageId": stage, "estado": estado, "tokenGuardado": cifrado.Valid})
}

func (s *Servidor) guardarCAPI(w http.ResponseWriter, r *http.Request) {
	sesion := sesionDe(r.Context())
	var e struct {
		DatasetID string `json:"datasetId"`
		Token     string `json:"token"`
		EtapaID   string `json:"qualifiedStageId"`
	}
	if !decodificarJSON(w, r, &e) {
		return
	}
	e.DatasetID = strings.TrimSpace(e.DatasetID)
	e.Token = strings.TrimSpace(e.Token)
	if e.DatasetID == "" || len(e.Token) < 16 {
		responderError(w, 422, "validation_error", "Dataset ID y token son obligatorios", nil)
		return
	}
	sec, err := seguridad.CifrarSecreto(s.cfg.LlaveCifrado, e.Token)
	if err != nil {
		s.errorInterno(w, "cifrar token CAPI", err)
		return
	}
	id, _ := identificadores.Nuevo("capi")
	ahora := time.Now().UTC()
	_, err = s.db.ExecContext(r.Context(), `INSERT INTO capi_settings(id,organization_id,dataset_id,token_cipher,token_iv,token_tag,qualified_stage_id,status,created_at,updated_at) VALUES (?,?,?,?,?,?,NULLIF(?,''),'connected',?,?) ON DUPLICATE KEY UPDATE dataset_id=VALUES(dataset_id),token_cipher=VALUES(token_cipher),token_iv=VALUES(token_iv),token_tag=VALUES(token_tag),qualified_stage_id=VALUES(qualified_stage_id),status='connected',updated_at=VALUES(updated_at)`, id, sesion.OrganizacionID, e.DatasetID, sec.Cifrado, sec.IV, sec.Tag, e.EtapaID, ahora, ahora)
	if err != nil {
		s.errorInterno(w, "guardar CAPI", err)
		return
	}
	responderJSON(w, 200, map[string]any{"configurada": true, "datasetId": e.DatasetID, "qualifiedStageId": e.EtapaID, "estado": "connected"})
}

func construirEventoCAPI(eventID, nombre string, marca time.Time, ctwa string, valor int64, moneda string) map[string]any {
	datos := map[string]any{"event_name": nombre, "event_time": marca.Unix(), "action_source": "business_messaging", "event_id": eventID, "user_data": map[string]any{"ctwa_clid": ctwa}}
	if valor > 0 {
		datos["custom_data"] = map[string]any{"value": float64(valor) / 100, "currency": moneda}
	}
	return datos
}

// enviarConversionCAPI permite al futuro worker procesar conversion_event sin
// duplicar contrato ni exponer el token.
func (s *Servidor) enviarConversionCAPI(ctx context.Context, org, eventID string) error {
	var dataset, cifrado, iv, tag, nombre, ctwa, moneda string
	var creado time.Time
	var valor sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT cs.dataset_id,cs.token_cipher,cs.token_iv,cs.token_tag,ce.event_name,ce.created_at,COALESCE(aa.ctwa_clid,''),l.amount_cents,COALESCE(l.currency,'USD') FROM conversion_event ce JOIN capi_settings cs ON cs.organization_id=ce.organization_id LEFT JOIN ad_attribution aa ON aa.id=ce.attribution_id AND aa.organization_id=ce.organization_id JOIN conversation cv ON cv.id=ce.conversation_id AND cv.organization_id=ce.organization_id LEFT JOIN lead l ON l.contact_id=cv.contact_id AND l.organization_id=cv.organization_id WHERE ce.organization_id=? AND ce.id=?`, org, eventID).Scan(&dataset, &cifrado, &iv, &tag, &nombre, &creado, &ctwa, &valor, &moneda)
	if err != nil {
		return err
	}
	token, err := seguridad.DescifrarSecreto(s.cfg.LlaveCifrado, seguridad.SecretoCifrado{Cifrado: cifrado, IV: iv, Tag: tag})
	if err != nil {
		return err
	}
	trace, err := meta.NuevoConBaseURL(token, "v25.0", s.cfg.MetaGraphBaseURL).EnviarEventosCAPI(ctx, dataset, []any{construirEventoCAPI(eventID, nombre, creado, ctwa, valor.Int64, moneda)})
	estado := "sent"
	var detalle any = nil
	if err != nil {
		estado = "failed"
		detalle = fmt.Sprint(err)
	}
	_, updateErr := s.db.ExecContext(ctx, "UPDATE conversion_event SET status=?,error=?,fb_trace_id=?,sent_at=?,created_at=created_at WHERE organization_id=? AND id=?", estado, detalle, nuloVacio(trace), time.Now().UTC(), org, eventID)
	if err != nil {
		return err
	}
	return updateErr
}
