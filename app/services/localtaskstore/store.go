package localtaskstore

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"hostlink/config/appconf"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var ErrTerminalReserveUnavailable = errors.New("terminal reserve unavailable")

const (
	TaskStatusReceived    = "received"
	TaskStatusRunning     = "running"
	TaskStatusFinal       = "final"
	TaskStatusInterrupted = "interrupted"

	OutboxMessageTypeOutput = "output"
	OutboxMessageTypeFinal  = "final"
)

type TaskReceipt struct {
	TaskID             string
	ExecutionAttemptID string
}

type TaskState struct {
	ID                   uint
	Exists               bool
	TaskID               string
	ExecutionAttemptID   string
	Status               string
	ExitCode             int
	OutputTruncated      bool
	ErrorTruncated       bool
	LocalOutputTruncated bool
}

type OutputChunk struct {
	MessageID          string
	TaskID             string
	ExecutionAttemptID string
	Stream             string
	Sequence           int64
	Payload            string
	ByteCount          int64
}

type FinalResult struct {
	MessageID          string
	TaskID             string
	ExecutionAttemptID string
	Status             string
	ExitCode           int
	Payload            string
	OutputTruncated    bool
	ErrorTruncated     bool
}

type OutboxMessage struct {
	MessageID          string
	TaskID             string
	ExecutionAttemptID string
	Type               string
	Stream             string
	Sequence           int64
	Payload            string
	ByteCount          int64
}

type Snapshot struct {
	Tasks []TaskState
}

type Config struct {
	Path                 string
	SpoolCapBytes        int64
	TerminalReserveBytes int64
}

type ReceiptStore interface {
	RecordReceived(TaskReceipt) (TaskState, error)
	RecordStarted(taskID, executionAttemptID string) error
	TaskState(taskID, executionAttemptID string) (TaskState, error)
}

type ResultOutbox interface {
	AppendOutputChunk(OutputChunk) error
	RecordFinal(FinalResult) error
	UnackedMessages() ([]OutboxMessage, error)
	AckMessage(messageID string) error
}

type RecoveryStore interface {
	Snapshot() (Snapshot, error)
	MarkInterruptedRunningTasks() error
}

type Store struct {
	db                   *gorm.DB
	spoolCapBytes        int64
	terminalReserveBytes int64
}

type taskExecutionRecord struct {
	ID                   uint `gorm:"primaryKey"`
	TaskID               string
	ExecutionAttemptID   string
	Status               string
	ExitCode             int
	OutputTruncated      bool
	ErrorTruncated       bool
	LocalOutputTruncated bool
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

func (taskExecutionRecord) TableName() string {
	return "local_task_executions"
}

type outboxMessageRecord struct {
	ID                 uint `gorm:"primaryKey"`
	MessageID          string
	TaskID             string
	ExecutionAttemptID string
	Type               string
	Stream             string
	Sequence           int64
	Payload            string
	ByteCount          int64
	AckedAt            *time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

func (outboxMessageRecord) TableName() string {
	return "local_task_outbox_messages"
}

func New(cfg Config) (*Store, error) {
	if cfg.Path == "" {
		return nil, fmt.Errorf("local task store path is required")
	}
	if cfg.SpoolCapBytes <= 0 {
		return nil, fmt.Errorf("local task store spool cap must be positive")
	}
	if cfg.TerminalReserveBytes <= 0 {
		return nil, fmt.Errorf("local task store terminal reserve must be positive")
	}
	if cfg.TerminalReserveBytes > cfg.SpoolCapBytes {
		return nil, fmt.Errorf("local task store terminal reserve cannot exceed spool cap")
	}

	if err := os.MkdirAll(filepath.Dir(cfg.Path), 0700); err != nil {
		return nil, fmt.Errorf("create local task store directory: %w", err)
	}

	db, err := gorm.Open(sqlite.Open(cfg.Path), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("open local task store: %w", err)
	}

	store := &Store{
		db:                   db,
		spoolCapBytes:        cfg.SpoolCapBytes,
		terminalReserveBytes: cfg.TerminalReserveBytes,
	}
	if err := store.migrate(); err != nil {
		_ = store.Close()
		return nil, err
	}

	return store, nil
}

func NewDefault() (*Store, error) {
	return New(Config{
		Path:                 appconf.LocalTaskStorePath(),
		SpoolCapBytes:        appconf.LocalTaskStoreSpoolCapBytes(),
		TerminalReserveBytes: appconf.LocalTaskStoreTerminalReserveBytes(),
	})
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	db, err := s.db.DB()
	if err != nil {
		return err
	}
	return db.Close()
}

func (s *Store) migrate() error {
	if err := s.db.AutoMigrate(&taskExecutionRecord{}, &outboxMessageRecord{}); err != nil {
		return fmt.Errorf("migrate local task store: %w", err)
	}
	if err := s.db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_local_task_executions_attempt ON local_task_executions(task_id, execution_attempt_id)").Error; err != nil {
		return fmt.Errorf("migrate local task execution index: %w", err)
	}
	if err := s.db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_local_task_outbox_message_id ON local_task_outbox_messages(message_id)").Error; err != nil {
		return fmt.Errorf("migrate local task outbox index: %w", err)
	}
	return nil
}

