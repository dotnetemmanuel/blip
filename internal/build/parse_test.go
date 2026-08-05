package build

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/dotnetemmanuel/blip/internal/output"
)

func load(t *testing.T, name string) *API {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	api, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse(%s): %v", name, err)
	}
	return api
}

func find(t *testing.T, api *API, group, name string) *Operation {
	t.Helper()
	for _, op := range api.Operations {
		if op.Group == group && op.Name == name {
			return op
		}
	}
	t.Fatalf("no operation %s %s; have %s", group, name, strings.Join(names(api), ", "))
	return nil
}

func TestResponsesListsEveryStatusIncludingThoseWithNoBody(t *testing.T) {
	api := load(t, "dotnet9-minimal.json")
	get := find(t, api, "orders", "get")

	got := get.Responses()
	if len(got) != 2 {
		t.Fatalf("Responses() = %+v, want 2 entries (200 and 404)", got)
	}
	if got[0].Status != "200" || got[0].Schema != "Order" {
		t.Errorf("first response = %+v, want status 200 referencing Order", got[0])
	}
	if got[1].Status != "404" || got[1].Schema != "" || got[1].Type != "" {
		t.Errorf("second response = %+v, want status 404 with no schema", got[1])
	}
}

func TestResponsesSortsDefaultLast(t *testing.T) {
	spec := `{"openapi":"3.0.1","info":{"title":"Sample","version":"1"},"paths":{
		"/api/widgets":{"get":{"operationId":"listWidgets","responses":{
			"default":{"description":"error"},
			"404":{"description":"missing"},
			"200":{"description":"ok"}}}}}}`
	api, err := Parse([]byte(spec))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	got := api.Operations[0].Responses()
	want := []string{"200", "404", "default"}
	if len(got) != len(want) {
		t.Fatalf("Responses() = %+v, want %d entries", got, len(want))
	}
	for i, status := range want {
		if got[i].Status != status {
			t.Errorf("Responses()[%d].Status = %q, want %q", i, got[i].Status, status)
		}
	}
}

func names(api *API) []string {
	out := make([]string, 0, len(api.Operations))
	for _, op := range api.Operations {
		out = append(out, op.Group+" "+op.Name)
	}
	return out
}

func TestParseNamesFromOperationIds(t *testing.T) {
	api := load(t, "dotnet9-minimal.json")

	tests := []struct {
		group, name, method, path string
	}{
		{"orders", "list", "GET", "/api/orders"},
		{"orders", "create", "POST", "/api/orders"},
		{"orders", "get", "GET", "/api/orders/{id}"},
		{"orders", "cancel", "POST", "/api/orders/{id}/cancel"},
	}

	for _, tt := range tests {
		t.Run(tt.group+" "+tt.name, func(t *testing.T) {
			op := find(t, api, tt.group, tt.name)
			if op.Method != tt.method || op.Path != tt.path {
				t.Errorf("got %s %s, want %s %s", op.Method, op.Path, tt.method, tt.path)
			}
			if op.Derived {
				t.Error("Derived = true, want false for an operation with an operationId")
			}
		})
	}
}

func TestParseDerivesNamesWithoutOperationIds(t *testing.T) {
	api := load(t, "dotnet9-minimal.json")

	tests := []struct {
		group, name, method, path string
	}{
		{"orders", "delete-by-id", "DELETE", "/api/orders/{id}"},
		{"orders", "lines-get-by-id-by-line-id", "GET", "/api/orders/{id}/lines/{lineId}"},
		{"customers", "orders-get-by-customer-id", "GET", "/api/customers/{customerId}/orders"},
		{"healthz", "get", "GET", "/healthz"},
	}

	for _, tt := range tests {
		t.Run(tt.group+" "+tt.name, func(t *testing.T) {
			op := find(t, api, tt.group, tt.name)
			if op.Method != tt.method || op.Path != tt.path {
				t.Errorf("got %s %s, want %s %s", op.Method, op.Path, tt.method, tt.path)
			}
			if !op.Derived {
				t.Error("Derived = false, want true")
			}
		})
	}
}

