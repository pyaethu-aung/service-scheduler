package api

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/gorillamux"
)

// kin-openapi checks "date" against a structural regex only and does not
// check "uuid" at all (its built-in pattern also rejects UUIDv7). Register
// both so the contract tests enforce the formats the document declares.
func init() {
	openapi3.DefineStringFormatValidator("uuid", openapi3.NewRegexpFormatValidator(
		`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`))
	openapi3.DefineStringFormatValidator("date", openapi3.NewCallbackValidator(func(s string) error {
		_, err := time.Parse(time.DateOnly, s)
		return err
	}))
}

// contract validates requests and responses against the embedded OpenAPI
// document, so every handler test doubles as a contract test.
type contract struct {
	router routers.Router
}

func newContract(t *testing.T) *contract {
	t.Helper()

	spec, err := GetSwagger()
	if err != nil {
		t.Fatalf("load embedded spec: %v", err)
	}
	if err := spec.Validate(context.Background()); err != nil {
		t.Fatalf("spec is invalid: %v", err)
	}
	// Match on path only; the servers entry names a deployment host.
	spec.Servers = nil

	router, err := gorillamux.NewRouter(spec)
	if err != nil {
		t.Fatalf("build spec router: %v", err)
	}
	return &contract{router: router}
}

// do serves req through h and fails the test if the request or the response
// does not conform to the contract.
func (c *contract) do(t *testing.T, h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	return c.serve(t, h, req, true)
}

// doInvalid is do for a request that breaks the contract on purpose: only
// the response is validated.
func (c *contract) doInvalid(t *testing.T, h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	return c.serve(t, h, req, false)
}

func (c *contract) serve(t *testing.T, h http.Handler, req *http.Request, validateRequest bool) *httptest.ResponseRecorder {
	t.Helper()

	var body []byte
	if req.Body != nil {
		var err error
		if body, err = io.ReadAll(req.Body); err != nil {
			t.Fatalf("read request body: %v", err)
		}
		req.Body = io.NopCloser(bytes.NewReader(body))
	}

	route, params, err := c.router.FindRoute(req)
	if err != nil {
		t.Fatalf("%s %s is not in the contract: %v", req.Method, req.URL.Path, err)
	}
	reqInput := &openapi3filter.RequestValidationInput{
		Request:    req,
		PathParams: params,
		Route:      route,
		Options:    &openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc},
	}
	if validateRequest {
		if err := openapi3filter.ValidateRequest(req.Context(), reqInput); err != nil {
			t.Fatalf("request does not match the contract: %v", err)
		}
	} else if err := openapi3filter.ValidateRequest(req.Context(), reqInput); err == nil {
		t.Fatalf("request was meant to break the contract but is valid")
	}
	req.Body = io.NopCloser(bytes.NewReader(body))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	resInput := &openapi3filter.ResponseValidationInput{
		RequestValidationInput: reqInput,
		Status:                 rec.Code,
		Header:                 rec.Header(),
		Body:                   io.NopCloser(bytes.NewReader(rec.Body.Bytes())),
		Options:                &openapi3filter.Options{IncludeResponseStatus: true},
	}
	if err := openapi3filter.ValidateResponse(req.Context(), resInput); err != nil {
		t.Fatalf("response %d does not match the contract: %v\nbody: %s", rec.Code, err, rec.Body.String())
	}
	return rec
}

// Compile-time check that the embedded spec type is what kin-openapi expects.
var _ func() (*openapi3.T, error) = GetSwagger

func TestContract_Healthz(t *testing.T) {
	t.Parallel()

	c := newContract(t)
	h := NewRouter(NewServer(nil, slog.New(slog.DiscardHandler)))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", nil)
	rec := c.do(t, h, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got, want := rec.Body.String(), "{\"status\":\"ok\"}\n"; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}
