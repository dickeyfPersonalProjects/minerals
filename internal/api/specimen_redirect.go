package api

import "net/http"

// registerSpecimenRedirect wires a GET /specimens/{id} handler that
// 302s to /#/specimens/{id}. The QR sheet PDF endpoint (mi-c78.2)
// encodes sticker payloads as https://<host>/specimens/{id}, but the
// SPA uses hash-based routing (svelte-spa-router). Without this shim,
// scanning a sticker lands the user on the SPA root instead of the
// specimen detail page (mi-1rg).
//
// The Go 1.22+ pattern syntax constrains the match to a single path
// segment, so /specimens/{id}/anything-else falls through to the SPA
// fallback. Sub-segment paths are not part of the QR contract.
func registerSpecimenRedirect(mux *http.ServeMux) {
	mux.HandleFunc("GET /specimens/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		w.Header().Set("Location", "/#/specimens/"+id)
		w.WriteHeader(http.StatusFound)
	})
}