func TestParseWarnsOnceAboutDerivedNames(t *testing.T) {
	api := load(t, "dotnet9-minimal.json")

	derivedWarnings := 0
	for _, w := range api.Warnings {
		if strings.Contains(w, "no operationId") {
			derivedWarnings++
		}
	}
	if derivedWarnings != 1 {
		t.Errorf("warnings about derived names = %d, want exactly 1: %v", derivedWarnings, api.Warnings)
	}
	for _, want := range []string{"DELETE /api/orders/{id}", "WithName"} {
		if !strings.Contains(strings.Join(api.Warnings, " "), want) {
			t.Errorf("warnings do not mention %q: %v", want, api.Warnings)
		}
	}
}

func TestParseIsDeterministic(t *testing.T) {
	first := strings.Join(names(load(t, "dotnet9-minimal.json")), "|")
	for i := 0; i < 20; i++ {
		if got := strings.Join(names(load(t, "dotnet9-minimal.json")), "|"); got != first {
			t.Fatalf("run %d produced a different tree:\n%s\n%s", i, got, first)
		}
	}
}

func TestParseParameters(t *testing.T) {
	api := load(t, "dotnet9-minimal.json")

	list := find(t, api, "orders", "list")
	query := list.ParamsIn(InQuery)
	if len(query) != 4 {
		t.Fatalf("query params = %d, want 4", len(query))
	}

	byName := map[string]Param{}
	for _, p := range query {
		byName[p.Name] = p
	}
	if got := byName["page"].Type; got != TypeInteger {
		t.Errorf("page type = %q, want integer", got)
	}
	if got := byName["page"].Default; got != "1" {
		t.Errorf("page default = %q, want 1", got)
	}
	if got := byName["tag"].Type; got != TypeArray {
		t.Errorf("tag type = %q, want array", got)
	}
	if got := byName["tag"].ItemType; got != TypeString {
		t.Errorf("tag item type = %q, want string", got)
	}
	if got := strings.Join(byName["status"].Enum, ","); got != "open,shipped,cancelled" {
		t.Errorf("status enum = %q", got)
	}
	if byName["status"].Description == "" {
		t.Error("status has no description; spec documentation was dropped")
	}
}

func TestParsePathAndHeaderParameters(t *testing.T) {
	api := load(t, "dotnet9-minimal.json")
	get := find(t, api, "orders", "get")

	pathParams := get.PathParams()
	if len(pathParams) != 1 || pathParams[0].Name != "id" {
		t.Fatalf("path params = %+v, want just id", pathParams)
	}
	if !pathParams[0].Required {
		t.Error("path param is not required; path params always are")
	}

	headers := get.ParamsIn(InHeader)
	if len(headers) != 1 || headers[0].Name != "X-Tenant" {
		t.Errorf("header params = %+v, want X-Tenant", headers)
	}
}

func TestPathParamsFollowThePathOrder(t *testing.T) {
	api := load(t, "dotnet9-minimal.json")
	op := find(t, api, "orders", "lines-get-by-id-by-line-id")

	got := []string{}
	for _, p := range op.PathParams() {
		got = append(got, p.Name)
	}
	if strings.Join(got, ",") != "id,lineId" {
		t.Errorf("path params = %v, want id then lineId", got)
	}
}

func TestParseFlatBodyGetsFields(t *testing.T) {
	api := load(t, "dotnet9-minimal.json")
	create := find(t, api, "orders", "create")

	if create.Body == nil {
		t.Fatal("no body")
	}
	if !create.Body.Flat {
		t.Error("Flat = false, want true for an object of scalars")
	}
	if create.Body.Schema != "CreateOrderRequest" {
		t.Errorf("Schema = %q, want CreateOrderRequest", create.Body.Schema)
	}
	if !create.Body.Required {
		t.Error("Required = false, want true")
	}

	byName := map[string]Field{}
	for _, f := range create.Body.Fields {
		byName[f.Name] = f
	}
	if !byName["sku"].Required {
		t.Error("sku is not marked required")
	}
	if byName["quantity"].Type != TypeInteger {
		t.Errorf("quantity type = %q, want integer", byName["quantity"].Type)
	}
	if byName["express"].Type != TypeBoolean {
		t.Errorf("express type = %q, want boolean", byName["express"].Type)
	}
}

