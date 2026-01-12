package inventory

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"
)

const violaOpenAPISpec = `openapi: 3.0.3
info:
  title: Multinic Operator API
  version: "1.0"
  description: |
    Operator가 Viola API로 전송하는 페이로드와 Interfaces 조회 API 문서입니다.
servers:
  - url: /
paths:
  /v1/k8s/multinic/node-configs:
    post:
      tags: ["viola"]
      summary: MultiNicNodeConfig 목록 적용
      description: |
        Operator가 노드별 인터페이스 목록을 전송하면 Viola API가 MultiNicNodeConfig로 변환/적용합니다.
      parameters:
        - name: x-provider-id
          in: header
          required: false
          schema:
            type: string
          description: Viola 라우팅용 provider 식별자 (선택)
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: array
              items:
                $ref: "#/components/schemas/NodeConfig"
      responses:
        "200":
          description: 적용 완료
        "400":
          description: 요청 오류
        "500":
          description: kubectl apply 실패
  /v1/interfaces/catalog:
    get:
      tags: ["interfaces"]
      summary: 조회 가능한 필터 목록
      description: |
        providerId/nodeName/instanceId 목록을 반환합니다.
      responses:
        "200":
          description: 조회 성공
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/InterfaceCatalog"
        "503":
          description: inventory 저장소 비활성
  /v1/interfaces/node-configs:
    get:
      tags: ["interfaces"]
      summary: 노드별 인터페이스 목록 조회
      parameters:
        - name: providerId
          in: query
          required: false
          schema:
            type: string
          description: provider 필터 (권장)
        - name: nodeName
          in: query
          required: false
          schema:
            type: string
          description: 노드명 필터
        - name: instanceId
          in: query
          required: false
          schema:
            type: string
          description: VM ID 필터
      responses:
        "200":
          description: 조회 성공
          content:
            application/json:
              schema:
                type: array
                items:
                  $ref: "#/components/schemas/InventoryRecord"
        "503":
          description: inventory 저장소 비활성
  /v1/interfaces/node-configs/{nodeName}:
    get:
      tags: ["interfaces"]
      summary: 노드별 인터페이스 단건 조회
      parameters:
        - name: nodeName
          in: path
          required: true
          schema:
            type: string
        - name: providerId
          in: query
          required: false
          schema:
            type: string
          description: provider 필터 (권장)
      responses:
        "200":
          description: 조회 성공
          content:
            application/json:
              schema:
                type: array
                items:
                  $ref: "#/components/schemas/InventoryRecord"
        "404":
          description: not found
        "503":
          description: inventory 저장소 비활성
components:
  schemas:
    NodeConfig:
      type: object
      required:
        - nodeName
        - instanceId
        - interfaces
      properties:
        nodeName:
          type: string
        instanceId:
          type: string
        interfaces:
          type: array
          items:
            $ref: "#/components/schemas/NodeInterface"
    NodeInterface:
      type: object
      required:
        - id
        - name
        - macAddress
        - address
        - cidr
        - mtu
      properties:
        id:
          type: integer
          minimum: 0
          maximum: 9
        name:
          type: string
          example: multinic0
        macAddress:
          type: string
        address:
          type: string
        cidr:
          type: string
        mtu:
          type: integer
    InventoryRecord:
      type: object
      properties:
        providerId:
          type: string
        nodeName:
          type: string
        instanceId:
          type: string
        config:
          $ref: "#/components/schemas/NodeConfig"
        lastConfigHash:
          type: string
        updatedAt:
          type: string
          format: date-time
    InterfaceCatalog:
      type: object
      properties:
        providerIds:
          type: array
          items:
            type: string
        nodeNames:
          type: array
          items:
            type: string
        instanceIds:
          type: array
          items:
            type: string
        nodes:
          type: array
          items:
            $ref: "#/components/schemas/InterfaceNodeSummary"
    InterfaceNodeSummary:
      type: object
      properties:
        providerId:
          type: string
        nodeName:
          type: string
        instanceId:
          type: string
        interfaceCount:
          type: integer
        updatedAt:
          type: string
          format: date-time
`

