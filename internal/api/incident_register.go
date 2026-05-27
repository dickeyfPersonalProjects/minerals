// Confidentiality-incident register surface (mi-2p6i). This is the
// admin-console wiring over internal/incidentregister — the Law 25
// register that lives in its OWN database (INCIDENT_REGISTER_DATABASE_URL)
// and is append-only / tamper-evident (no delete/update path anywhere,
// including the GDPR erasure flow mi-nwg5).
//
// Four endpoints under /api/v1/admin/incident-register, all gated on the
// CONTRACT §13 v2 `devops` Casbin resource:
//
//	POST  .../incidents        file an entry      (devops:edit)
//	GET   .../incidents        list the register  (devops:view)
//	GET   .../incidents/{id}   one entry          (devops:view)
//	GET   .../export           full dump + integrity proof (devops:view)
//
// devops-admin (view+edit) and admin (superset) can file; devops-viewer
// (view only) can read but not file. The whole surface is registered
// ONLY when a register store is wired — when INCIDENT_REGISTER_DATABASE_URL
// is unset the store is nil, these routes are absent, and the admin
// overview reports the section as "planned" rather than "available".
package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/dickeyfPersonalProjects/minerals/internal/auth"
	"github.com/dickeyfPersonalProjects/minerals/internal/authz"
	"github.com/dickeyfPersonalProjects/minerals/internal/incidentregister"
)

// IncidentRegister is the api-side view of the Law 25 register store.
// *incidentregister.Store satisfies it; unit tests substitute a fake.
// Note the absence of any Delete/Update method — the interface itself
// carries the append-only guarantee into the handler layer.
type IncidentRegister interface {
	Create(ctx context.Context, in incidentregister.NewIncident) (incidentregister.Incident, error)
	GetByID(ctx context.Context, id uuid.UUID) (incidentregister.Incident, error)
	List(ctx context.Context) ([]incidentregister.Incident, error)
	Export(ctx context.Context) (incidentregister.Export, error)
}

// incidentRegisterService hosts the four register endpoints. It carries
// the store and the authz guard so every endpoint enforces the same §13
// v2 devops gate the rest of the console uses.
type incidentRegisterService struct {
	reg   IncidentRegister
	guard authzGuard
}

// registerIncidentRegisterOperations wires the register endpoints. It
// no-ops when reg is nil (INCIDENT_REGISTER_DATABASE_URL unset, or the
// unit-test path that doesn't exercise the register) — the routes simply
// aren't registered and fall through to the catch-all 404.
func registerIncidentRegisterOperations(api huma.API, mws authMiddlewares, guard authzGuard, reg IncidentRegister) {
	if reg == nil {
		return
	}
	s := &incidentRegisterService{reg: reg, guard: guard}

	huma.Register(api, huma.Operation{
		OperationID: "incident-register-file",
		Method:      http.MethodPost,
		Path:        "/api/v1/admin/incident-register/incidents",
		Summary:     "File a confidentiality-incident register entry",
		Description: "Appends an entry to the Law 25 confidentiality-incident register, which " +
			"lives in a SEPARATE database and is append-only / tamper-evident (sha256 hash " +
			"chain). Gated on `devops:edit` — devops-admin and admin can file; a view-only " +
			"devops-viewer receives 403. The entry's seq, hashes, recorded_at and retain_until " +
			"(became_aware_date + 5 years) are server-derived and not caller-settable.",
		Tags:          []string{"incident-register"},
		DefaultStatus: http.StatusCreated,
		Errors:        []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden},
		Middlewares:   mws.Protected(),
	}, s.file)

	huma.Register(api, huma.Operation{
		OperationID: "incident-register-list",
		Method:      http.MethodGet,
		Path:        "/api/v1/admin/incident-register/incidents",
		Summary:     "List confidentiality-incident register entries",
		Description: "Returns every register entry in chain order. Gated on `devops:view` " +
			"(devops-viewer, devops-admin, admin).",
		Tags:        []string{"incident-register"},
		Errors:      []int{http.StatusUnauthorized, http.StatusForbidden},
		Middlewares: mws.Protected(),
	}, s.list)

	huma.Register(api, huma.Operation{
		OperationID: "incident-register-get",
		Method:      http.MethodGet,
		Path:        "/api/v1/admin/incident-register/incidents/{id}",
		Summary:     "Get one confidentiality-incident register entry",
		Description: "Returns a single register entry by id. Gated on `devops:view`.",
		Tags:        []string{"incident-register"},
		Errors:      []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound},
		Middlewares: mws.Protected(),
	}, s.get)

	huma.Register(api, huma.Operation{
		OperationID: "incident-register-export",
		Method:      http.MethodGet,
		Path:        "/api/v1/admin/incident-register/export",
		Summary:     "Export the confidentiality-incident register",
		Description: "Returns the full register plus an integrity proof (hash-chain " +
			"verification) — the CAI-requestable copy. Gated on `devops:view`.",
		Tags:        []string{"incident-register"},
		Errors:      []int{http.StatusUnauthorized, http.StatusForbidden},
		Middlewares: mws.Protected(),
	}, s.export)
}

