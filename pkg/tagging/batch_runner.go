package tagging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/google/uuid"

	"keywordhunter-mvp/pkg/logger"
	"keywordhunter-mvp/pkg/shared"
	"keywordhunter-mvp/pkg/storage"
)

const (
	StatusPending   = "pending"
	StatusRunning   = "running"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"
)

var ErrNoValidResultIDs = errors.New("geçerli result ID bulunamadı")

// BatchRunner toplu etiketleme işlerini worker kuyruğunda çalıştırır.
type BatchRunner struct {
	db          *storage.DB
	engine      *Engine
	queue       chan string
	workerCount int

	mu      sync.RWMutex
	cancels map[string]context.CancelFunc
}

// NewBatchRunner yeni iş kuyruğunu oluşturur ve worker'ları başlatır.
func NewBatchRunner(db *storage.DB, engine *Engine, workerCount int) *BatchRunner {
	if workerCount <= 0 {
		workerCount = 1
	}

	r := &BatchRunner{
		db:          db,
		engine:      engine,
		queue:       make(chan string, 64),
		workerCount: workerCount,
		cancels:     make(map[string]context.CancelFunc),
	}

	for i := 0; i < r.workerCount; i++ {
		go r.workerLoop(i + 1)
	}

	return r
}

// RecoverPendingJobs restart sonrası yarım kalan işleri tekrar kuyruğa alır.
func (r *BatchRunner) RecoverPendingJobs() error {
	if err := r.db.ResetRunningTaggingJobs(); err != nil {
		return err
	}

	jobs, err := r.db.ListTaggingJobsByStatuses([]string{StatusPending}, 100)
	if err != nil {
		return err
	}

	for _, job := range jobs {
		r.queue <- job.ID
	}

	if len(jobs) > 0 {
		logger.Info("TAG JOB RECOVERY: %d iş kuyruğa geri alındı", len(jobs))
	}

	return nil
}

// Submit yeni bir toplu etiketleme işi oluşturur.
func (r *BatchRunner) Submit(ctx context.Context, resultIDs []int64, query string) (*storage.TaggingJob, error) {
	_ = ctx

	ids := normalizeIDs(resultIDs)
	if len(ids) == 0 {
		return nil, fmt.Errorf("%w", ErrNoValidResultIDs)
	}

	existingSet, err := r.db.ExistingResultIDSet(ids)
	if err != nil {
		return nil, fmt.Errorf("kayıt doğrulama hatası: %w", err)
	}

	validatedIDs := make([]int64, 0, len(ids))
	for _, id := range ids {
		if existingSet[id] {
			validatedIDs = append(validatedIDs, id)
		}
	}

	if len(validatedIDs) == 0 {
		return nil, fmt.Errorf("%w", ErrNoValidResultIDs)
	}

	idsJSON, err := json.Marshal(validatedIDs)
	if err != nil {
		return nil, fmt.Errorf("iş yükü oluşturulamadı: %w", err)
	}

	job := storage.TaggingJob{
		ID:            uuid.NewString(),
		Query:         query,
		TotalCount:    len(validatedIDs),
		Status:        StatusPending,
		ResultIDsJSON: string(idsJSON),
	}

	if err := r.db.CreateTaggingJob(job); err != nil {
		return nil, fmt.Errorf("iş kaydedilemedi: %w", err)
	}

	select {
	case r.queue <- job.ID:
	default:
		go func() {
			r.queue <- job.ID
		}()
	}

	skipped := len(ids) - len(validatedIDs)
	if skipped > 0 {
		shared.Streamer.BroadcastLog("info", fmt.Sprintf("Toplu etiketleme kuyruğa alındı (%d kayıt, %d geçersiz ID atlandı)", len(validatedIDs), skipped), "")
	} else {
		shared.Streamer.BroadcastLog("info", fmt.Sprintf("Toplu etiketleme kuyruğa alındı (%d kayıt)", len(validatedIDs)), "")
	}

	return &job, nil
}

// Cancel bekleyen/çalışan işi iptal eder.
func (r *BatchRunner) Cancel(jobID string) error {
	job, err := r.db.GetTaggingJob(jobID)
	if err != nil {
		return err
	}

	switch job.Status {
	case StatusCompleted, StatusFailed, StatusCancelled:
		return nil
	case StatusPending:
		return r.db.MarkTaggingJobFinished(jobID, StatusCancelled, "Kullanıcı tarafından iptal edildi")
	}

	r.mu.RLock()
	cancelFn, exists := r.cancels[jobID]
	r.mu.RUnlock()
	if exists {
		cancelFn()
	}

	return nil
}

