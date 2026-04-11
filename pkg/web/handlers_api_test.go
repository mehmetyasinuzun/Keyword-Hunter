package web

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"

	"keywordhunter-mvp/pkg/storage"
	"keywordhunter-mvp/pkg/tagging"
)

type mockTagEngine struct {
	result *tagging.AutoTagResult
	err    error
}

func (m mockTagEngine) TagResultByID(ctx context.Context, resultID int64) (*tagging.AutoTagResult, error) {
	return m.result, m.err
}

type mockBatchRunner struct {
	job       *storage.TaggingJob
	err       error
	cancelErr error
}

func (m mockBatchRunner) Submit(ctx context.Context, resultIDs []int64, query string) (*storage.TaggingJob, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.job != nil {
		return m.job, nil
	}
	return &storage.TaggingJob{ID: "job-1", Status: tagging.StatusPending, TotalCount: len(resultIDs)}, nil
}

func (m mockBatchRunner) Cancel(jobID string) error {
	return m.cancelErr
}

func (m mockBatchRunner) RecoverPendingJobs() error {
	return nil
}

func setupAPIRouter(s *Server) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/api/auto-tag", s.handleAutoTag)
	r.POST("/api/batch-auto-tag", s.handleBatchAutoTag)
	r.GET("/api/batch-auto-tag/:id", s.handleBatchAutoTagStatus)
	r.POST("/api/batch-auto-tag/:id/cancel", s.handleBatchAutoTagCancel)
	r.GET("/api/graph/queries", s.handleGraphQueriesAPI)
	r.GET("/api/graph/results", s.handleGraphResultsAPI)
	return r
}

func newWebTestDB(t *testing.T) *storage.DB {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "web_api_test.db")
	db, err := storage.New(dbPath)
	if err != nil {
		t.Fatalf("test db create error: %v", err)
	}

	t.Cleanup(func() {
		_ = db.Close()
	})

	return db
}

func performJSONRequest(r *gin.Engine, method, path string, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestHandleAutoTag_StatusMapping(t *testing.T) {
	tests := []struct {
		name       string
		engine     mockTagEngine
		body       string
		wantStatus int
	}{
		{
			name:       "bad request body",
			engine:     mockTagEngine{},
			body:       `{}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "result not found",
			engine: mockTagEngine{
				err: tagging.ErrResultNotFound,
			},
			body:       `{"id":1}`,
			wantStatus: http.StatusNotFound,
		},
		{
			name: "no taggable signal",
			engine: mockTagEngine{
				err: tagging.ErrNoTaggableSignal,
			},
			body:       `{"id":1}`,
			wantStatus: http.StatusUnprocessableEntity,
		},
		{
			name: "internal error",
			engine: mockTagEngine{
				err: errors.New("db boom"),
			},
			body:       `{"id":1}`,
			wantStatus: http.StatusInternalServerError,
		},
		{
			name: "success",
			engine: mockTagEngine{
				result: &tagging.AutoTagResult{
					ResultID:    10,
					Tags:        []string{"leak", "dump"},
					TagsStr:     "leak, dump",
					KeywordHits: 5,
					Category:    "Veri Sızıntısı",
					Criticality: 5,
					Confidence:  88,
				},
			},
			body:       `{"id":10}`,
			wantStatus: http.StatusOK,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := &Server{tagEngine: tc.engine, batchRunner: mockBatchRunner{}}
			r := setupAPIRouter(s)
			w := performJSONRequest(r, http.MethodPost, "/api/auto-tag", tc.body)

			if w.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d, body=%s", w.Code, tc.wantStatus, w.Body.String())
			}

			if tc.wantStatus == http.StatusOK {
				var resp map[string]interface{}
				if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
					t.Fatalf("response parse error: %v", err)
				}
				if ok, _ := resp["success"].(bool); !ok {
					t.Fatalf("expected success=true, got body=%s", w.Body.String())
				}
				if resp["category"] == nil {
					t.Fatalf("expected category in response, got body=%s", w.Body.String())
				}
				if resp["criticality"] == nil {
					t.Fatalf("expected criticality in response, got body=%s", w.Body.String())
				}
			}
		})
	}
}

func TestHandleBatchAutoTag_StatusMapping(t *testing.T) {
	tests := []struct {
		name       string
		runner     mockBatchRunner
		body       string
		wantStatus int
	}{
		{
			name:       "bad request body",
			runner:     mockBatchRunner{},
			body:       `{}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "empty ids",
			runner:     mockBatchRunner{},
			body:       `{"resultIds":[],"query":"x"}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "no valid result IDs",
			runner: mockBatchRunner{
				err: tagging.ErrNoValidResultIDs,
			},
			body:       `{"resultIds":[1,2],"query":"x"}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "internal error",
			runner: mockBatchRunner{
				err: errors.New("queue failure"),
			},
			body:       `{"resultIds":[1,2],"query":"x"}`,
			wantStatus: http.StatusInternalServerError,
		},
		{
			name: "success",
			runner: mockBatchRunner{
				job: &storage.TaggingJob{ID: "job-123", Status: tagging.StatusPending, TotalCount: 2},
			},
			body:       `{"resultIds":[1,2],"query":"x"}`,
			wantStatus: http.StatusOK,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := &Server{tagEngine: mockTagEngine{}, batchRunner: tc.runner}
			r := setupAPIRouter(s)
			w := performJSONRequest(r, http.MethodPost, "/api/batch-auto-tag", tc.body)

			if w.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d, body=%s", w.Code, tc.wantStatus, w.Body.String())
			}
		})
	}
}

