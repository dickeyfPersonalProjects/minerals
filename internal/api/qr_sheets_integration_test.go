//go:build integration

package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dickeyfPersonalProjects/minerals/internal/api"
	"github.com/dickeyfPersonalProjects/minerals/internal/db"
)

// qrSheetTestSrv stands up an httptest.NewServer around api.New()
// wired with the QR sheet repo and the dependencies the sheet
// endpoints transitively need (specimens for FK targets). Specimens
// aren't exercised via the HTTP API in these tests — the QR sheet
// flow uses raw INSERTs against the pool to seed specimen rows.
func qrSheetTestSrv(t *testing.T) (*httptest.Server, *pgxpool.Pool) {
	t.Helper()
	pool := scopedDB(t)
	h := api.New(api.Deps{
		Specimens: db.NewSpecimenPostgres(pool),
		QRSheets:  db.NewQRSheetPostgres(pool),
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, pool
}

// seedAPISpecimen inserts a bare specimen row directly and returns its
// id. Mirrors the helper in the db integration tests; the QR sheet
// flow doesn't go through the specimen API in v1.
func seedAPISpecimen(t *testing.T, pool *pgxpool.Pool, name string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	now := time.Now().UTC()
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO specimens (id, type, name, author_id, created_at, updated_at)
		VALUES ($1, 'mineral', $2, $3, $4, $4)`,
		id, name, uuid.MustParse("00000000-0000-0000-0000-000000000001"), now,
	); err != nil {
		t.Fatalf("seed specimen %q: %v", name, err)
	}
	return id
}

type qrSheetSpecimenJSON struct {
	SpecimenID   string  `json:"specimen_id"`
	Name         string  `json:"name"`
	Position     int     `json:"position"`
	ThumbnailURL *string `json:"thumbnail_url"`
}

type qrSheetJSON struct {
	ID        string                `json:"id"`
	Template  string                `json:"template"`
	PageCount int                   `json:"page_count"`
	Specimens []qrSheetSpecimenJSON `json:"specimens"`
}

func decodeQRSheet(t *testing.T, body []byte) qrSheetJSON {
	t.Helper()
	var v qrSheetJSON
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("unmarshal: %v\nbody=%s", err, string(body))
	}
	return v
}

func TestIntegration_QRSheetAPI_GetMissing_Returns404(t *testing.T) {
	srv, _ := qrSheetTestSrv(t)

	status, body := doJSON(t, srv.Client(), http.MethodGet, srv.URL+"/api/v1/qr-sheet", nil)
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404\nbody=%s", status, string(body))
	}
	var env envelopeBody
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if env.Error.Code != "qr_sheet_not_found" {
		t.Errorf("code = %q want qr_sheet_not_found", env.Error.Code)
	}
}

func TestIntegration_QRSheetAPI_CreateThenGet(t *testing.T) {
	srv, _ := qrSheetTestSrv(t)

	status, body := doJSON(t, srv.Client(), http.MethodPost, srv.URL+"/api/v1/qr-sheet",
		map[string]any{"template": "avery-5160"})
	if status != http.StatusCreated {
		t.Fatalf("status = %d want 201\nbody=%s", status, string(body))
	}
	created := decodeQRSheet(t, body)
	if created.Template != "avery-5160" {
		t.Errorf("template = %q", created.Template)
	}
	if created.PageCount != 0 || len(created.Specimens) != 0 {
		t.Errorf("expected empty sheet, got pages=%d specimens=%d", created.PageCount, len(created.Specimens))
	}

	status, body = doJSON(t, srv.Client(), http.MethodGet, srv.URL+"/api/v1/qr-sheet", nil)
	if status != http.StatusOK {
		t.Fatalf("get status = %d\nbody=%s", status, string(body))
	}
	got := decodeQRSheet(t, body)
	if got.ID != created.ID {
		t.Errorf("id roundtrip: got %q want %q", got.ID, created.ID)
	}
}

func TestIntegration_QRSheetAPI_CreateConflictWhenAlreadyExists(t *testing.T) {
	srv, _ := qrSheetTestSrv(t)

	status, _ := doJSON(t, srv.Client(), http.MethodPost, srv.URL+"/api/v1/qr-sheet",
		map[string]any{"template": "avery-5160"})
	if status != http.StatusCreated {
		t.Fatalf("first create: %d", status)
	}
	status, body := doJSON(t, srv.Client(), http.MethodPost, srv.URL+"/api/v1/qr-sheet",
		map[string]any{"template": "avery-5163"})
	if status != http.StatusConflict {
		t.Fatalf("second create status = %d want 409\nbody=%s", status, string(body))
	}
	var env envelopeBody
	_ = json.Unmarshal(body, &env)
	if env.Error.Code != "qr_sheet_conflict" {
		t.Errorf("code = %q want qr_sheet_conflict", env.Error.Code)
	}
}

func TestIntegration_QRSheetAPI_CreateRejectsUnknownTemplate(t *testing.T) {
	srv, _ := qrSheetTestSrv(t)

	status, body := doJSON(t, srv.Client(), http.MethodPost, srv.URL+"/api/v1/qr-sheet",
		map[string]any{"template": "totally-bogus"})
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d want 400\nbody=%s", status, string(body))
	}
	var env envelopeBody
	_ = json.Unmarshal(body, &env)
	if env.Error.Code != "invalid_template" {
		t.Errorf("code = %q want invalid_template", env.Error.Code)
	}
}

func TestIntegration_QRSheetAPI_PatchSwitchesTemplate(t *testing.T) {
	srv, _ := qrSheetTestSrv(t)

	doJSON(t, srv.Client(), http.MethodPost, srv.URL+"/api/v1/qr-sheet",
		map[string]any{"template": "avery-5160"})

	status, body := doJSON(t, srv.Client(), http.MethodPatch, srv.URL+"/api/v1/qr-sheet",
		map[string]any{"template": "avery-l7160"})
	if status != http.StatusOK {
		t.Fatalf("patch status = %d\nbody=%s", status, string(body))
	}
	got := decodeQRSheet(t, body)
	if got.Template != "avery-l7160" {
		t.Errorf("template = %q want avery-l7160", got.Template)
	}
}

func TestIntegration_QRSheetAPI_PatchMissingReturns404(t *testing.T) {
	srv, _ := qrSheetTestSrv(t)

	status, body := doJSON(t, srv.Client(), http.MethodPatch, srv.URL+"/api/v1/qr-sheet",
		map[string]any{"template": "avery-5160"})
	if status != http.StatusNotFound {
		t.Fatalf("status = %d want 404\nbody=%s", status, string(body))
	}
}

func TestIntegration_QRSheetAPI_DeleteWipesSheet(t *testing.T) {
	srv, _ := qrSheetTestSrv(t)

	doJSON(t, srv.Client(), http.MethodPost, srv.URL+"/api/v1/qr-sheet",
		map[string]any{"template": "avery-5160"})

	status, _ := doJSON(t, srv.Client(), http.MethodDelete, srv.URL+"/api/v1/qr-sheet", nil)
	if status != http.StatusNoContent {
		t.Fatalf("delete status = %d want 204", status)
	}

	// Re-create succeeds — proves the first sheet is gone.
	status, _ = doJSON(t, srv.Client(), http.MethodPost, srv.URL+"/api/v1/qr-sheet",
		map[string]any{"template": "avery-5163"})
	if status != http.StatusCreated {
		t.Fatalf("re-create after delete: %d", status)
	}
}

func TestIntegration_QRSheetAPI_AddSpecimens_AppendsAndComputesPageCount(t *testing.T) {
	srv, pool := qrSheetTestSrv(t)

	doJSON(t, srv.Client(), http.MethodPost, srv.URL+"/api/v1/qr-sheet",
		map[string]any{"template": "avery-5164"}) // capacity 6/sheet

	// Add 7 specimens → page_count should be 2.
	var ids []uuid.UUID
	for i := 0; i < 7; i++ {
		id := seedAPISpecimen(t, pool, "addable")
		ids = append(ids, id)
		status, body := doJSON(t, srv.Client(), http.MethodPost,
			srv.URL+"/api/v1/qr-sheet/specimens",
			map[string]any{"specimen_id": id.String()})
		if status != http.StatusOK {
			t.Fatalf("add %d: status=%d body=%s", i, status, string(body))
		}
	}

	status, body := doJSON(t, srv.Client(), http.MethodGet, srv.URL+"/api/v1/qr-sheet", nil)
	if status != http.StatusOK {
		t.Fatalf("get status = %d", status)
	}
	got := decodeQRSheet(t, body)
	if len(got.Specimens) != 7 {
		t.Errorf("specimens = %d want 7", len(got.Specimens))
	}
	if got.PageCount != 2 {
		t.Errorf("page_count = %d, want 2 (7 specimens / 6 per sheet)", got.PageCount)
	}
	// Position order matches insertion order.
	for i, sp := range got.Specimens {
		if sp.Position != i+1 {
			t.Errorf("specimen[%d].position = %d want %d", i, sp.Position, i+1)
		}
		if sp.SpecimenID != ids[i].String() {
			t.Errorf("specimen[%d].id = %s want %s", i, sp.SpecimenID, ids[i])
		}
	}
}

func TestIntegration_QRSheetAPI_AddSpecimenIsIdempotent(t *testing.T) {
	srv, pool := qrSheetTestSrv(t)

	doJSON(t, srv.Client(), http.MethodPost, srv.URL+"/api/v1/qr-sheet",
		map[string]any{"template": "avery-5160"})
	id := seedAPISpecimen(t, pool, "dup")

	doJSON(t, srv.Client(), http.MethodPost, srv.URL+"/api/v1/qr-sheet/specimens",
		map[string]any{"specimen_id": id.String()})
	status, _ := doJSON(t, srv.Client(), http.MethodPost, srv.URL+"/api/v1/qr-sheet/specimens",
		map[string]any{"specimen_id": id.String()})
	if status != http.StatusOK {
		t.Fatalf("re-add status = %d want 200", status)
	}

	_, body := doJSON(t, srv.Client(), http.MethodGet, srv.URL+"/api/v1/qr-sheet", nil)
	got := decodeQRSheet(t, body)
	if len(got.Specimens) != 1 || got.Specimens[0].Position != 1 {
		t.Errorf("idempotency: got %+v", got.Specimens)
	}
}

func TestIntegration_QRSheetAPI_AddSpecimen_404_WhenNoSheet(t *testing.T) {
	srv, pool := qrSheetTestSrv(t)
	id := seedAPISpecimen(t, pool, "orphan")

	status, body := doJSON(t, srv.Client(), http.MethodPost,
		srv.URL+"/api/v1/qr-sheet/specimens",
		map[string]any{"specimen_id": id.String()})
	if status != http.StatusNotFound {
		t.Fatalf("status = %d want 404\nbody=%s", status, string(body))
	}
}

func TestIntegration_QRSheetAPI_AddSpecimen_404_WhenSpecimenMissing(t *testing.T) {
	srv, _ := qrSheetTestSrv(t)

	doJSON(t, srv.Client(), http.MethodPost, srv.URL+"/api/v1/qr-sheet",
		map[string]any{"template": "avery-5160"})

	status, body := doJSON(t, srv.Client(), http.MethodPost,
		srv.URL+"/api/v1/qr-sheet/specimens",
		map[string]any{"specimen_id": uuid.New().String()})
	if status != http.StatusNotFound {
		t.Fatalf("status = %d want 404\nbody=%s", status, string(body))
	}
	var env envelopeBody
	_ = json.Unmarshal(body, &env)
	if env.Error.Code != "specimen_not_found" {
		t.Errorf("code = %q", env.Error.Code)
	}
}

func TestIntegration_QRSheetAPI_RemoveSpecimen_RepacksPositions(t *testing.T) {
	srv, pool := qrSheetTestSrv(t)

	doJSON(t, srv.Client(), http.MethodPost, srv.URL+"/api/v1/qr-sheet",
		map[string]any{"template": "avery-5160"})

	var ids []uuid.UUID
	for i := 0; i < 4; i++ {
		id := seedAPISpecimen(t, pool, "spec")
		ids = append(ids, id)
		doJSON(t, srv.Client(), http.MethodPost,
			srv.URL+"/api/v1/qr-sheet/specimens",
			map[string]any{"specimen_id": id.String()})
	}
	// Delete the second specimen (position 2).
	status, body := doJSON(t, srv.Client(), http.MethodDelete,
		srv.URL+"/api/v1/qr-sheet/specimens/"+ids[1].String(), nil)
	if status != http.StatusNoContent {
		t.Fatalf("remove status = %d body=%s", status, string(body))
	}

	_, body = doJSON(t, srv.Client(), http.MethodGet, srv.URL+"/api/v1/qr-sheet", nil)
	got := decodeQRSheet(t, body)
	wantOrder := []uuid.UUID{ids[0], ids[2], ids[3]}
	if len(got.Specimens) != 3 {
		t.Fatalf("len = %d", len(got.Specimens))
	}
	for i, sp := range got.Specimens {
		if sp.SpecimenID != wantOrder[i].String() {
			t.Errorf("specimen[%d].id = %s want %s", i, sp.SpecimenID, wantOrder[i])
		}
		if sp.Position != i+1 {
			t.Errorf("specimen[%d].position = %d want %d (no gaps)", i, sp.Position, i+1)
		}
	}
}

func TestIntegration_QRSheetAPI_RemoveSpecimen_404_NotOnSheet(t *testing.T) {
	srv, _ := qrSheetTestSrv(t)

	doJSON(t, srv.Client(), http.MethodPost, srv.URL+"/api/v1/qr-sheet",
		map[string]any{"template": "avery-5160"})

	status, body := doJSON(t, srv.Client(), http.MethodDelete,
		srv.URL+"/api/v1/qr-sheet/specimens/"+uuid.New().String(), nil)
	if status != http.StatusNotFound {
		t.Fatalf("status = %d want 404\nbody=%s", status, string(body))
	}
	var env envelopeBody
	_ = json.Unmarshal(body, &env)
	if env.Error.Code != "qr_sheet_specimen_not_found" {
		t.Errorf("code = %q", env.Error.Code)
	}
}

func TestIntegration_QRSheetAPI_GetIncludesThumbnailURL(t *testing.T) {
	srv, pool := qrSheetTestSrv(t)

	// Seed a specimen and a photo for it via direct SQL — the QR
	// flow doesn't depend on the photo API in v1.
	now := time.Now().UTC()
	spec := seedAPISpecimen(t, pool, "withphoto")
	fileID := uuid.New()
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO files (id, s3_key, content_type, byte_size, sha256, uploaded_by, uploaded_at)
		VALUES ($1, $2, 'image/jpeg', 1, 'x', $3, $4)`,
		fileID, "key-"+fileID.String(),
		uuid.MustParse("00000000-0000-0000-0000-000000000001"), now,
	); err != nil {
		t.Fatalf("file insert: %v", err)
	}
	photoID := uuid.New()
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO photos (id, specimen_id, file_id, position, created_at)
		VALUES ($1, $2, $3, 1, $4)`,
		photoID, spec, fileID, now,
	); err != nil {
		t.Fatalf("photo insert: %v", err)
	}

	doJSON(t, srv.Client(), http.MethodPost, srv.URL+"/api/v1/qr-sheet",
		map[string]any{"template": "avery-5160"})
	doJSON(t, srv.Client(), http.MethodPost, srv.URL+"/api/v1/qr-sheet/specimens",
		map[string]any{"specimen_id": spec.String()})

	_, body := doJSON(t, srv.Client(), http.MethodGet, srv.URL+"/api/v1/qr-sheet", nil)
	got := decodeQRSheet(t, body)
	if len(got.Specimens) != 1 {
		t.Fatalf("got %d specimens", len(got.Specimens))
	}
	want := "/api/v1/photos/" + photoID.String() + "/thumb"
	if got.Specimens[0].ThumbnailURL == nil || *got.Specimens[0].ThumbnailURL != want {
		t.Errorf("thumbnail_url = %v, want %q", got.Specimens[0].ThumbnailURL, want)
	}
	if got.Specimens[0].Name != "withphoto" {
		t.Errorf("name = %q", got.Specimens[0].Name)
	}
}