const swaggerHTML = `<!doctype html>
<html lang="ko">
  <head>
    <meta charset="utf-8" />
    <title>Operator Payload Docs</title>
    <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css" />
  </head>
  <body>
    <div id="swagger-ui"></div>
    <script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
    <script>
      window.onload = function () {
        SwaggerUIBundle({
          url: '/openapi.yaml',
          dom_id: '#swagger-ui',
          presets: [SwaggerUIBundle.presets.apis],
          layout: 'BaseLayout'
        });
      };
    </script>
  </body>
</html>`

type Server struct {
	addr  string
	store *Store
}

type catalogResponse struct {
	ProviderIDs []string            `json:"providerIds"`
	NodeNames   []string            `json:"nodeNames"`
	InstanceIDs []string            `json:"instanceIds"`
	Nodes       []catalogNodeRecord `json:"nodes"`
}

type catalogNodeRecord struct {
	ProviderID     string    `json:"providerId"`
	NodeName       string    `json:"nodeName"`
	InstanceID     string    `json:"instanceId"`
	InterfaceCount int       `json:"interfaceCount"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

func NewServer(addr string, store *Store) *Server {
	return &Server{addr: addr, store: store}
}

func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/openapi.yaml", s.handleOpenAPI)
	mux.HandleFunc("/docs", s.handleDocs)
	mux.HandleFunc("/v1/interfaces/catalog", s.handleCatalog)
	mux.HandleFunc("/v1/interfaces/node-configs", s.handleList)
	mux.HandleFunc("/v1/interfaces/node-configs/", s.handleGetByName)
	mux.HandleFunc("/v1/inventory/node-configs", s.handleList)
	mux.HandleFunc("/v1/inventory/node-configs/", s.handleGetByName)

	srv := &http.Server{
		Addr:              s.addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	return srv.ListenAndServe()
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) handleOpenAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/yaml")
	_, _ = w.Write([]byte(violaOpenAPISpec))
}

func (s *Server) handleDocs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(swaggerHTML))
}

func (s *Server) handleCatalog(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		http.Error(w, "inventory store not available", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	records, err := s.store.List(r.Context(), "", "", "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	resp := buildCatalog(records)
	writeJSON(w, resp)
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		http.Error(w, "inventory store not available", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	providerID := r.URL.Query().Get("providerId")
	nodeName := r.URL.Query().Get("nodeName")
	instanceID := r.URL.Query().Get("instanceId")

	records, err := s.store.List(r.Context(), providerID, nodeName, instanceID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, records)
}

func (s *Server) handleGetByName(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		http.Error(w, "inventory store not available", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	nodeName := strings.TrimPrefix(r.URL.Path, "/v1/inventory/node-configs/")
	if nodeName == "" {
		http.Error(w, "nodeName required", http.StatusBadRequest)
		return
	}
	providerID := r.URL.Query().Get("providerId")
	records, err := s.store.List(r.Context(), providerID, nodeName, "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if len(records) == 0 {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	writeJSON(w, records)
}

func buildCatalog(records []Record) catalogResponse {
	providerSet := make(map[string]struct{})
	nodeSet := make(map[string]struct{})
	instanceSet := make(map[string]struct{})
	nodes := make([]catalogNodeRecord, 0, len(records))

	for _, rec := range records {
		if rec.ProviderID != "" {
			providerSet[rec.ProviderID] = struct{}{}
		}
		if rec.NodeName != "" {
			nodeSet[rec.NodeName] = struct{}{}
		}
		if rec.InstanceID != "" {
			instanceSet[rec.InstanceID] = struct{}{}
		}
		nodes = append(nodes, catalogNodeRecord{
			ProviderID:     rec.ProviderID,
			NodeName:       rec.NodeName,
			InstanceID:     rec.InstanceID,
			InterfaceCount: len(rec.Config.Interfaces),
			UpdatedAt:      rec.UpdatedAt,
		})
	}

	return catalogResponse{
		ProviderIDs: setToSortedList(providerSet),
		NodeNames:   setToSortedList(nodeSet),
		InstanceIDs: setToSortedList(instanceSet),
		Nodes:       nodes,
	}
}

func setToSortedList(in map[string]struct{}) []string {
	if len(in) == 0 {
		return []string{}
	}
	out := make([]string, 0, len(in))
	for key := range in {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	_ = enc.Encode(v)
}
