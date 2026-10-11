package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// problemBase prefixes every RFC 9457 problem type URI.
const problemBase = "https://service-scheduler.example/problems/"

// newProblem builds an RFC 9457 problem. slug names the problem type.
func newProblem(status int, slug, title, detail string) Problem {
	p := Problem{Type: problemBase + slug, Title: title, Status: status}
	if detail != "" {
		p.Detail = &detail
	}
	return p
}

// writeProblem writes p as application/problem+json. It serves the paths
// the generated handlers do not: request binding errors and unexpected
// handler errors.
func writeProblem(w http.ResponseWriter, p Problem) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(p.Status)
	_ = json.NewEncoder(w).Encode(p)
}

// badRequest handles malformed parameters and bodies.
func badRequest(w http.ResponseWriter, _ *http.Request, err error) {
	writeProblem(w, newProblem(http.StatusBadRequest, "invalid-request", "The request is invalid", err.Error()))
}

// internalError logs an unexpected handler error and returns a 500 whose
// body reveals nothing about the cause.
func internalError(logger *slog.Logger) func(http.ResponseWriter, *http.Request, error) {
	return func(w http.ResponseWriter, r *http.Request, err error) {
		logger.ErrorContext(r.Context(), "request failed",
			"method", r.Method, "path", r.URL.Path, "error", err)
		writeProblem(w, newProblem(http.StatusInternalServerError, "internal", "Internal server error", ""))
	}
}