func (s *Store) RecordReceived(receipt TaskReceipt) (TaskState, error) {
	if receipt.TaskID == "" {
		return TaskState{}, fmt.Errorf("task ID is required")
	}
	if receipt.ExecutionAttemptID == "" {
		return TaskState{}, fmt.Errorf("execution attempt ID is required")
	}

	var state TaskState
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := s.ensureTerminalReserveAvailable(tx); err != nil {
			return err
		}

		var existing taskExecutionRecord
		err := tx.Where("task_id = ? AND execution_attempt_id = ?", receipt.TaskID, receipt.ExecutionAttemptID).First(&existing).Error
		if err == nil {
			state = taskStateFromRecord(existing)
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		record := taskExecutionRecord{
			TaskID:             receipt.TaskID,
			ExecutionAttemptID: receipt.ExecutionAttemptID,
			Status:             TaskStatusReceived,
		}
		if err := tx.Create(&record).Error; err != nil {
			return err
		}
		state = taskStateFromRecord(record)
		return nil
	})
	if err != nil {
		return TaskState{}, fmt.Errorf("record task receipt: %w", err)
	}
	return state, nil
}

func (s *Store) TaskState(taskID, executionAttemptID string) (TaskState, error) {
	query := s.db.Where("task_id = ?", taskID)
	if executionAttemptID != "" {
		query = query.Where("execution_attempt_id = ?", executionAttemptID)
	}

	var record taskExecutionRecord
	err := query.Order("updated_at DESC, id DESC").First(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return TaskState{}, nil
	}
	if err != nil {
		return TaskState{}, fmt.Errorf("load task state: %w", err)
	}
	return taskStateFromRecord(record), nil
}

func (s *Store) RecordStarted(taskID, executionAttemptID string) error {
	if taskID == "" {
		return fmt.Errorf("task ID is required")
	}
	if executionAttemptID == "" {
		return fmt.Errorf("execution attempt ID is required")
	}

	return s.db.Transaction(func(tx *gorm.DB) error {
		return s.upsertExecutionState(tx, taskExecutionRecord{
			TaskID:             taskID,
			ExecutionAttemptID: executionAttemptID,
			Status:             TaskStatusRunning,
		})
	})
}

func (s *Store) AppendOutputChunk(chunk OutputChunk) error {
	if err := validateOutputChunk(chunk); err != nil {
		return err
	}

	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := s.upsertExecutionState(tx, taskExecutionRecord{
			TaskID:             chunk.TaskID,
			ExecutionAttemptID: chunk.ExecutionAttemptID,
			Status:             TaskStatusRunning,
		}); err != nil {
			return err
		}

		record := outboxMessageRecord{
			MessageID:          chunk.MessageID,
			TaskID:             chunk.TaskID,
			ExecutionAttemptID: chunk.ExecutionAttemptID,
			Type:               OutboxMessageTypeOutput,
			Stream:             chunk.Stream,
			Sequence:           chunk.Sequence,
			Payload:            chunk.Payload,
			ByteCount:          chunk.ByteCount,
		}
		if err := tx.Create(&record).Error; err != nil {
			return err
		}
		return s.enforceChunkCap(tx)
	})
}

func (s *Store) RecordFinal(result FinalResult) error {
	if err := validateFinalResult(result); err != nil {
		return err
	}

	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := s.rotateChunksForTerminal(tx, int64(len(result.Payload))); err != nil {
			return err
		}

		if err := s.upsertExecutionState(tx, taskExecutionRecord{
			TaskID:             result.TaskID,
			ExecutionAttemptID: result.ExecutionAttemptID,
			Status:             TaskStatusFinal,
			ExitCode:           result.ExitCode,
			OutputTruncated:    result.OutputTruncated,
			ErrorTruncated:     result.ErrorTruncated,
		}); err != nil {
			return err
		}

		record := outboxMessageRecord{
			MessageID:          result.MessageID,
			TaskID:             result.TaskID,
			ExecutionAttemptID: result.ExecutionAttemptID,
			Type:               OutboxMessageTypeFinal,
			Payload:            result.Payload,
			ByteCount:          int64(len(result.Payload)),
		}
		return tx.Create(&record).Error
	})
}

