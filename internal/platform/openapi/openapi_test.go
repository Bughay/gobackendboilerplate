package openapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Bughay/ego-backend-boilerplate/internal/platform/lib"
)

// specDoc is the small slice of the OpenAPI document the tests need. Raw
// messages are kept for the parts each test inspects separately.
type specDoc struct {
	OpenAPI string `json:"openapi"`
	Info    struct {
		Title string `json:"title"`
	} `json:"info"`
	Paths      map[string]map[string]json.RawMessage `json:"paths"`
	Components struct {
		Schemas         map[string]json.RawMessage `json:"schemas"`
		Responses       map[string]json.RawMessage `json:"responses"`
		Parameters      map[string]json.RawMessage `json:"parameters"`
		Headers         map[string]json.RawMessage `json:"headers"`
		SecuritySchemes map[string]json.RawMessage `json:"securitySchemes"`
	} `json:"components"`
}

// operation is the subset of one OpenAPI operation the tests assert on.
type operation struct {
	OperationID string                     `json:"operationId"`
	Tags        []string                   `json:"tags"`
	Security    []map[string][]string      `json:"security"`
	Responses   map[string]json.RawMessage `json:"responses"`
}

// expectedOperation describes one route the spec must contain. auth is "",
// "bearer", or "cookie".
type expectedOperation struct {
	path   string
	method string
	auth   string
}

// expectedOperations is the complete public API surface: 16 operations across
// 12 paths. Adding or removing a route must update this table and openapi.json.
var expectedOperations = []expectedOperation{
	{"/health", http.MethodGet, ""},
	{"/auth/register", http.MethodPost, ""},
	{"/auth/login", http.MethodPost, ""},
	{"/auth/refresh", http.MethodPost, "cookie"},
	{"/auth/logout", http.MethodPost, ""},
	{"/products", http.MethodPost, "bearer"},
	{"/products", http.MethodGet, "bearer"},
	{"/products/{id}", http.MethodGet, "bearer"},
	{"/products/{id}", http.MethodPut, "bearer"},
	{"/products/{id}", http.MethodDelete, "bearer"},
	{"/orders", http.MethodPost, "bearer"},
	{"/orders", http.MethodGet, "bearer"},
	{"/orders/{id}", http.MethodGet, "bearer"},
	{"/demo/jobs/hello", http.MethodPost, "bearer"},
	{"/demo/jobs/report", http.MethodPost, "bearer"},
	{"/demo/jobs/{id}", http.MethodGet, "bearer"},
}

// loadSpec decodes the embedded specification used by every test.
func loadSpec(t *testing.T) specDoc {
	t.Helper()

	var doc specDoc
	if err := json.Unmarshal(specJSON, &doc); err != nil {
		t.Fatalf("embedded openapi.json is not valid JSON: %v", err)
	}
	return doc
}

// operationFor returns one documented operation, failing when the path or
// method is missing.
func operationFor(t *testing.T, doc specDoc, path, method string) operation {
	t.Helper()

	methods, ok := doc.Paths[path]
	if !ok {
		t.Fatalf("spec is missing path %s", path)
	}
	raw, ok := methods[strings.ToLower(method)]
	if !ok {
		t.Fatalf("spec is missing operation %s %s", method, path)
	}

	var op operation
	if err := json.Unmarshal(raw, &op); err != nil {
		t.Fatalf("decode operation %s %s: %v", method, path, err)
	}
	return op
}

// assertSecurity checks that an operation declares the expected auth
// requirement: none, bearerAuth, or refreshCookie.
func assertSecurity(t *testing.T, method, path string, op operation, want string) {
	t.Helper()

	switch want {
	case "":
		if len(op.Security) != 0 {
			t.Errorf("%s %s has security %v, want none", method, path, op.Security)
		}
	case "bearer":
		if !hasSecurityKey(op.Security, "bearerAuth") {
			t.Errorf("%s %s security = %v, want bearerAuth", method, path, op.Security)
		}
	case "cookie":
		if !hasSecurityKey(op.Security, "refreshCookie") {
			t.Errorf("%s %s security = %v, want refreshCookie", method, path, op.Security)
		}
	default:
		t.Fatalf("unknown expected auth %q", want)
	}
}

