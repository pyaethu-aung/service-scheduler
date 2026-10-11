package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
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

// writeProblem writes body as application/problem+json with status. It
// serves the paths the generated handlers do not: request binding errors and
// unexpected handler errors.
func writeProblem(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// requestError handles malformed parameters and bodies, keeping each
// route's documented statuses (Section 7): POST /v1/appointments has no 400,
// so it answers 422 with the offending parameter; an appointment id that is
// not a UUID cannot exist, so it is a 404; every other route answers 400.
func requestError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/v1/appointments":
		writeProblem(w, http.StatusUnprocessableEntity,
			validationProblem(InvalidParam{Name: paramName(err), Reason: err.Error()}))
		return
	case strings.HasPrefix(r.URL.Path, "/v1/appointments/"):
		writeProblem(w, http.StatusNotFound,
			newProblem(http.StatusNotFound, "not-found", "Appointment not found", err.Error()))
		return
	}
	writeProblem(w, http.StatusBadRequest,
		newProblem(http.StatusBadRequest, "invalid-request", "The request is invalid", err.Error()))
}

// paramName names the parameter or body field a binding error is about,
// or "body" when the JSON body as a whole could not be decoded.
func paramName(err error) string {
	var (
		required *RequiredHeaderError
		format   *InvalidParamFormatError
		param    *RequiredParamError
		typed    *json.UnmarshalTypeError
	)
	switch {
	case errors.As(err, &required):
		return required.ParamName
	case errors.As(err, &format):
		return format.ParamName
	case errors.As(err, &param):
		return param.ParamName
	case errors.As(err, &typed) && typed.Field != "":
		return typed.Field
	}
	return "body"
}

// internalError logs an unexpected handler error and returns a 500 whose
// body reveals nothing about the cause.
func internalError(logger *slog.Logger) func(http.ResponseWriter, *http.Request, error) {
	return func(w http.ResponseWriter, r *http.Request, err error) {
		logger.ErrorContext(r.Context(), "request failed",
			"method", r.Method, "path", r.URL.Path, "error", err)
		writeProblem(w, http.StatusInternalServerError,
			newProblem(http.StatusInternalServerError, "internal", "Internal server error", ""))
	}
}