// devopsResource is the §13 v2 resource the register endpoints gate on.
// Same resource the admin overview uses (adminConsoleResource); declared
// here as a value builder for clarity at the call sites.
func devopsResource() *authz.Resource { return &authz.Resource{Type: adminConsoleResource} }

type fileIncidentInput struct {
	Body fileIncidentBody
}

// fileIncidentBody is the operator-supplied content: the 8 Law 25 fields.
// recorded_by is taken from the authenticated caller, not the body — it
// must reflect who actually filed the entry. All text fields are bounded
// so a single entry can't be used to bloat the register.
type fileIncidentBody struct {
	PersonalInfoInvolved      string `json:"personal_info_involved" minLength:"1" maxLength:"10000" doc:"Field 1 — what personal information was involved (or why it can't be described)."`
	Circumstances             string `json:"circumstances" minLength:"1" maxLength:"10000" doc:"Field 2 — what happened and how."`
	IncidentOccurredAt        string `json:"incident_occurred_at" minLength:"1" maxLength:"1000" doc:"Field 3 — date/period the incident occurred (free text; approximate if unknown)."`
	BecameAwareDate           Date   `json:"became_aware_date" doc:"Field 4 — date the operator became aware (YYYY-MM-DD). Drives the 5-year retention deadline."`
	PeopleAffected            string `json:"people_affected" minLength:"1" maxLength:"1000" doc:"Field 5 — number of people affected (or best estimate; free text)."`
	RiskAssessment            string `json:"risk_assessment" minLength:"1" maxLength:"10000" doc:"Field 6 — risk of serious injury (Yes/No) and the factors considered."`
	CAINotified               bool   `json:"cai_notified" doc:"Field 7a — whether the Commission d'accès à l'information was notified."`
	CAINotifiedDetail         string `json:"cai_notified_detail,omitempty" maxLength:"2000" doc:"Field 7a detail — date notified, or the reason it was not (when required)."`
	IndividualsNotified       bool   `json:"individuals_notified" doc:"Field 7b — whether affected individuals were notified."`
	IndividualsNotifiedDetail string `json:"individuals_notified_detail,omitempty" maxLength:"2000" doc:"Field 7b detail — date/method, or the reason they were not (when required)."`
	MeasuresTaken             string `json:"measures_taken" minLength:"1" maxLength:"10000" doc:"Field 8 — steps to reduce risk of injury and prevent recurrence."`
}

type incidentOutput struct {
	Body incidentregister.Incident
}