func TestParseSummaryAndDescriptionSurvive(t *testing.T) {
	api := load(t, "dotnet9-minimal.json")
	list := find(t, api, "orders", "list")

	if list.Summary != "List orders" {
		t.Errorf("Summary = %q", list.Summary)
	}
	if !strings.Contains(list.Description, "newest first") {
		t.Errorf("Description = %q, want the spec text", list.Description)
	}
}

func TestParseSwashbuckleNames(t *testing.T) {
	api := load(t, "swashbuckle.json")

	tests := []struct{ group, name string }{
		{"products", "get-all"},
		{"products", "create"},
		{"products", "get-by-id"},
		{"products", "update"},
		{"inventory", "adjust"},
	}

	for _, tt := range tests {
		t.Run(tt.group+" "+tt.name, func(t *testing.T) {
			find(t, api, tt.group, tt.name)
		})
	}
}

func TestParseGroupsUntaggedOperationsByPath(t *testing.T) {
	api := load(t, "no-tags.json")

	groups := strings.Join(api.Groups(), ",")
	if groups != "exports,reports" {
		t.Errorf("groups = %q, want exports,reports", groups)
	}
	find(t, api, "reports", "get")
	find(t, api, "reports", "post")
	find(t, api, "reports", "get-by-report-id")
	find(t, api, "exports", "download-get-by-export-id")
}

func TestFullNameIsStable(t *testing.T) {
	api := load(t, "dotnet9-minimal.json")

	if got := find(t, api, "orders", "list").FullName(); got != "listOrders" {
		t.Errorf("FullName = %q, want the operationId", got)
	}
	if got := find(t, api, "orders", "delete-by-id").FullName(); got != "orders-delete-by-id" {
		t.Errorf("FullName = %q, want group and name", got)
	}
}

func TestFind(t *testing.T) {
	api := load(t, "dotnet9-minimal.json")

	if op := api.Find("listOrders"); op == nil || op.Path != "/api/orders" {
		t.Errorf("Find(listOrders) = %v", op)
	}
	if op := api.Find("orders-delete-by-id"); op == nil || op.Method != "DELETE" {
		t.Errorf("Find(orders-delete-by-id) = %v", op)
	}
	if op := api.Find("nope"); op != nil {
		t.Errorf("Find(nope) = %v, want nil", op)
	}
}

func TestParseRefusesSwagger2(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "swagger20-petstore.json"))
	if err != nil {
		t.Fatal(err)
	}

	api, err := Parse(data)
	if err == nil {
		t.Fatalf("Parse succeeded on a Swagger 2.0 document, got %d operations", len(api.Operations))
	}
	if !strings.Contains(err.Error(), "2.0") || !strings.Contains(err.Error(), "OpenAPI 3") {
		t.Errorf("error = %q, want it to name the version found and say only OpenAPI 3.x is supported", err.Error())
	}
	if got := output.ExitCodeFor(err); got != output.ExitConfig {
		t.Errorf("exit code = %d, want ExitConfig (%d)", got, output.ExitConfig)
	}
}

func TestParseRefusesSwagger1(t *testing.T) {
	spec := `{"swagger":"1.2","info":{"title":"x","version":"1"}}`

	_, err := Parse([]byte(spec))
	if err == nil {
		t.Fatal("Parse succeeded on a Swagger 1.2 document")
	}
	if !strings.Contains(err.Error(), "1.2") {
		t.Errorf("error = %q, want it to name the version found", err.Error())
	}
}