func (s *Store) ensureTerminalReserveAvailable(tx *gorm.DB) error {
	used, err := s.totalUnackedBytes(tx)
	if err != nil {
		return err
	}
	if used+s.terminalReserveBytes > s.spoolCapBytes {
		return ErrTerminalReserveUnavailable
	}
	return nil
}

func (s *Store) enforceChunkCap(tx *gorm.DB) error {
	chunkCap := s.spoolCapBytes - s.terminalReserveBytes
	if chunkCap < 0 {
		chunkCap = 0
	}

	for {
		used, err := s.unackedBytesByType(tx, OutboxMessageTypeOutput)
		if err != nil {
			return err
		}
		if used <= chunkCap {
			return nil
		}
		rotated, err := s.rotateOldestChunk(tx)
		if err != nil {
			return err
		}
		if !rotated {
			return nil
		}
	}
}

func (s *Store) rotateChunksForTerminal(tx *gorm.DB, terminalBytes int64) error {
	for {
		used, err := s.totalUnackedBytes(tx)
		if err != nil {
			return err
		}
		if used+terminalBytes <= s.spoolCapBytes {
			return nil
		}
		rotated, err := s.rotateOldestChunk(tx)
		if err != nil {
			return err
		}
		if !rotated {
			return ErrTerminalReserveUnavailable
		}
	}
}

func (s *Store) rotateOldestChunk(tx *gorm.DB) (bool, error) {
	var oldest outboxMessageRecord
	err := tx.Where("acked_at IS NULL AND type = ?", OutboxMessageTypeOutput).Order("created_at ASC, id ASC").First(&oldest).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	now := time.Now().UTC()
	if err := tx.Model(&oldest).Update("acked_at", now).Error; err != nil {
		return false, err
	}
	if err := s.markLocalOutputTruncated(tx, oldest.TaskID, oldest.ExecutionAttemptID); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) markLocalOutputTruncated(tx *gorm.DB, taskID, executionAttemptID string) error {
	return s.upsertExecutionState(tx, taskExecutionRecord{
		TaskID:               taskID,
		ExecutionAttemptID:   executionAttemptID,
		Status:               TaskStatusRunning,
		LocalOutputTruncated: true,
	})
}

func (s *Store) totalUnackedBytes(tx *gorm.DB) (int64, error) {
	var total int64
	if err := tx.Model(&outboxMessageRecord{}).Where("acked_at IS NULL").Select("COALESCE(SUM(byte_count), 0)").Scan(&total).Error; err != nil {
		return 0, err
	}
	return total, nil
}

func (s *Store) unackedBytesByType(tx *gorm.DB, messageType string) (int64, error) {
	var total int64
	if err := tx.Model(&outboxMessageRecord{}).Where("acked_at IS NULL AND type = ?", messageType).Select("COALESCE(SUM(byte_count), 0)").Scan(&total).Error; err != nil {
		return 0, err
	}
	return total, nil
}

func (s *Store) UnackedMessages() ([]OutboxMessage, error) {
	var records []outboxMessageRecord
	if err := s.db.Where("acked_at IS NULL").Order("created_at ASC, id ASC").Find(&records).Error; err != nil {
		return nil, fmt.Errorf("load unacked messages: %w", err)
	}

	messages := make([]OutboxMessage, 0, len(records))
	for _, record := range records {
		messages = append(messages, outboxMessageFromRecord(record))
	}
	return messages, nil
}

func (s *Store) AckMessage(messageID string) error {
	if messageID == "" {
		return fmt.Errorf("message ID is required")
	}
	now := time.Now().UTC()
	if err := s.db.Model(&outboxMessageRecord{}).Where("message_id = ? AND acked_at IS NULL", messageID).Update("acked_at", now).Error; err != nil {
		return fmt.Errorf("ack message: %w", err)
	}
	return nil
}

