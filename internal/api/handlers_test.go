package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/0prodigy/OpenE2B/internal/db"
)

func setupTestHandler() *Handler {
	// Use in-memory store for tests
	memStore := db.NewMemoryStore()
	store := NewStore(memStore)
	config := &Config{
		Domain:   "test.e2b.local",
		APIPort:  3000,
		EnvdPort: 49983,
	}

	// Seed a template
	req := TemplateBuildRequestV3{
		Alias: "test-template",
	}
	cpuCount := 1
	memoryMB := 512
	req.CPUCount = &cpuCount
	req.MemoryMB = &memoryMB
	store.CreateTemplate(req)

	return NewHandler(store, config)
}

func TestHealth(t *testing.T) {
	handler := setupTestHandler()

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()

	handler.Health(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}
}

func TestListSandboxes_Empty(t *testing.T) {
	handler := setupTestHandler()

	req := httptest.NewRequest("GET", "/sandboxes", nil)
	w := httptest.NewRecorder()

	handler.ListSandboxes(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var sandboxes []ListedSandbox
	json.NewDecoder(w.Body).Decode(&sandboxes)

	if len(sandboxes) != 0 {
		t.Errorf("Expected empty list, got %d items", len(sandboxes))
	}
}

func TestCreateSandbox(t *testing.T) {
	handler := setupTestHandler()

	body := `{"templateID": "test-template"}`
	req := httptest.NewRequest("POST", "/sandboxes", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.CreateSandbox(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("Expected status 201, got %d: %s", w.Code, w.Body.String())
	}

	var sandbox Sandbox
	if err := json.NewDecoder(w.Body).Decode(&sandbox); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if sandbox.TemplateID == "" {
		t.Error("Expected non-empty templateID")
	}

	if sandbox.SandboxID == "" {
		t.Error("Expected non-empty sandboxID")
	}

	if sandbox.EnvdVersion == "" {
		t.Error("Expected non-empty envdVersion")
	}
}

func TestCreateSandbox_MissingTemplate(t *testing.T) {
	handler := setupTestHandler()

	body := `{"templateID": "nonexistent"}`
	req := httptest.NewRequest("POST", "/sandboxes", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.CreateSandbox(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected status 404, got %d", w.Code)
	}
}

func TestGetSandbox(t *testing.T) {
	handler := setupTestHandler()

	// Create a sandbox first
	createBody := `{"templateID": "test-template"}`
	createReq := httptest.NewRequest("POST", "/sandboxes", bytes.NewBufferString(createBody))
	createReq.Header.Set("Content-Type", "application/json")
	createW := httptest.NewRecorder()
	handler.CreateSandbox(createW, createReq)

	var sandbox Sandbox
	json.NewDecoder(createW.Body).Decode(&sandbox)

	// Get the sandbox
	getReq := httptest.NewRequest("GET", "/sandboxes/"+sandbox.SandboxID, nil)
	getW := httptest.NewRecorder()
	handler.GetSandbox(getW, getReq)

	if getW.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", getW.Code)
	}

	var detail SandboxDetail
	json.NewDecoder(getW.Body).Decode(&detail)

	if detail.SandboxID != sandbox.SandboxID {
		t.Errorf("Expected sandboxID '%s', got '%s'", sandbox.SandboxID, detail.SandboxID)
	}

	if detail.State != SandboxStateRunning {
		t.Errorf("Expected state 'running', got '%s'", detail.State)
	}
}

func TestListTemplates(t *testing.T) {
	handler := setupTestHandler()

	req := httptest.NewRequest("GET", "/templates", nil)
	w := httptest.NewRecorder()

	handler.ListTemplates(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var templates []Template
	json.NewDecoder(w.Body).Decode(&templates)

	if len(templates) != 1 {
		t.Errorf("Expected 1 template, got %d", len(templates))
	}

	if templates[0].Aliases[0] != "test-template" {
		t.Errorf("Expected alias 'test-template', got '%s'", templates[0].Aliases[0])
	}
}

func TestCreateTemplateV3(t *testing.T) {
	handler := setupTestHandler()

	body := `{"alias": "new-template", "cpuCount": 2, "memoryMB": 1024}`
	req := httptest.NewRequest("POST", "/v3/templates", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.CreateTemplateV3(w, req)

	if w.Code != http.StatusAccepted {
		t.Errorf("Expected status 202, got %d: %s", w.Code, w.Body.String())
	}

	var resp TemplateRequestResponseV3
	json.NewDecoder(w.Body).Decode(&resp)

	if resp.TemplateID == "" {
		t.Error("Expected non-empty templateID")
	}

	if resp.Aliases[0] != "new-template" {
		t.Errorf("Expected alias 'new-template', got '%s'", resp.Aliases[0])
	}
}

func TestGetBuildStatus(t *testing.T) {
	handler := setupTestHandler()

	// Get template to find build ID
	templates := handler.store.ListTemplates(nil)
	if len(templates) == 0 {
		t.Fatal("Expected at least one template")
	}

	template := templates[0]
	buildID := template.BuildID

	// Get build status
	req := httptest.NewRequest("GET", "/templates/"+template.TemplateID+"/builds/"+buildID+"/status", nil)
	w := httptest.NewRecorder()
	handler.GetBuildStatus(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var buildInfo TemplateBuildInfo
	json.NewDecoder(w.Body).Decode(&buildInfo)

	if buildInfo.TemplateID != template.TemplateID {
		t.Errorf("Expected templateID '%s', got '%s'", template.TemplateID, buildInfo.TemplateID)
	}

	if buildInfo.BuildID != buildID {
		t.Errorf("Expected buildID '%s', got '%s'", buildID, buildInfo.BuildID)
	}

	if buildInfo.Status != TemplateBuildStatusWaiting {
		t.Errorf("Expected status 'waiting', got '%s'", buildInfo.Status)
	}
}

func TestStartBuildV2(t *testing.T) {
	handler := setupTestHandler()

	// Create a new template with a fresh build in waiting state
	createBody := `{"alias": "v2-test-template", "cpuCount": 1, "memoryMB": 512}`
	createReq := httptest.NewRequest("POST", "/v3/templates", bytes.NewBufferString(createBody))
	createReq.Header.Set("Content-Type", "application/json")
	createW := httptest.NewRecorder()
	handler.CreateTemplateV3(createW, createReq)

	var createResp TemplateRequestResponseV3
	json.NewDecoder(createW.Body).Decode(&createResp)

	// Start build with v2 endpoint
	body := `{"fromImage": "ubuntu:22.04", "startCmd": "/bin/bash", "steps": [{"name": "install", "command": "apt-get update"}]}`
	req := httptest.NewRequest("POST", "/v2/templates/"+createResp.TemplateID+"/builds/"+createResp.BuildID, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.StartBuildV2(w, req)

	if w.Code != http.StatusAccepted {
		t.Errorf("Expected status 202, got %d: %s", w.Code, w.Body.String())
	}

	// Verify build status changed to building
	build, ok := handler.store.GetBuild(createResp.BuildID)
	if !ok {
		t.Fatal("Build not found")
	}

	if build.Status != TemplateBuildStatusBuilding {
		t.Errorf("Expected status 'building', got '%s'", build.Status)
	}
}
