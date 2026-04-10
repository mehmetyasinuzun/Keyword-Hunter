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