func TestParseDoesNotRefuseAnUnrelatedNestedSwaggerField(t *testing.T) {
	// Only a top-level swagger key should refuse; this one is nested.
	spec := `{"openapi":"3.0.1","info":{"title":"x","version":"1"},"paths":{
		"/things":{"get":{"tags":["things"],"operationId":"getThings","responses":{"200":{"description":"OK",
		"content":{"application/json":{"schema":{"type":"object","properties":{
		"swagger":{"type":"string"}}}}}}}}}}}`

	api, err := Parse([]byte(spec))
	if err != nil {
		t.Fatalf("Parse refused a 3.x document over a nested field named swagger: %v", err)
	}
	if len(api.Operations) != 1 {
		t.Errorf("operations = %d, want 1", len(api.Operations))
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	if _, err := Parse([]byte("not a spec")); err == nil {
		t.Error("Parse succeeded on garbage")
	}
}

func TestParseEmptySpec(t *testing.T) {
	api, err := Parse([]byte(`{"openapi":"3.0.1","info":{"title":"x","version":"1"},"paths":{}}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(api.Operations) != 0 {
		t.Errorf("operations = %d, want 0", len(api.Operations))
	}
}

func TestReservedGroupIsRenamed(t *testing.T) {
	spec := `{"openapi":"3.0.1","info":{"title":"x","version":"1"},"paths":{
		"/spec/things":{"get":{"tags":["spec"],"operationId":"listThings","responses":{"200":{"description":"OK"}}}}}}`

	api, err := Parse([]byte(spec))
	if err != nil {
		t.Fatal(err)
	}
	if got := api.Operations[0].Group; got != "spec-api" {
		t.Errorf("group = %q, want spec-api", got)
	}
	if len(api.Warnings) == 0 {
		t.Error("no warning about the renamed group")
	}
}

func TestCollidingNamesAreNumbered(t *testing.T) {
	spec := `{"openapi":"3.0.1","info":{"title":"x","version":"1"},"paths":{
		"/things":{"get":{"tags":["things"],"operationId":"get","responses":{"200":{"description":"OK"}}}},
		"/other":{"get":{"tags":["things"],"operationId":"get","responses":{"200":{"description":"OK"}}}}}}`

	api, err := Parse([]byte(spec))
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(names(api), ",")
	if got != "things get,things get-2" {
		t.Errorf("names = %q, want the collision numbered", got)
	}
}

func TestKebab(t *testing.T) {
	tests := []struct{ in, want string }{
		{"listOrders", "list-orders"},
		{"Products_GetAll", "products-get-all"},
		{"Order Lines", "order-lines"},
		{"getHTTPStatus", "get-http-status"},
		{"already-kebab", "already-kebab"},
		{"UPPER", "upper"},
	}

	for _, tt := range tests {
		if got := kebab(tt.in); got != tt.want {
			t.Errorf("kebab(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestDeriveName(t *testing.T) {
	tests := []struct {
		method, path, want string
	}{
		{"GET", "/api/orders/{id}", "orders-get-by-id"},
		{"DELETE", "/api/orders/{id}", "orders-delete-by-id"},
		{"GET", "/api/orders", "orders-get"},
		{"POST", "/admin/reindex/{tenant}", "admin-reindex-post-by-tenant"},
		{"GET", "/healthz", "healthz-get"},
		{"GET", "/api/v1/reports", "reports-get"},
		{"GET", "/", "get"},
	}

	for _, tt := range tests {
		if got := deriveName(tt.method, tt.path); got != tt.want {
			t.Errorf("deriveName(%s, %s) = %q, want %q", tt.method, tt.path, got, tt.want)
		}
	}
}

func TestShortName(t *testing.T) {
	tests := []struct {
		full, group, want string
	}{
		{"list-orders", "orders", "list"},
		{"orders-list", "orders", "list"},
		{"get-order", "orders", "get"},
		{"products-get-all", "products", "get-all"},
		{"create", "orders", "create"},
		{"orders", "orders", "orders"},
		{"list-categories", "categories", "list"},
		{"list-category", "categories", "list"},
	}

	for _, tt := range tests {
		if got := shortName(tt.full, tt.group); got != tt.want {
			t.Errorf("shortName(%q, %q) = %q, want %q", tt.full, tt.group, got, tt.want)
		}
	}
}

func TestDotnetAssemblyTagIsNotAGroup(t *testing.T) {
	// What Microsoft.AspNetCore.OpenApi emits for an endpoint declared without
	// WithTags: the assembly name, which is also the start of the title.
	spec := `{"openapi":"3.0.1","info":{"title":"OrdersApi | v1","version":"1.0.0"},"paths":{
		"/healthz":{"get":{"tags":["OrdersApi"],"responses":{"200":{"description":"OK"}}}},
		"/api/customers/{customerId}/orders":{"get":{"tags":["OrdersApi"],"parameters":[
			{"name":"customerId","in":"path","required":true,"schema":{"type":"string"}}],
			"responses":{"200":{"description":"OK"}}}},
		"/api/orders":{"get":{"tags":["Orders"],"operationId":"listOrders","responses":{"200":{"description":"OK"}}}}}}`

	api, err := Parse([]byte(spec))
	if err != nil {
		t.Fatal(err)
	}

	got := strings.Join(api.Groups(), ",")
	if got != "customers,healthz,orders" {
		t.Errorf("groups = %q, want the assembly tag ignored and paths used instead", got)
	}
	if op := api.Find("orders-healthz-get"); op != nil {
		t.Error("the assembly tag was used as a group")
	}
}

func TestARealTagIsStillHonouredWhenItMatchesNothing(t *testing.T) {
	spec := `{"openapi":"3.0.1","info":{"title":"Billing | v1","version":"1"},"paths":{
		"/api/invoices":{"get":{"tags":["Invoices"],"operationId":"listInvoices","responses":{"200":{"description":"OK"}}}}}}`

	api, err := Parse([]byte(spec))
	if err != nil {
		t.Fatal(err)
	}
	if got := api.Operations[0].Group; got != "invoices" {
		t.Errorf("group = %q, want invoices", got)
	}
}

func TestNullEnumMemberIsDropped(t *testing.T) {
	// .NET emits this for a nullable enum query parameter.
	spec := `{"openapi":"3.0.1","info":{"title":"x","version":"1"},"paths":{
		"/api/orders":{"get":{"tags":["Orders"],"operationId":"listOrders","parameters":[
			{"name":"status","in":"query","schema":{"enum":["Open","Shipped",null]}}],
			"responses":{"200":{"description":"OK"}}}}}}`

	api, err := Parse([]byte(spec))
	if err != nil {
		t.Fatal(err)
	}

	status := api.Operations[0].ParamsIn(InQuery)[0]
	if got := strings.Join(status.Enum, ","); got != "Open,Shipped" {
		t.Errorf("enum = %q, want the null member dropped", got)
	}
	if status.Type != TypeString {
		t.Errorf("type = %q, want an enum with no type to bind as a string", status.Type)
	}
}

// The .NET 10 fixture is a document a real Microsoft.AspNetCore.OpenApi served,
// captured verbatim. It is OpenAPI 3.1, where a schema type is a union.
func TestDotnet10OpenAPI31(t *testing.T) {
	api := load(t, "dotnet10-minimal.json")

	list := find(t, api, "orders", "list")
	byName := map[string]Param{}
	for _, p := range list.ParamsIn(InQuery) {
		byName[p.Name] = p
	}

	// .NET 10 emits ["integer","string"], so that a query value may arrive
	// string-encoded. A flag still has to bind as an integer.
	if got := byName["page"].Type; got != TypeInteger {
		t.Errorf("page type = %q, want integer from the union", got)
	}
	if !byName["page"].Required {
		t.Error("page is not required, but the spec says it is")
	}
	if got := byName["tag"].Type; got != TypeArray {
		t.Errorf("tag type = %q, want array", got)
	}
	if got := strings.Join(byName["status"].Enum, ","); got != "Open,Shipped,Cancelled" {
		t.Errorf("status enum = %q, want the null member dropped", got)
	}
}

func TestDotnet10GroupsAndNames(t *testing.T) {
	api := load(t, "dotnet10-minimal.json")

	// The assembly tag must not become a group, and the endpoints declared
	// without WithName must get derived names.
	tests := []struct{ group, name string }{
		{"orders", "list"},
		{"orders", "create"},
		{"orders", "get"},
		{"orders", "cancel"},
		{"orders", "bulk-create"},
		{"orders", "delete-by-id"},
		{"orders", "lines-get-by-id-by-line-id"},
		{"customers", "orders-get-by-customer-id"},
		{"healthz", "get"},
		{"diagnostics", "boom"},
	}

	for _, tt := range tests {
		t.Run(tt.group+" "+tt.name, func(t *testing.T) {
			find(t, api, tt.group, tt.name)
		})
	}
}

func TestDotnet10BodyFlatness(t *testing.T) {
	api := load(t, "dotnet10-minimal.json")

	create := find(t, api, "orders", "create")
	if create.Body == nil || !create.Body.Flat {
		t.Fatalf("create body = %+v, want a flat body despite the 3.1 type unions", create.Body)
	}
	byName := map[string]Field{}
	for _, f := range create.Body.Fields {
		byName[f.Name] = f
	}
	if byName["quantity"].Type != TypeInteger {
		t.Errorf("quantity type = %q, want integer", byName["quantity"].Type)
	}
	if byName["note"].Type != TypeString {
		t.Errorf("note type = %q, want string from [null,string]", byName["note"].Type)
	}

	bulk := find(t, api, "orders", "bulk-create")
	if bulk.Body == nil || bulk.Body.Flat {
		t.Errorf("bulk body = %+v, want it recognised as nested", bulk.Body)
	}
}

func TestSchemaTypeReadsUnions(t *testing.T) {
	tests := []struct {
		name  string
		types []string
		want  string
	}{
		{"single", []string{"string"}, TypeString},
		{"nullable string", []string{"null", "string"}, TypeString},
		{"string-encoded integer", []string{"integer", "string"}, TypeInteger},
		{"string-encoded number", []string{"number", "string"}, TypeNumber},
		{"nullable integer", []string{"null", "integer"}, TypeInteger},
		{"nullable array", []string{"array", "null"}, TypeArray},
		{"nullable object", []string{"object", "null"}, TypeObject},
		{"null alone", []string{"null"}, TypeString},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			types := openapi3.Types(tt.types)
			if got := schemaType(&openapi3.Schema{Type: &types}); got != tt.want {
				t.Errorf("schemaType(%v) = %q, want %q", tt.types, got, tt.want)
			}
		})
	}
}

func TestSchemaTypeWithNoType(t *testing.T) {
	if got := schemaType(&openapi3.Schema{}); got != TypeString {
		t.Errorf("schemaType = %q, want string when the spec states no type", got)
	}
	if got := schemaType(nil); got != TypeString {
		t.Errorf("schemaType(nil) = %q, want string", got)
	}
}

func TestNumberingNeverHandsOutTheSameNameTwice(t *testing.T) {
	// An operationId that kebabs to an already-numbered name used to collide with
	// the number a derived name had been given.
	spec := `{"openapi":"3.0.1","info":{"title":"x","version":"1"},"paths":{
		"/api/things":{"get":{"tags":["things"],"responses":{"200":{"description":"OK"}}}},
		"/things":{"get":{"tags":["things"],"responses":{"200":{"description":"OK"}}}},
		"/zzz/things":{"get":{"tags":["things"],"operationId":"things_get_2","responses":{"200":{"description":"OK"}}}}}}`

	api, err := Parse([]byte(spec))
	if err != nil {
		t.Fatal(err)
	}

	seen := map[string]string{}
	for _, op := range api.Operations {
		key := op.Group + " " + op.Name
		if first, dup := seen[key]; dup {
			t.Errorf("%q is shared by %s and %s %s", key, first, op.Method, op.Path)
		}
		seen[key] = op.Method + " " + op.Path
	}
	if len(seen) != 3 {
		t.Errorf("distinct names = %d, want 3: %v", len(seen), seen)
	}
}
