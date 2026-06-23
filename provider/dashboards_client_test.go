package provider

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

func TestDashboardsRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/dash/api/workspaces" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get(dashboardsXsrfHeader) != "true" {
			t.Fatalf("missing %s header", dashboardsXsrfHeader)
		}
		username, password, ok := r.BasicAuth()
		if !ok || username != "admin" || password != "secret" {
			t.Fatalf("unexpected basic auth: %s/%s", username, password)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"result":{"id":"workspace-1"}}`))
	}))
	defer server.Close()

	dashboardsURL, err := url.Parse(server.URL + "/dash")
	if err != nil {
		t.Fatal(err)
	}
	conf := &ProviderConf{
		dashboardsUrl:       dashboardsURL.String(),
		parsedDashboardsUrl: dashboardsURL,
		username:            "admin",
		password:            "secret",
	}

	var response dashboardWorkspaceCreateResponse
	if err := dashboardsRequest(conf, http.MethodPost, "/api/workspaces", map[string]string{"name": "logs"}, &response); err != nil {
		t.Fatal(err)
	}
	if !response.Success || response.Result.ID != "workspace-1" {
		t.Fatalf("unexpected response: %+v", response)
	}
}

func TestDashboardsRequestRequiresDashboardsURL(t *testing.T) {
	err := dashboardsRequest(&ProviderConf{}, http.MethodGet, "/api/workspaces/test", nil, nil)
	if err == nil {
		t.Fatal("expected dashboards_url error")
	}
}

func TestDashboardsRequestReturnsNotFound(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()

	dashboardsURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	conf := &ProviderConf{
		dashboardsUrl:       dashboardsURL.String(),
		parsedDashboardsUrl: dashboardsURL,
	}

	err = dashboardsRequest(conf, http.MethodGet, "/api/workspaces/missing", nil, nil)
	if err != errDashboardsNotFound {
		t.Fatalf("expected not found error, got %v", err)
	}
}

func TestDashboardWorkspaceRequestBody(t *testing.T) {
	resource := resourceOpensearchDashboardWorkspace()
	data := schema.TestResourceDataRaw(t, resource.Schema, map[string]interface{}{
		"name":        "Logs",
		"description": "Production logs",
		"features":    []interface{}{"use-case-observability"},
		"permissions": `{"library_write":["admin"]}`,
	})

	body, err := dashboardWorkspaceRequestBody(data)
	if err != nil {
		t.Fatal(err)
	}

	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	expected := `{"attributes":{"name":"Logs","description":"Production logs","features":["use-case-observability"]},"permissions":{"library_write":["admin"]}}`
	if string(encoded) != expected {
		t.Fatalf("unexpected body: %s", string(encoded))
	}
}

func TestDashboardWorkspaceCreateTenantHeader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		assertHeader(t, r, SECURITY_TENANT_HEADER, "tenant-a")

		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/workspaces":
			_, _ = w.Write([]byte(`{"success":true,"result":{"id":"workspace-1"}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/workspaces/workspace-1":
			_, _ = w.Write([]byte(`{"success":true,"result":{"id":"workspace-1","name":"Logs","description":"Production logs","features":["use-case-observability"]}}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	dashboardsURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	conf := &ProviderConf{
		dashboardsUrl:       dashboardsURL.String(),
		parsedDashboardsUrl: dashboardsURL,
	}

	resource := resourceOpensearchDashboardWorkspace()
	data := schema.TestResourceDataRaw(t, resource.Schema, map[string]interface{}{
		"name":        "Logs",
		"description": "Production logs",
		"features":    []interface{}{"use-case-observability"},
		"tenant_name": "tenant-a",
	})

	if err := resourceOpensearchDashboardWorkspaceCreate(data, conf); err != nil {
		t.Fatal(err)
	}
}

func TestDashboardWorkspaceGetReturnsNotFoundFromAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":false,"error":"workspace not found"}`))
	}))
	defer server.Close()

	dashboardsURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	conf := &ProviderConf{
		dashboardsUrl:       dashboardsURL.String(),
		parsedDashboardsUrl: dashboardsURL,
	}

	_, err = getDashboardWorkspace("workspace-1", conf, nil)
	if err != errDashboardsNotFound {
		t.Fatalf("expected not found error, got %v", err)
	}
}

func TestDashboardWorkspaceObjectsRequestBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/workspaces/_associate" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		assertHeader(t, r, SECURITY_TENANT_HEADER, "tenant-a")

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		expected := `{"workspaceId":"workspace-1","savedObjects":[{"type":"index-pattern","id":"logs-*"}]}`
		if string(body) != expected {
			t.Fatalf("unexpected request body: %s", string(body))
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"result":[{"id":"logs-*"}]}`))
	}))
	defer server.Close()

	dashboardsURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	conf := &ProviderConf{
		dashboardsUrl:       dashboardsURL.String(),
		parsedDashboardsUrl: dashboardsURL,
	}

	resource := resourceOpensearchDashboardWorkspaceObjects()
	data := schema.TestResourceDataRaw(t, resource.Schema, map[string]interface{}{
		"workspace_id": "workspace-1",
		"tenant_name":  "tenant-a",
		"saved_object": []interface{}{
			map[string]interface{}{
				"type": "index-pattern",
				"id":   "logs-*",
			},
		},
	})

	if err := associateDashboardWorkspaceObjects(data, conf, "/api/workspaces/_associate"); err != nil {
		t.Fatal(err)
	}
}

func TestDashboardWorkspaceMappingUsesTenantIndex(t *testing.T) {
	expectedIndex, err := resourceOpensearchOpenDistroDashboardComputeIndex("tenant-a")
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		if r.URL.Path != "/"+expectedIndex+"/_mapping" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		assertHeader(t, r, SECURITY_TENANT_HEADER, "tenant-a")

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		expected := `{"properties":{"workspaces":{"type":"keyword"}}}`
		if string(body) != expected {
			t.Fatalf("unexpected request body: %s", string(body))
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"acknowledged":true}`))
	}))
	defer server.Close()

	opensearchURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	conf := &ProviderConf{
		rawUrl:         server.URL,
		parsedUrl:      opensearchURL,
		sniffing:       false,
		healthchecking: false,
		osVersion:      "2.19.0",
	}

	if err := ensureDashboardWorkspaceIndexMapping(conf, "tenant-a"); err != nil {
		t.Fatal(err)
	}
}