func TestHandleBatchAutoTagStatus_StatusMapping(t *testing.T) {
	db := newWebTestDB(t)
	if err := db.CreateTaggingJob(storage.TaggingJob{
		ID:            "job-existing",
		Query:         "seed",
		TotalCount:    2,
		Status:        tagging.StatusPending,
		ResultIDsJSON: "[1,2]",
	}); err != nil {
		t.Fatalf("seed job create error: %v", err)
	}

	s := &Server{db: db, tagEngine: mockTagEngine{}, batchRunner: mockBatchRunner{}}
	r := setupAPIRouter(s)

	w := performJSONRequest(r, http.MethodGet, "/api/batch-auto-tag/job-existing", "")
	if w.Code != http.StatusOK {
		t.Fatalf("existing job status = %d, want %d, body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	w = performJSONRequest(r, http.MethodGet, "/api/batch-auto-tag/job-missing", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("missing job status = %d, want %d, body=%s", w.Code, http.StatusNotFound, w.Body.String())
	}
}

func TestHandleBatchAutoTagCancel_StatusMapping(t *testing.T) {
	t.Run("not found", func(t *testing.T) {
		s := &Server{tagEngine: mockTagEngine{}, batchRunner: mockBatchRunner{cancelErr: sql.ErrNoRows}}
		r := setupAPIRouter(s)

		w := performJSONRequest(r, http.MethodPost, "/api/batch-auto-tag/job-missing/cancel", "{}")
		if w.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want %d, body=%s", w.Code, http.StatusNotFound, w.Body.String())
		}
	})

	t.Run("success", func(t *testing.T) {
		db := newWebTestDB(t)
		if err := db.CreateTaggingJob(storage.TaggingJob{
			ID:            "job-cancel",
			Query:         "seed",
			TotalCount:    1,
			Status:        tagging.StatusPending,
			ResultIDsJSON: "[1]",
		}); err != nil {
			t.Fatalf("seed job create error: %v", err)
		}

		s := &Server{db: db, tagEngine: mockTagEngine{}, batchRunner: mockBatchRunner{}}
		r := setupAPIRouter(s)

		w := performJSONRequest(r, http.MethodPost, "/api/batch-auto-tag/job-cancel/cancel", "{}")
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d, body=%s", w.Code, http.StatusOK, w.Body.String())
		}

		job, err := db.GetTaggingJob("job-cancel")
		if err != nil {
			t.Fatalf("job fetch error: %v", err)
		}
		if job.Status != tagging.StatusCancelled {
			t.Fatalf("job status = %s, want %s", job.Status, tagging.StatusCancelled)
		}
	})
}

