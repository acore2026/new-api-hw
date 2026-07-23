package model

import (
	"time"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const ChannelBenchmarkScheduleID = 1

type ChannelBenchmarkSchedule struct {
	Id              int    `json:"id" gorm:"primaryKey"`
	Enabled         bool   `json:"enabled" gorm:"default:false"`
	IntervalMinutes int    `json:"interval_minutes" gorm:"default:60"`
	RetentionDays   int    `json:"retention_days" gorm:"default:30"`
	Concurrency     int    `json:"concurrency" gorm:"default:3"`
	TimeoutSeconds  int    `json:"timeout_seconds" gorm:"default:120"`
	MaxTokens       int    `json:"max_tokens" gorm:"default:128"`
	Prompt          string `json:"prompt" gorm:"type:text"`
	EnableThinking  bool   `json:"enable_thinking" gorm:"default:true"`
	ChannelIds      string `json:"-" gorm:"type:text"`
	NextRunAt       int64  `json:"next_run_at" gorm:"bigint;default:0"`
	LastRunAt       int64  `json:"last_run_at" gorm:"bigint;default:0"`
	UpdatedAt       int64  `json:"updated_at" gorm:"bigint;default:0"`
}

type ChannelBenchmarkRun struct {
	Id          string `json:"id" gorm:"primaryKey;size:64"`
	Trigger     string `json:"trigger" gorm:"size:24;index"`
	Status      string `json:"status" gorm:"size:24;index"`
	Config      string `json:"-" gorm:"type:text"`
	Total       int    `json:"total" gorm:"default:0"`
	Completed   int    `json:"completed" gorm:"default:0"`
	Succeeded   int    `json:"succeeded" gorm:"default:0"`
	Failed      int    `json:"failed" gorm:"default:0"`
	Cancelled   int    `json:"cancelled" gorm:"default:0"`
	StartedAt   int64  `json:"started_at" gorm:"bigint;index"`
	CompletedAt int64  `json:"completed_at" gorm:"bigint;index"`
}

type ChannelBenchmarkResult struct {
	Id              int      `json:"id" gorm:"primaryKey"`
	RunId           string   `json:"run_id" gorm:"size:64;index:idx_benchmark_result_run"`
	Trigger         string   `json:"trigger" gorm:"size:24"`
	RecordedAt      int64    `json:"recorded_at" gorm:"bigint;index:idx_benchmark_result_recorded"`
	ChannelId       int      `json:"channel_id" gorm:"index:idx_benchmark_result_channel"`
	ChannelName     string   `json:"channel_name" gorm:"size:255"`
	ChannelType     int      `json:"channel_type"`
	ChannelTypeName string   `json:"channel_type_name" gorm:"size:128"`
	Model           string   `json:"model" gorm:"size:255;index:idx_benchmark_result_model"`
	Status          string   `json:"status" gorm:"size:24;index"`
	Stream          bool     `json:"stream"`
	TotalLatencyMs  int64    `json:"total_latency_ms"`
	TtftMs          *int64   `json:"ttft_ms,omitempty"`
	OutputTokens    int      `json:"output_tokens"`
	Tps             *float64 `json:"tps,omitempty"`
	Error           string   `json:"error,omitempty" gorm:"type:text"`
	ErrorCode       string   `json:"error_code,omitempty" gorm:"size:255"`
}

func GetChannelBenchmarkSchedule() (*ChannelBenchmarkSchedule, error) {
	var schedule ChannelBenchmarkSchedule
	err := DB.Where("id = ?", ChannelBenchmarkScheduleID).First(&schedule).Error
	if err != nil {
		return nil, err
	}
	return &schedule, nil
}

func SaveChannelBenchmarkSchedule(schedule *ChannelBenchmarkSchedule) error {
	if schedule == nil {
		return nil
	}
	schedule.Id = ChannelBenchmarkScheduleID
	schedule.UpdatedAt = time.Now().Unix()
	return DB.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"enabled",
			"interval_minutes",
			"retention_days",
			"concurrency",
			"timeout_seconds",
			"max_tokens",
			"prompt",
			"enable_thinking",
			"channel_ids",
			"next_run_at",
			"last_run_at",
			"updated_at",
		}),
	}).Create(schedule).Error
}