func TestDashboardDataSourceCreate(t *testing.T) {
	var metadataRequests int
	var createRequests int
	var readRequests int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/internal/data-source-management/fetchDataSourceMetaData":
			metadataRequests++
			assertHeader(t, r, SECURITY_TENANT_HEADER, "tenant-a")
			assertJSONBody(t, r, map[string]interface{}{
				"dataSourceAttr": map[string]interface{}{
					"endpoint": "https://remote.example.com",
					"auth": map[string]interface{}{
						"type":        dashboardDataSourceAuthNoAuth,
						"credentials": map[string]interface{}{},
					},
				},
			})
			_, _ = w.Write([]byte(`{"dataSourceVersion":"2.19.0","dataSourceEngineType":"OpenSearch","installedPlugins":["opensearch-security"]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/saved_objects/data-source":
			createRequests++
			assertHeader(t, r, SECURITY_TENANT_HEADER, "tenant-a")
			assertJSONBody(t, r, map[string]interface{}{
				"attributes": map[string]interface{}{
					"title":                "Remote cluster",
					"description":          "Remote OpenSearch cluster",
					"endpoint":             "https://remote.example.com",
					"auth":                 map[string]interface{}{"type": dashboardDataSourceAuthNoAuth, "credentials": map[string]interface{}{}},
					"dataSourceVersion":    "2.19.0",
					"dataSourceEngineType": "OpenSearch",
					"installedPlugins":     []interface{}{"opensearch-security"},
				},
			})
			_, _ = w.Write([]byte(`{"id":"data-source-1","type":"data-source","attributes":{"title":"Remote cluster","description":"Remote OpenSearch cluster","endpoint":"https://remote.example.com","auth":{"type":"no_auth"},"dataSourceVersion":"2.19.0","dataSourceEngineType":"OpenSearch","installedPlugins":["opensearch-security"]}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/saved_objects/data-source/data-source-1":
			readRequests++
			assertHeader(t, r, SECURITY_TENANT_HEADER, "tenant-a")
			_, _ = w.Write([]byte(`{"id":"data-source-1","type":"data-source","attributes":{"title":"Remote cluster","description":"Remote OpenSearch cluster","endpoint":"https://remote.example.com","auth":{"type":"no_auth"},"dataSourceVersion":"2.19.0","dataSourceEngineType":"OpenSearch","installedPlugins":["opensearch-security"]}}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	dashboardsURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	conf := &ProviderConf{
		dashboardsUrl:       dashboardsURL.String(),
		parsedDashboardsUrl: dashboardsURL,
	}

	resource := resourceOpensearchDashboardDataSource()
	data := schema.TestResourceDataRaw(t, resource.Schema, map[string]interface{}{
		"title":       "Remote cluster",
		"description": "Remote OpenSearch cluster",
		"endpoint":    "https://remote.example.com",
		"tenant_name": "tenant-a",
		"auth_type":   dashboardDataSourceAuthNoAuth,
	})

	if err := resourceOpensearchDashboardDataSourceCreate(data, conf); err != nil {
		t.Fatal(err)
	}
	if data.Id() != "data-source-1" {
		t.Fatalf("unexpected ID: %s", data.Id())
	}
	if metadataRequests != 1 || createRequests != 1 || readRequests != 1 {
		t.Fatalf("unexpected request counts: metadata=%d create=%d read=%d", metadataRequests, createRequests, readRequests)
	}
}

func TestDashboardDataSourceUpdateAttributesOmitEndpoint(t *testing.T) {
	resource := resourceOpensearchDashboardDataSource()
	data := schema.TestResourceDataRaw(t, resource.Schema, map[string]interface{}{
		"title":       "Remote cluster",
		"description": "Updated description",
		"endpoint":    "https://remote.example.com",
		"auth_type":   dashboardDataSourceAuthNoAuth,
	})

	attributes, err := dashboardDataSourceAttributes(data, false, false)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(attributes)
	if err != nil {
		t.Fatal(err)
	}
	expected := `{"title":"Remote cluster","description":"Updated description"}`
	if string(encoded) != expected {
		t.Fatalf("unexpected update attributes: %s", string(encoded))
	}
}

func assertHeader(t *testing.T, r *http.Request, key, expected string) {
	t.Helper()

	if actual := r.Header.Get(key); actual != expected {
		t.Fatalf("unexpected %s header: %s", key, actual)
	}
}

func assertJSONBody(t *testing.T, r *http.Request, expected map[string]interface{}) {
	t.Helper()

	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}

	var actual map[string]interface{}
	if err := json.Unmarshal(body, &actual); err != nil {
		t.Fatalf("invalid JSON body %q: %s", string(body), err)
	}

	expectedJSON, err := json.Marshal(expected)
	if err != nil {
		t.Fatal(err)
	}
	actualJSON, err := json.Marshal(actual)
	if err != nil {
		t.Fatal(err)
	}
	if string(actualJSON) != string(expectedJSON) {
		t.Fatalf("unexpected JSON body:\nexpected: %s\nactual:   %s", string(expectedJSON), string(actualJSON))
	}
}