func TestParseGraphLimit(t *testing.T) {
	tests := []struct {
		name       string
		raw        string
		maxAllowed int
		want       int
		wantErr    bool
	}{
		{
			name:       "valid value",
			raw:        "120",
			maxAllowed: 500,
			want:       120,
			wantErr:    false,
		},
		{
			name:       "clamped to max",
			raw:        "1200",
			maxAllowed: 500,
			want:       500,
			wantErr:    false,
		},
		{
			name:       "zero is allowed",
			raw:        "0",
			maxAllowed: 500,
			want:       0,
			wantErr:    false,
		},
		{
			name:       "negative rejected",
			raw:        "-5",
			maxAllowed: 500,
			wantErr:    true,
		},
		{
			name:       "non numeric rejected",
			raw:        "abc",
			maxAllowed: 500,
			wantErr:    true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseGraphLimit(tc.raw, tc.maxAllowed)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if got != tc.want {
				t.Fatalf("limit = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestHandleGraphQueriesAPI_Filter(t *testing.T) {
	db := newWebTestDB(t)
	_, err := db.SaveResults([]storage.SearchResult{
		{Title: "Alpha", URL: "http://alpha.onion", Source: "Torch", Query: "leak"},
		{Title: "Beta", URL: "http://beta.onion", Source: "Ahmia", Query: "leak"},
		{Title: "Gamma", URL: "http://gamma.onion", Source: "Torch", Query: "market"},
	})
	if err != nil {
		t.Fatalf("seed results error: %v", err)
	}

	s := &Server{db: db}
	r := setupAPIRouter(s)

	w := performJSONRequest(r, http.MethodGet, "/api/graph/queries?limit=10&q=leak", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp struct {
		Queries []struct {
			Query string `json:"query"`
			Count int    `json:"count"`
		} `json:"queries"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response parse error: %v", err)
	}

	if len(resp.Queries) != 1 {
		t.Fatalf("queries len = %d, want 1", len(resp.Queries))
	}
	if resp.Queries[0].Query != "leak" {
		t.Fatalf("query = %q, want %q", resp.Queries[0].Query, "leak")
	}
	if resp.Queries[0].Count != 2 {
		t.Fatalf("count = %d, want 2", resp.Queries[0].Count)
	}
}

func TestHandleGraphResultsAPI_PaginationAndFields(t *testing.T) {
	db := newWebTestDB(t)
	_, err := db.SaveResults([]storage.SearchResult{
		{Title: "Alpha", URL: "http://same.onion/page", Source: "Torch", Query: "hunt"},
		{Title: "Beta", URL: "http://other.onion", Source: "Torch", Query: "hunt"},
		{Title: "Alpha Mirror", URL: "http://same.onion/page", Source: "Ahmia", Query: "hunt"},
	})
	if err != nil {
		t.Fatalf("seed results error: %v", err)
	}

	if _, err := db.GetDBConn().Exec("UPDATE graph_nodes SET is_expanded = 1 WHERE url = ?", "http://same.onion/page"); err != nil {
		t.Fatalf("update graph_nodes error: %v", err)
	}

	s := &Server{db: db}
	r := setupAPIRouter(s)

	w := performJSONRequest(r, http.MethodGet, "/api/graph/results?q=hunt&engine=Torch&limit=1&offset=0", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	var page1 struct {
		Query      string `json:"query"`
		Engine     string `json:"engine"`
		Offset     int    `json:"offset"`
		Limit      int    `json:"limit"`
		NextOffset int    `json:"nextOffset"`
		Results    []struct {
			ID          int64  `json:"id"`
			Title       string `json:"title"`
			URL         string `json:"url"`
			SourceCount int    `json:"sourceCount"`
			IsExpanded  bool   `json:"isExpanded"`
			Domain      string `json:"domain"`
		} `json:"results"`
	}

	if err := json.Unmarshal(w.Body.Bytes(), &page1); err != nil {
		t.Fatalf("response parse error: %v", err)
	}

	if page1.Query != "hunt" || page1.Engine != "Torch" {
		t.Fatalf("unexpected query/engine: %s/%s", page1.Query, page1.Engine)
	}
	if page1.Offset != 0 || page1.Limit != 1 {
		t.Fatalf("unexpected page info: offset=%d limit=%d", page1.Offset, page1.Limit)
	}
	if len(page1.Results) != 1 {
		t.Fatalf("results len = %d, want 1", len(page1.Results))
	}
	if page1.NextOffset != 1 {
		t.Fatalf("nextOffset = %d, want 1", page1.NextOffset)
	}

	first := page1.Results[0]
	if first.SourceCount < 1 {
		t.Fatalf("sourceCount = %d, want >= 1", first.SourceCount)
	}
	if first.URL == "http://same.onion/page" {
		if !first.IsExpanded {
			t.Fatalf("expected expanded result for same.onion")
		}
		if first.SourceCount != 2 {
			t.Fatalf("sourceCount = %d, want 2", first.SourceCount)
		}
	}
	if first.Domain == "" {
		t.Fatalf("domain should not be empty")
	}

	w2 := performJSONRequest(r, http.MethodGet, "/api/graph/results?q=hunt&engine=Torch&limit=1&offset=1", "")
	if w2.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", w2.Code, http.StatusOK, w2.Body.String())
	}

	var page2 struct {
		NextOffset int `json:"nextOffset"`
		Results    []struct {
			URL string `json:"url"`
		} `json:"results"`
	}
	if err := json.Unmarshal(w2.Body.Bytes(), &page2); err != nil {
		t.Fatalf("response parse error: %v", err)
	}
	if len(page2.Results) != 1 {
		t.Fatalf("results len = %d, want 1", len(page2.Results))
	}
	if page2.NextOffset != 0 {
		t.Fatalf("nextOffset = %d, want 0", page2.NextOffset)
	}
	if page2.Results[0].URL == first.URL {
		t.Fatalf("pagination should return a different row in page2")
	}
}
