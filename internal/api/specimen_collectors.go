// Specimen↔collector chain HTTP surface (mi-zv3 / C-3). Implements
// the two §10 chain endpoints on top of domain.SpecimenCollectorRepo:
//
//	GET /api/v1/specimens/{id}/collectors
//	PUT /api/v1/specimens/{id}/collectors
//
// The chain is edited atomically: PUT replaces every link for a
// specimen with the supplied ordered list of collector_ids. Per the
// bead acceptance criteria there is intentionally NO per-link DELETE
// endpoint — chains are short (typically 0-3) and atomic replace
// matches the user's mental model of "editing the chain."
package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
)

// SpecimenCollectorLinkView is one row of a specimen's collector
// chain on the wire. The collector is embedded in full so the client
// doesn't need a second round-trip to render the chain.
type SpecimenCollectorLinkView struct {
	Collector CollectorView `json:"collector" doc:"The collector at this position in the chain."`
	Position  int           `json:"position" doc:"1-indexed position within the chain."`
}

func toSpecimenCollectorLinkView(l domain.SpecimenCollectorLink) SpecimenCollectorLinkView {
	return SpecimenCollectorLinkView{
		Collector: toCollectorView(l.Collector),
		Position:  l.Position,
	}
}

type getSpecimenCollectorsInput struct {
	ID string `path:"id" doc:"Specimen UUID."`
}

type specimenCollectorsBody struct {
	Items []SpecimenCollectorLinkView `json:"items" doc:"Collector chain in position-ascending order. Empty when the specimen has no collectors."`
}

type specimenCollectorsOutput struct {
	Body specimenCollectorsBody
}

type putSpecimenCollectorsInput struct {
	ID   string                    `path:"id" doc:"Specimen UUID."`
	Body putSpecimenCollectorsBody `json:"body"`
}

type putSpecimenCollectorsBody struct {
	CollectorIDs []string `json:"collector_ids" doc:"Ordered list of collector UUIDs. Array index becomes the chain position (1-indexed). Pass an empty array to clear the chain."`
}

// SpecimenCollectorService wires huma operations against a
// domain.SpecimenCollectorRepo. The specimen repo is held alongside
// so the GET handler can return 404 when the specimen itself is
// missing (vs an empty 200 for a known specimen with no chain).
type SpecimenCollectorService struct {
	specimens domain.SpecimenRepo
	links     domain.SpecimenCollectorRepo
}

func registerSpecimenCollectorOperations(
	api huma.API, specimens domain.SpecimenRepo, links domain.SpecimenCollectorRepo,
) {
	if specimens == nil || links == nil {
		return
	}
	s := &SpecimenCollectorService{specimens: specimens, links: links}
	mws := huma.Middlewares{humaAuth}

	huma.Register(api, huma.Operation{
		OperationID: "get-specimen-collectors",
		Method:      http.MethodGet,
		Path:        "/api/v1/specimens/{id}/collectors",
		Summary:     "Get a specimen's collector chain",
		Description: "Returns the ordered collector chain for the specimen. " +
			"Empty array when the specimen has no collectors. 404 when the specimen does not exist.",
		Tags:        []string{"specimens"},
		Errors:      []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusNotFound},
		Middlewares: mws,
	}, s.get)

	huma.Register(api, huma.Operation{
		OperationID: "put-specimen-collectors",
		Method:      http.MethodPut,
		Path:        "/api/v1/specimens/{id}/collectors",
		Summary:     "Replace a specimen's collector chain",
		Description: "Atomically replaces every link in the chain with the supplied collector_ids in order (array index = position, 1-indexed). " +
			"Pass an empty array to clear the chain. Returns 404 when the specimen does not exist; 404 when any collector_id does not exist; 400 when the body contains duplicate ids.",
		Tags:        []string{"specimens"},
		Errors:      []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusNotFound},
		Middlewares: mws,
	}, s.put)
}

func (s *SpecimenCollectorService) get(
	ctx context.Context, in *getSpecimenCollectorsInput,
) (*specimenCollectorsOutput, error) {
	id, err := parseUUID(in.ID, "id")
	if err != nil {
		return nil, err
	}
	// Probe specimen existence so an unknown id returns 404 rather
	// than a misleading empty 200.
	if _, err := s.specimens.GetByID(ctx, id); err != nil {
		return nil, mapSpecimenError(err)
	}
	links, err := s.links.GetChain(ctx, nil, id)
	if err != nil {
		return nil, newAPIError(http.StatusInternalServerError, "internal_error",
			"internal server error", nil)
	}
	items := make([]SpecimenCollectorLinkView, 0, len(links))
	for _, l := range links {
		items = append(items, toSpecimenCollectorLinkView(l))
	}
	return &specimenCollectorsOutput{Body: specimenCollectorsBody{Items: items}}, nil
}

func (s *SpecimenCollectorService) put(
	ctx context.Context, in *putSpecimenCollectorsInput,
) (*specimenCollectorsOutput, error) {
	id, err := parseUUID(in.ID, "id")
	if err != nil {
		return nil, err
	}

	ids := make([]uuid.UUID, 0, len(in.Body.CollectorIDs))
	seen := make(map[uuid.UUID]struct{}, len(in.Body.CollectorIDs))
	for i, raw := range in.Body.CollectorIDs {
		cid, perr := uuid.Parse(raw)
		if perr != nil {
			return nil, newAPIError(http.StatusBadRequest, "invalid_collector_id",
				"collector_ids must be UUIDs",
				map[string]any{"field": "collector_ids", "index": i})
		}
		if _, dup := seen[cid]; dup {
			return nil, newAPIError(http.StatusBadRequest, "duplicate_collector_id",
				"collector_ids must not contain duplicates",
				map[string]any{"field": "collector_ids", "index": i, "id": cid.String()})
		}
		seen[cid] = struct{}{}
		ids = append(ids, cid)
	}

	if err := s.links.ReplaceChain(ctx, nil, id, ids); err != nil {
		return nil, mapSpecimenCollectorError(err)
	}

	links, err := s.links.GetChain(ctx, nil, id)
	if err != nil {
		return nil, newAPIError(http.StatusInternalServerError, "internal_error",
			"internal server error", nil)
	}
	items := make([]SpecimenCollectorLinkView, 0, len(links))
	for _, l := range links {
		items = append(items, toSpecimenCollectorLinkView(l))
	}
	return &specimenCollectorsOutput{Body: specimenCollectorsBody{Items: items}}, nil
}

// mapSpecimenCollectorError translates ReplaceChain sentinels into
// §10 envelope errors. The chain endpoints don't surface the
// ErrSpecimenReferenced / ErrSpecimenTypeImmutable sentinels — they
// can't be raised by the join-table operations.
func mapSpecimenCollectorError(err error) error {
	switch {
	case errors.Is(err, domain.ErrSpecimenNotFound):
		return newAPIError(http.StatusNotFound, "specimen_not_found",
			"no such specimen", nil)
	case errors.Is(err, domain.ErrCollectorNotFound):
		return newAPIError(http.StatusNotFound, "collector_not_found",
			"one or more collector_ids do not exist",
			map[string]any{"field": "collector_ids"})
	case errors.Is(err, domain.ErrCollectorConflict):
		return newAPIError(http.StatusBadRequest, "duplicate_collector_id",
			"collector_ids must not contain duplicates",
			map[string]any{"field": "collector_ids"})
	}
	return newAPIError(http.StatusInternalServerError, "internal_error",
		"internal server error", nil)
}