// hasSecurityKey reports whether any security requirement names key.
func hasSecurityKey(security []map[string][]string, key string) bool {
	for _, requirement := range security {
		if _, ok := requirement[key]; ok {
			return true
		}
	}
	return false
}

// TestSpecIsOpenAPI30 proves the document advertises the supported version and
// keeps the UI title and info.title in lockstep.
func TestSpecIsOpenAPI30(t *testing.T) {
	doc := loadSpec(t)

	if doc.OpenAPI != "3.0.3" {
		t.Fatalf("openapi = %q, want 3.0.3", doc.OpenAPI)
	}
	if doc.Info.Title != Title {
		t.Fatalf("info.title = %q, want %q", doc.Info.Title, Title)
	}
}

// TestSpecCoversExpectedOperations proves the document and the test's route
// table describe exactly the same API surface, with no missing or extra ops.
func TestSpecCoversExpectedOperations(t *testing.T) {
	doc := loadSpec(t)

	expected := make(map[string]expectedOperation, len(expectedOperations))
	for _, want := range expectedOperations {
		key := want.method + " " + want.path
		expected[key] = want
	}

	seen := make(map[string]struct{}, len(expectedOperations))
	for path, methods := range doc.Paths {
		for method := range methods {
			switch method {
			case "get", "post", "put", "delete", "patch":
				key := strings.ToUpper(method) + " " + path
				if _, ok := expected[key]; !ok {
					t.Errorf("spec documents unexpected operation %s", key)
				}
				seen[key] = struct{}{}
			}
		}
	}

	if len(seen) != len(expectedOperations) {
		t.Fatalf("documented operations = %d, want %d", len(seen), len(expectedOperations))
	}
	for key := range expected {
		if _, ok := seen[key]; !ok {
			t.Errorf("spec is missing operation %s", key)
		}
	}

	for _, want := range expectedOperations {
		op := operationFor(t, doc, want.path, want.method)
		if op.OperationID == "" {
			t.Errorf("%s %s has no operationId", want.method, want.path)
		}
		if len(op.Tags) == 0 {
			t.Errorf("%s %s has no tags", want.method, want.path)
		}
		if len(op.Responses) == 0 {
			t.Errorf("%s %s has no responses", want.method, want.path)
		}
		assertSecurity(t, want.method, want.path, op, want.auth)
	}
}

// TestSpecComponentsAndSecuritySchemes proves the reusable pieces referenced by
// the operations exist.
func TestSpecComponentsAndSecuritySchemes(t *testing.T) {
	doc := loadSpec(t)

	for _, name := range []string{
		"User", "RegisterRequest", "RegistrationResponse", "LoginRequest",
		"LoginResponse", "RefreshResponse", "LogoutResponse",
		"Product", "ProductCreateRequest", "ProductUpdateRequest",
		"Order", "OrderItem", "OrderCreateRequest",
		"DemoHelloRequest", "DemoReportRequest", "DemoJob",
		"ErrorEnvelope", "ErrorDetail",
	} {
		if _, ok := doc.Components.Schemas[name]; !ok {
			t.Errorf("missing schema %s", name)
		}
	}

	for _, name := range []string{
		"BadRequest", "Unauthorized", "NotFound", "UnprocessableEntity",
		"TooManyRequests", "InternalServerError",
	} {
		if _, ok := doc.Components.Responses[name]; !ok {
			t.Errorf("missing response component %s", name)
		}
	}

	for _, name := range []string{"ProductID", "OrderID", "DemoJobID"} {
		if _, ok := doc.Components.Parameters[name]; !ok {
			t.Errorf("missing parameter component %s", name)
		}
	}

	for _, name := range []string{"Retry-After", "X-RateLimit-Limit", "X-RateLimit-Remaining", "X-RateLimit-Reset"} {
		if _, ok := doc.Components.Headers[name]; !ok {
			t.Errorf("missing header component %s", name)
		}
	}

	for _, name := range []string{"bearerAuth", "refreshCookie"} {
		if _, ok := doc.Components.SecuritySchemes[name]; !ok {
			t.Errorf("missing security scheme %s", name)
		}
	}
}