func (s *Store) Snapshot() (Snapshot, error) {
	var records []taskExecutionRecord
	if err := s.db.Order("updated_at ASC, id ASC").Find(&records).Error; err != nil {
		return Snapshot{}, fmt.Errorf("load task snapshot: %w", err)
	}

	snapshot := Snapshot{Tasks: make([]TaskState, 0, len(records))}
	for _, record := range records {
		snapshot.Tasks = append(snapshot.Tasks, taskStateFromRecord(record))
	}
	return snapshot, nil
}

func (s *Store) MarkInterruptedRunningTasks() error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		var running []taskExecutionRecord
		if err := tx.Where("status = ?", TaskStatusRunning).Find(&running).Error; err != nil {
			return err
		}

		for _, record := range running {
			if err := tx.Model(&record).Updates(map[string]any{
				"status":    TaskStatusInterrupted,
				"exit_code": -1,
			}).Error; err != nil {
				return err
			}

			messageID := interruptedMessageID(record.TaskID, record.ExecutionAttemptID)
			var existing outboxMessageRecord
			err := tx.Where("message_id = ?", messageID).First(&existing).Error
			if err == nil {
				continue
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}

			payload := fmt.Sprintf(`{"status":"interrupted","exit_code":-1,"output_truncated":%t,"error_truncated":%t}`, record.OutputTruncated, record.ErrorTruncated)
			outbox := outboxMessageRecord{
				MessageID:          messageID,
				TaskID:             record.TaskID,
				ExecutionAttemptID: record.ExecutionAttemptID,
				Type:               OutboxMessageTypeFinal,
				Payload:            payload,
				ByteCount:          int64(len(payload)),
			}
			if err := tx.Create(&outbox).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) upsertExecutionState(tx *gorm.DB, next taskExecutionRecord) error {
	var existing taskExecutionRecord
	err := tx.Where("task_id = ? AND execution_attempt_id = ?", next.TaskID, next.ExecutionAttemptID).First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return tx.Create(&next).Error
	}
	if err != nil {
		return err
	}

	updates := map[string]any{"status": next.Status}
	if next.ExitCode != 0 {
		updates["exit_code"] = next.ExitCode
	}
	if next.OutputTruncated {
		updates["output_truncated"] = true
	}
	if next.ErrorTruncated {
		updates["error_truncated"] = true
	}
	if next.LocalOutputTruncated {
		updates["local_output_truncated"] = true
	}
	return tx.Model(&existing).Updates(updates).Error
}

func validateOutputChunk(chunk OutputChunk) error {
	if chunk.MessageID == "" {
		return fmt.Errorf("message ID is required")
	}
	if chunk.TaskID == "" {
		return fmt.Errorf("task ID is required")
	}
	if chunk.ExecutionAttemptID == "" {
		return fmt.Errorf("execution attempt ID is required")
	}
	if chunk.Stream == "" {
		return fmt.Errorf("stream is required")
	}
	if chunk.Sequence <= 0 {
		return fmt.Errorf("sequence must be positive")
	}
	if chunk.ByteCount < 0 {
		return fmt.Errorf("byte count cannot be negative")
	}
	return nil
}

func validateFinalResult(result FinalResult) error {
	if result.MessageID == "" {
		return fmt.Errorf("message ID is required")
	}
	if result.TaskID == "" {
		return fmt.Errorf("task ID is required")
	}
	if result.ExecutionAttemptID == "" {
		return fmt.Errorf("execution attempt ID is required")
	}
	if result.Status == "" {
		return fmt.Errorf("status is required")
	}
	return nil
}

func taskStateFromRecord(record taskExecutionRecord) TaskState {
	return TaskState{
		ID:                   record.ID,
		Exists:               true,
		TaskID:               record.TaskID,
		ExecutionAttemptID:   record.ExecutionAttemptID,
		Status:               record.Status,
		ExitCode:             record.ExitCode,
		OutputTruncated:      record.OutputTruncated,
		ErrorTruncated:       record.ErrorTruncated,
		LocalOutputTruncated: record.LocalOutputTruncated,
	}
}

func outboxMessageFromRecord(record outboxMessageRecord) OutboxMessage {
	return OutboxMessage{
		MessageID:          record.MessageID,
		TaskID:             record.TaskID,
		ExecutionAttemptID: record.ExecutionAttemptID,
		Type:               record.Type,
		Stream:             record.Stream,
		Sequence:           record.Sequence,
		Payload:            record.Payload,
		ByteCount:          record.ByteCount,
	}
}

func interruptedMessageID(taskID, executionAttemptID string) string {
	return "local-interrupted-" + strings.NewReplacer("/", "-", " ", "-", "|", "-").Replace(taskID+"-"+executionAttemptID)
}