func (r *BatchRunner) workerLoop(workerID int) {
	for jobID := range r.queue {
		r.processJob(jobID, workerID)
	}
}

func (r *BatchRunner) processJob(jobID string, workerID int) {
	job, err := r.db.GetTaggingJob(jobID)
	if err != nil {
		logger.Warn("TAG JOB GET ERROR: %s - %v", jobID, err)
		return
	}

	if job.Status == StatusCancelled || job.Status == StatusCompleted || job.Status == StatusFailed {
		return
	}

	resultIDs, err := storage.DecodeTaggingJobIDs(job)
	if err != nil {
		_ = r.db.MarkTaggingJobFinished(jobID, StatusFailed, "İş yükü parse edilemedi")
		logger.Warn("TAG JOB PAYLOAD ERROR: %s - %v", jobID, err)
		return
	}

	if len(resultIDs) == 0 {
		_ = r.db.MarkTaggingJobFinished(jobID, StatusFailed, "İş yükünde kayıt yok")
		return
	}

	if err := r.db.MarkTaggingJobRunning(jobID); err != nil {
		logger.Warn("TAG JOB RUNNING ERROR: %s - %v", jobID, err)
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	r.setCancel(jobID, cancel)
	defer r.clearCancel(jobID)

	startIndex := job.ProcessedCount
	if startIndex < 0 {
		startIndex = 0
	}
	if startIndex > len(resultIDs) {
		startIndex = len(resultIDs)
	}

	successCount := job.SuccessCount
	failureCount := job.FailureCount

	logger.Info("TAG JOB START: %s (worker=%d, remaining=%d)", jobID, workerID, len(resultIDs)-startIndex)

	for _, resultID := range resultIDs[startIndex:] {
		select {
		case <-ctx.Done():
			_ = r.db.MarkTaggingJobFinished(jobID, StatusCancelled, "Kullanıcı tarafından iptal edildi")
			shared.Streamer.BroadcastLog("auto_tag_error", "Etiketleme işi iptal edildi", "")
			logger.Warn("TAG JOB CANCELLED: %s", jobID)
			return
		default:
		}

		res, err := r.engine.TagResultByID(ctx, resultID)
		if err != nil {
			failureCount++
			_ = r.db.IncrementTaggingJobProgress(jobID, false)
			shared.Streamer.BroadcastLog("auto_tag_error", fmt.Sprintf("ID %d etiketlenemedi: %v", resultID, err), "")
			continue
		}

		successCount++
		_ = r.db.IncrementTaggingJobProgress(jobID, true)
		shared.Streamer.BroadcastLog("auto_tag", fmt.Sprintf("ID %d etiketlendi: %s", resultID, res.TagsStr), "")
	}

	status := StatusCompleted
	errorMessage := ""
	if successCount == 0 && failureCount > 0 {
		status = StatusFailed
		errorMessage = "Hiçbir kayıt etiketlenemedi"
	}

	if err := r.db.MarkTaggingJobFinished(jobID, status, errorMessage); err != nil {
		logger.Warn("TAG JOB FINISH ERROR: %s - %v", jobID, err)
	}

	if status == StatusCompleted {
		shared.Streamer.BroadcastLog("success", "Etiketleme tamamlandı", "")
		logger.Info("TAG JOB COMPLETED: %s", jobID)
	} else {
		shared.Streamer.BroadcastLog("auto_tag_error", "Etiketleme tamamlandı ancak tüm kayıtlar başarısız", "")
		logger.Warn("TAG JOB FAILED: %s", jobID)
	}
}

func normalizeIDs(ids []int64) []int64 {
	if len(ids) == 0 {
		return []int64{}
	}

	seen := make(map[int64]bool)
	result := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 || seen[id] {
			continue
		}
		seen[id] = true
		result = append(result, id)
	}
	return result
}

func (r *BatchRunner) setCancel(jobID string, cancel context.CancelFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cancels[jobID] = cancel
}

func (r *BatchRunner) clearCancel(jobID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if cancel, ok := r.cancels[jobID]; ok {
		cancel()
	}
	delete(r.cancels, jobID)
}