func ClaimDueChannelBenchmarkSchedule(now int64) (*ChannelBenchmarkSchedule, error) {
	schedule, err := GetChannelBenchmarkSchedule()
	if err != nil {
		return nil, err
	}
	if !schedule.Enabled || schedule.NextRunAt > now {
		return nil, nil
	}

	nextRunAt := now + int64(schedule.IntervalMinutes)*60
	result := DB.Model(&ChannelBenchmarkSchedule{}).
		Where("id = ? AND enabled = ? AND next_run_at = ?", schedule.Id, true, schedule.NextRunAt).
		Updates(map[string]interface{}{
			"last_run_at": now,
			"next_run_at": nextRunAt,
			"updated_at":  now,
		})
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, nil
	}
	schedule.LastRunAt = now
	schedule.NextRunAt = nextRunAt
	return schedule, nil
}

func CreateChannelBenchmarkRun(run *ChannelBenchmarkRun) error {
	return DB.Create(run).Error
}

func CompleteChannelBenchmarkRun(run *ChannelBenchmarkRun, results []ChannelBenchmarkResult) error {
	if run == nil {
		return nil
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&ChannelBenchmarkRun{}).
			Where("id = ?", run.Id).
			Updates(map[string]interface{}{
				"status":       run.Status,
				"completed":    run.Completed,
				"succeeded":    run.Succeeded,
				"failed":       run.Failed,
				"cancelled":    run.Cancelled,
				"completed_at": run.CompletedAt,
			}).Error; err != nil {
			return err
		}
		if len(results) == 0 {
			return nil
		}
		return tx.CreateInBatches(results, 200).Error
	})
}

func MarkInterruptedChannelBenchmarkRunsBefore(startedBefore int64) error {
	if startedBefore <= 0 {
		return nil
	}
	return DB.Model(&ChannelBenchmarkRun{}).
		Where("status IN ? AND started_at < ?", []string{"running", "cancelling"}, startedBefore).
		Updates(map[string]interface{}{
			"status":       "interrupted",
			"completed_at": time.Now().UnixMilli(),
		}).Error
}

func GetChannelBenchmarkRuns(startedAfter int64, limit int) ([]ChannelBenchmarkRun, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	var runs []ChannelBenchmarkRun
	err := DB.Where("started_at >= ?", startedAfter).
		Order("started_at DESC").
		Limit(limit).
		Find(&runs).Error
	return runs, err
}

func HasActiveChannelBenchmarkRun(startedAfter int64) (bool, error) {
	var count int64
	err := DB.Model(&ChannelBenchmarkRun{}).
		Where("status IN ? AND started_at >= ?", []string{"running", "cancelling"}, startedAfter).
		Count(&count).Error
	return count > 0, err
}

func GetChannelBenchmarkResults(
	recordedAfter int64,
	channelIds []int,
	modelNames []string,
	limit int,
) ([]ChannelBenchmarkResult, error) {
	if limit <= 0 || limit > 100000 {
		limit = 50000
	}
	query := DB.Where("recorded_at >= ?", recordedAfter)
	if len(channelIds) > 0 {
		query = query.Where("channel_id IN ?", channelIds)
	}
	if len(modelNames) > 0 {
		query = query.Where("model IN ?", modelNames)
	}
	var results []ChannelBenchmarkResult
	err := query.Order("recorded_at ASC, id ASC").Limit(limit).Find(&results).Error
	return results, err
}

func DeleteChannelBenchmarkDataBefore(cutoff int64) error {
	if cutoff <= 0 {
		return nil
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("recorded_at < ?", cutoff).
			Delete(&ChannelBenchmarkResult{}).Error; err != nil {
			return err
		}
		return tx.Where("started_at < ?", cutoff*1000).
			Delete(&ChannelBenchmarkRun{}).Error
	})
}

func EncodeChannelBenchmarkChannelIds(channelIds []int) (string, error) {
	if channelIds == nil {
		return "", nil
	}
	data, err := common.Marshal(channelIds)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func DecodeChannelBenchmarkChannelIds(value string) ([]int, error) {
	if value == "" {
		return nil, nil
	}
	var channelIds []int
	if err := common.UnmarshalJsonStr(value, &channelIds); err != nil {
		return nil, err
	}
	return channelIds, nil
}