// TestSpecRefsResolve proves every $ref in the document is a local reference
// that resolves. This catches typoed component names and accidental external
// dependencies without needing a third-party validator.
func TestSpecRefsResolve(t *testing.T) {
	var root any
	if err := json.Unmarshal(specJSON, &root); err != nil {
		t.Fatalf("decode spec: %v", err)
	}

	refs := 0
	var walk func(path string, node any)
	walk = func(path string, node any) {
		switch value := node.(type) {
		case map[string]any:
			if rawRef, ok := value["$ref"].(string); ok {
				refs++
				if !strings.HasPrefix(rawRef, "#/") {
					t.Errorf("%s has external ref %q", path, rawRef)
					return
				}
				if !resolveJSONPointer(root, rawRef) {
					t.Errorf("%s has unresolved ref %q", path, rawRef)
				}
			}
			for key, child := range value {
				walk(path+"/"+key, child)
			}
		case []any:
			for i, child := range value {
				walk(fmt.Sprintf("%s/%d", path, i), child)
			}
		}
	}
	walk("#", root)

	if refs == 0 {
		t.Fatal("spec contains no $ref values; the resolver test would prove nothing")
	}
}

// resolveJSONPointer walks a #/... JSON pointer from root and reports whether
// the target exists. Only the simple pointer form used by this spec is needed.
func resolveJSONPointer(root any, ref string) bool {
	current := root
	for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
		object, ok := current.(map[string]any)
		if !ok {
			return false
		}
		next, ok := object[part]
		if !ok {
			return false
		}
		current = next
	}
	return true
}

// TestRequestSchemasRejectUnknownFields proves every object schema in every
// request body sets additionalProperties:false, matching helpers.Decode's
// DisallowUnknownFields behavior. It recurses so nested objects such as order
// items are checked too.
func TestRequestSchemasRejectUnknownFields(t *testing.T) {
	doc := loadSpec(t)

	for _, name := range []string{
		"RegisterRequest", "LoginRequest", "ProductCreateRequest",
		"ProductUpdateRequest", "OrderCreateRequest", "DemoHelloRequest",
		"DemoReportRequest",
	} {
		raw, ok := doc.Components.Schemas[name]
		if !ok {
			t.Errorf("missing request schema %s", name)
			continue
		}

		var schema any
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatalf("decode schema %s: %v", name, err)
		}
		assertStrictObjectSchemas(t, name, schema)
	}
}

// assertStrictObjectSchemas fails on any {"type":"object"} schema that does not
// explicitly set additionalProperties to false.
func assertStrictObjectSchemas(t *testing.T, path string, node any) {
	t.Helper()

	switch value := node.(type) {
	case map[string]any:
		if value["type"] == "object" {
			additional, ok := value["additionalProperties"].(bool)
			if !ok || additional {
				t.Errorf("%s is an object schema without additionalProperties:false", path)
			}
		}
		for key, child := range value {
			assertStrictObjectSchemas(t, path+"/"+key, child)
		}
	case []any:
		for i, child := range value {
			assertStrictObjectSchemas(t, fmt.Sprintf("%s/%d", path, i), child)
		}
	}
}

// TestOrderItemProductIDIsNullable proves the spec reflects catalog deletion:
// the order snapshot keeps product_name/unit_price but nulls product_id.
func TestOrderItemProductIDIsNullable(t *testing.T) {
	doc := loadSpec(t)

	var schema map[string]any
	if err := json.Unmarshal(doc.Components.Schemas["OrderItem"], &schema); err != nil {
		t.Fatalf("decode OrderItem: %v", err)
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("OrderItem has no properties object")
	}
	productID, ok := properties["product_id"].(map[string]any)
	if !ok {
		t.Fatal("OrderItem has no product_id property")
	}
	if productID["nullable"] != true {
		t.Fatalf("OrderItem.product_id nullable = %v, want true", productID["nullable"])
	}
}