func (s *incidentRegisterService) file(ctx context.Context, in *fileIncidentInput) (*incidentOutput, error) {
	if err := s.guard.check(ctx, devopsResource(), actEdit); err != nil {
		return nil, err
	}

	recordedBy := "unknown"
	if u := auth.FromContext(ctx); u.ID != uuid.Nil {
		recordedBy = u.ID.String()
	}

	entry, err := s.reg.Create(ctx, incidentregister.NewIncident{
		PersonalInfoInvolved:      in.Body.PersonalInfoInvolved,
		Circumstances:             in.Body.Circumstances,
		IncidentOccurredAt:        in.Body.IncidentOccurredAt,
		BecameAwareDate:           time.Time(in.Body.BecameAwareDate),
		PeopleAffected:            in.Body.PeopleAffected,
		RiskAssessment:            in.Body.RiskAssessment,
		CAINotified:               in.Body.CAINotified,
		CAINotifiedDetail:         in.Body.CAINotifiedDetail,
		IndividualsNotified:       in.Body.IndividualsNotified,
		IndividualsNotifiedDetail: in.Body.IndividualsNotifiedDetail,
		MeasuresTaken:             in.Body.MeasuresTaken,
		RecordedBy:                recordedBy,
	})
	if err != nil {
		return nil, newAPIError(http.StatusInternalServerError, "internal_error",
			"failed to record incident", nil)
	}

	// Audit-log the append (no PII in the event — just the chain
	// coordinates and who filed it). The register itself is the durable
	// record; this is the operational breadcrumb.
	slog.WarnContext(ctx, "confidentiality incident recorded",
		"event", "incident_register.filed",
		"incident_id", entry.ID.String(),
		"seq", entry.Seq,
		"recorded_by", recordedBy,
		"retain_until", entry.RetainUntil.Format(time.DateOnly),
	)

	return &incidentOutput{Body: entry}, nil
}

type listIncidentsOutput struct {
	Body listIncidentsBody
}

type listIncidentsBody struct {
	Incidents []incidentregister.Incident `json:"incidents" doc:"Register entries in chain (chronological) order."`
	Count     int                         `json:"count" doc:"Number of entries."`
}

func (s *incidentRegisterService) list(ctx context.Context, _ *struct{}) (*listIncidentsOutput, error) {
	if err := s.guard.check(ctx, devopsResource(), actView); err != nil {
		return nil, err
	}
	entries, err := s.reg.List(ctx)
	if err != nil {
		return nil, newAPIError(http.StatusInternalServerError, "internal_error",
			"failed to list register", nil)
	}
	return &listIncidentsOutput{Body: listIncidentsBody{Incidents: entries, Count: len(entries)}}, nil
}

type getIncidentInput struct {
	ID string `path:"id" doc:"Register entry id (UUID)."`
}

func (s *incidentRegisterService) get(ctx context.Context, in *getIncidentInput) (*incidentOutput, error) {
	if err := s.guard.check(ctx, devopsResource(), actView); err != nil {
		return nil, err
	}
	id, err := parseUUID(in.ID, "id")
	if err != nil {
		return nil, err
	}
	entry, err := s.reg.GetByID(ctx, id)
	if errors.Is(err, incidentregister.ErrNotFound) {
		return nil, newAPIError(http.StatusNotFound, "incident_not_found", "no such register entry", nil)
	}
	if err != nil {
		return nil, newAPIError(http.StatusInternalServerError, "internal_error",
			"failed to read register entry", nil)
	}
	return &incidentOutput{Body: entry}, nil
}

type exportOutput struct {
	Body incidentregister.Export
}

func (s *incidentRegisterService) export(ctx context.Context, _ *struct{}) (*exportOutput, error) {
	if err := s.guard.check(ctx, devopsResource(), actView); err != nil {
		return nil, err
	}
	dump, err := s.reg.Export(ctx)
	if err != nil {
		return nil, newAPIError(http.StatusInternalServerError, "internal_error",
			"failed to export register", nil)
	}
	return &exportOutput{Body: dump}, nil
}