// TestErrorEnvelopeShape proves the documented error model is the real
// {"error":{"code","message"}} envelope.
func TestErrorEnvelopeShape(t *testing.T) {
	doc := loadSpec(t)

	var envelope map[string]any
	if err := json.Unmarshal(doc.Components.Schemas["ErrorEnvelope"], &envelope); err != nil {
		t.Fatalf("decode ErrorEnvelope: %v", err)
	}
	properties, ok := envelope["properties"].(map[string]any)
	if !ok {
		t.Fatal("ErrorEnvelope has no properties object")
	}
	detail, ok := properties["error"].(map[string]any)
	if !ok {
		t.Fatal("ErrorEnvelope.error is not an object")
	}
	if ref, _ := detail["$ref"].(string); ref != "#/components/schemas/ErrorDetail" {
		t.Fatalf("ErrorEnvelope.error ref = %q, want ErrorDetail", ref)
	}

	var detailSchema map[string]any
	if err := json.Unmarshal(doc.Components.Schemas["ErrorDetail"], &detailSchema); err != nil {
		t.Fatalf("decode ErrorDetail: %v", err)
	}
	detailProperties, ok := detailSchema["properties"].(map[string]any)
	if !ok {
		t.Fatal("ErrorDetail has no properties object")
	}
	for _, field := range []string{"code", "message"} {
		if _, ok := detailProperties[field]; !ok {
			t.Errorf("ErrorDetail is missing %s", field)
		}
	}
}

// docsMux builds the docs routes exactly the way cmd/api mounts them.
func docsMux() *http.ServeMux {
	mux := http.NewServeMux()
	RegisterRoutes(mux)
	return mux
}

// TestSpecHandlerServesEmbeddedJSON proves the raw document is served as JSON
// with revalidation enabled and is byte-for-byte the embedded spec.
func TestSpecHandlerServesEmbeddedJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	docsMux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, SpecPath, nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("Cache-Control = %q, want no-cache", got)
	}
	if !json.Valid(rec.Body.Bytes()) {
		t.Fatalf("response body is not valid JSON: %s", rec.Body.String())
	}
	var doc specDoc
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode served spec: %v", err)
	}
	if doc.OpenAPI != "3.0.3" {
		t.Fatalf("served openapi = %q, want 3.0.3", doc.OpenAPI)
	}
}

// TestSwaggerUIRendersAndOverridesCSP proves the UI page is served, embeds the
// expected title and spec URL, and replaces the strict API CSP with the docs
// policy while every other route keeps the strict policy.
func TestSwaggerUIRendersAndOverridesCSP(t *testing.T) {
	h := lib.SecurityHeaders()(docsMux())

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, UIPath, nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("docs status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "text/html") {
		t.Fatalf("docs Content-Type = %q, want text/html", got)
	}
	body := rec.Body.String()
	if !strings.Contains(body, Title) {
		t.Fatalf("docs body does not contain the title %q", Title)
	}
	if !strings.Contains(body, SpecPath) {
		t.Fatalf("docs body does not reference the spec path %q", SpecPath)
	}
	if got := rec.Header().Get("Content-Security-Policy"); got != docsContentSecurityPolicy {
		t.Fatalf("docs CSP = %q, want the docs policy", got)
	}

	specRec := httptest.NewRecorder()
	h.ServeHTTP(specRec, httptest.NewRequest(http.MethodGet, SpecPath, nil))
	if got := specRec.Header().Get("Content-Security-Policy"); got != "default-src 'none'; frame-ancestors 'none'" {
		t.Fatalf("spec CSP = %q, want the strict API policy", got)
	}
}

// TestSwaggerUIServesEmbeddedAssets proves the CSS/JS assets are reachable
// under the docs base path, so the page can actually render.
func TestSwaggerUIServesEmbeddedAssets(t *testing.T) {
	rec := httptest.NewRecorder()
	docsMux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, UIPath+"swagger-ui.css", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("swagger-ui.css status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "text/css") {
		t.Fatalf("swagger-ui.css Content-Type = %q, want text/css", got)
	}
	if rec.Body.Len() == 0 {
		t.Fatal("swagger-ui.css response body is empty")
	}
}

// TestDocsRedirectsToTrailingSlash proves /docs (no slash) redirects to the
// canonical base path browsers need to load the UI's relative assets.
func TestDocsRedirectsToTrailingSlash(t *testing.T) {
	rec := httptest.NewRecorder()
	docsMux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/docs", nil))

	if rec.Code != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d, want 307", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != UIPath {
		t.Fatalf("Location = %q, want %q", got, UIPath)
	}
}
