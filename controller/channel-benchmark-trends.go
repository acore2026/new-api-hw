package controller

import (
	"errors"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const (
	defaultChannelBenchmarkIntervalMinutes = 60
	defaultChannelBenchmarkRetentionDays   = 30
	minChannelBenchmarkIntervalMinutes     = 5
	maxChannelBenchmarkIntervalMinutes     = 24 * 60
	maxChannelBenchmarkTrendHours          = 24 * 30
)

type channelBenchmarkSchedulePayload struct {
	Enabled         bool   `json:"enabled"`
	IntervalMinutes int    `json:"interval_minutes"`
	RetentionDays   int    `json:"retention_days"`
	Concurrency     int    `json:"concurrency"`
	TimeoutSeconds  int    `json:"timeout_seconds"`
	MaxTokens       int    `json:"max_tokens"`
	Prompt          string `json:"prompt"`
	EnableThinking  bool   `json:"enable_thinking"`
	ChannelIDs      []int  `json:"channel_ids"`
	NextRunAt       int64  `json:"next_run_at"`
	LastRunAt       int64  `json:"last_run_at"`
	UpdatedAt       int64  `json:"updated_at"`
}

type channelBenchmarkTrendSummary struct {
	Samples          int     `json:"samples"`
	Succeeded        int     `json:"succeeded"`
	Failed           int     `json:"failed"`
	SuccessRate      float64 `json:"success_rate"`
	AverageTps       float64 `json:"average_tps"`
	MedianTps        float64 `json:"median_tps"`
	P95Tps           float64 `json:"p95_tps"`
	AverageTtftMs    int64   `json:"average_ttft_ms"`
	P95TtftMs        int64   `json:"p95_ttft_ms"`
	AverageLatencyMs int64   `json:"average_latency_ms"`
	P95LatencyMs     int64   `json:"p95_latency_ms"`
	OutputTokens     int64   `json:"output_tokens"`
}

type channelBenchmarkTrendChannel struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

func defaultChannelBenchmarkSchedule() *model.ChannelBenchmarkSchedule {
	return &model.ChannelBenchmarkSchedule{
		Id:              model.ChannelBenchmarkScheduleID,
		Enabled:         false,
		IntervalMinutes: defaultChannelBenchmarkIntervalMinutes,
		RetentionDays:   defaultChannelBenchmarkRetentionDays,
		Concurrency:     defaultChannelBenchmarkConcurrency,
		TimeoutSeconds:  defaultChannelBenchmarkTimeoutSeconds,
		MaxTokens:       defaultChannelBenchmarkMaxTokens,
		Prompt:          defaultChannelBenchmarkPrompt,
		EnableThinking:  true,
	}
}

func getOrCreateChannelBenchmarkSchedule() (*model.ChannelBenchmarkSchedule, error) {
	schedule, err := model.GetChannelBenchmarkSchedule()
	if err == nil {
		return schedule, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	schedule = defaultChannelBenchmarkSchedule()
	if err := model.SaveChannelBenchmarkSchedule(schedule); err != nil {
		return nil, err
	}
	return schedule, nil
}

func schedulePayloadFromModel(schedule *model.ChannelBenchmarkSchedule) (channelBenchmarkSchedulePayload, error) {
	channelIDs, err := model.DecodeChannelBenchmarkChannelIds(schedule.ChannelIds)
	if err != nil {
		return channelBenchmarkSchedulePayload{}, err
	}
	return channelBenchmarkSchedulePayload{
		Enabled:         schedule.Enabled,
		IntervalMinutes: schedule.IntervalMinutes,
		RetentionDays:   schedule.RetentionDays,
		Concurrency:     schedule.Concurrency,
		TimeoutSeconds:  schedule.TimeoutSeconds,
		MaxTokens:       schedule.MaxTokens,
		Prompt:          schedule.Prompt,
		EnableThinking:  schedule.EnableThinking,
		ChannelIDs:      channelIDs,
		NextRunAt:       schedule.NextRunAt,
		LastRunAt:       schedule.LastRunAt,
		UpdatedAt:       schedule.UpdatedAt,
	}, nil
}

func GetChannelBenchmarkSchedule(c *gin.Context) {
	schedule, err := getOrCreateChannelBenchmarkSchedule()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	payload, err := schedulePayloadFromModel(schedule)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, payload)
}

func UpdateChannelBenchmarkSchedule(c *gin.Context) {
	var payload channelBenchmarkSchedulePayload
	if err := common.DecodeJson(c.Request.Body, &payload); err != nil {
		common.ApiErrorMsg(c, "invalid benchmark schedule")
		return
	}
	payload.Prompt = strings.TrimSpace(payload.Prompt)
	if payload.IntervalMinutes < minChannelBenchmarkIntervalMinutes ||
		payload.IntervalMinutes > maxChannelBenchmarkIntervalMinutes {
		common.ApiErrorMsg(c, "interval_minutes must be between 5 and 1440")
		return
	}
	if payload.RetentionDays < 1 || payload.RetentionDays > 365 {
		common.ApiErrorMsg(c, "retention_days must be between 1 and 365")
		return
	}
	config := channelBenchmarkConfig{
		Concurrency:    payload.Concurrency,
		TimeoutSeconds: payload.TimeoutSeconds,
		MaxTokens:      payload.MaxTokens,
		Prompt:         payload.Prompt,
		EnableThinking: payload.EnableThinking,
		ChannelIDs:     payload.ChannelIDs,
	}
	if err := validateChannelBenchmarkConfig(config); err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	channelIDs, err := model.EncodeChannelBenchmarkChannelIds(payload.ChannelIDs)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	existing, err := getOrCreateChannelBenchmarkSchedule()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	now := time.Now().Unix()
	nextRunAt := int64(0)
	if payload.Enabled {
		nextRunAt = now + int64(payload.IntervalMinutes)*60
		if existing.Enabled &&
			existing.IntervalMinutes == payload.IntervalMinutes &&
			existing.NextRunAt > now {
			nextRunAt = existing.NextRunAt
		}
	}
	schedule := &model.ChannelBenchmarkSchedule{
		Id:              model.ChannelBenchmarkScheduleID,
		Enabled:         payload.Enabled,
		IntervalMinutes: payload.IntervalMinutes,
		RetentionDays:   payload.RetentionDays,
		Concurrency:     payload.Concurrency,
		TimeoutSeconds:  payload.TimeoutSeconds,
		MaxTokens:       payload.MaxTokens,
		Prompt:          payload.Prompt,
		EnableThinking:  payload.EnableThinking,
		ChannelIds:      channelIDs,
		NextRunAt:       nextRunAt,
		LastRunAt:       existing.LastRunAt,
	}
	if err := model.SaveChannelBenchmarkSchedule(schedule); err != nil {
		common.ApiError(c, err)
		return
	}
	response, err := schedulePayloadFromModel(schedule)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, response)
}

func GetChannelBenchmarkTrends(c *gin.Context) {
	hours := 24
	if raw := c.Query("hours"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			hours = parsed
		}
	}
	if hours < 1 {
		hours = 1
	}
	if hours > maxChannelBenchmarkTrendHours {
		hours = maxChannelBenchmarkTrendHours
	}
	channelIDs := parseBenchmarkIntList(c.Query("channel_ids"))
	modelNames := parseBenchmarkStringList(c.Query("models"))
	startedAfter := time.Now().Add(-time.Duration(hours) * time.Hour).Unix()

	results, err := model.GetChannelBenchmarkResults(startedAfter, channelIDs, modelNames, 50000)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	runs, err := model.GetChannelBenchmarkRuns(startedAfter*1000, 500)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	modelSet := make(map[string]struct{})
	channelMap := make(map[int]string)
	for _, result := range results {
		modelSet[result.Model] = struct{}{}
		channelMap[result.ChannelId] = result.ChannelName
	}
	models := make([]string, 0, len(modelSet))
	for modelName := range modelSet {
		models = append(models, modelName)
	}
	sort.Strings(models)
	channels := make([]channelBenchmarkTrendChannel, 0, len(channelMap))
	for channelID, channelName := range channelMap {
		channels = append(channels, channelBenchmarkTrendChannel{ID: channelID, Name: channelName})
	}
	sort.Slice(channels, func(i, j int) bool {
		if channels[i].Name == channels[j].Name {
			return channels[i].ID < channels[j].ID
		}
		return channels[i].Name < channels[j].Name
	})

	common.ApiSuccess(c, gin.H{
		"hours":     hours,
		"summary":   summarizeChannelBenchmarkResults(results),
		"runs":      runs,
		"results":   results,
		"models":    models,
		"channels":  channels,
		"truncated": len(results) >= 50000,
	})
}

func summarizeChannelBenchmarkResults(results []model.ChannelBenchmarkResult) channelBenchmarkTrendSummary {
	summary := channelBenchmarkTrendSummary{Samples: len(results)}
	tpsValues := make([]float64, 0, len(results))
	ttftValues := make([]float64, 0, len(results))
	latencyValues := make([]float64, 0, len(results))
	var tpsSum float64
	var ttftSum float64
	var latencySum float64
	for _, result := range results {
		if result.Status == "success" {
			summary.Succeeded++
		} else if result.Status == "failed" {
			summary.Failed++
		}
		if result.Tps != nil && *result.Tps >= 0 {
			tpsValues = append(tpsValues, *result.Tps)
			tpsSum += *result.Tps
		}
		if result.TtftMs != nil && *result.TtftMs >= 0 {
			value := float64(*result.TtftMs)
			ttftValues = append(ttftValues, value)
			ttftSum += value
		}
		if result.TotalLatencyMs >= 0 {
			value := float64(result.TotalLatencyMs)
			latencyValues = append(latencyValues, value)
			latencySum += value
		}
		summary.OutputTokens += int64(result.OutputTokens)
	}
	if summary.Samples > 0 {
		summary.SuccessRate = roundBenchmarkMetric(float64(summary.Succeeded) / float64(summary.Samples) * 100)
	}
	if len(tpsValues) > 0 {
		sort.Float64s(tpsValues)
		summary.AverageTps = roundBenchmarkMetric(tpsSum / float64(len(tpsValues)))
		summary.MedianTps = roundBenchmarkMetric(percentileBenchmarkMetric(tpsValues, 0.5))
		summary.P95Tps = roundBenchmarkMetric(percentileBenchmarkMetric(tpsValues, 0.95))
	}
	if len(ttftValues) > 0 {
		sort.Float64s(ttftValues)
		summary.AverageTtftMs = int64(math.Round(ttftSum / float64(len(ttftValues))))
		summary.P95TtftMs = int64(math.Round(percentileBenchmarkMetric(ttftValues, 0.95)))
	}
	if len(latencyValues) > 0 {
		sort.Float64s(latencyValues)
		summary.AverageLatencyMs = int64(math.Round(latencySum / float64(len(latencyValues))))
		summary.P95LatencyMs = int64(math.Round(percentileBenchmarkMetric(latencyValues, 0.95)))
	}
	return summary
}

func percentileBenchmarkMetric(sorted []float64, percentile float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	index := int(math.Ceil(percentile*float64(len(sorted)))) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	return sorted[index]
}

func roundBenchmarkMetric(value float64) float64 {
	return math.Round(value*100) / 100
}

func parseBenchmarkIntList(value string) []int {
	parts := parseBenchmarkStringList(value)
	values := make([]int, 0, len(parts))
	for _, part := range parts {
		if parsed, err := strconv.Atoi(part); err == nil && parsed > 0 {
			values = append(values, parsed)
		}
	}
	return values
}

func parseBenchmarkStringList(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			values = append(values, trimmed)
		}
	}
	return values
}

func InitChannelBenchmarkScheduler() {
	staleBefore := time.Now().Add(-7 * 24 * time.Hour).UnixMilli()
	if err := model.MarkInterruptedChannelBenchmarkRunsBefore(staleBefore); err != nil {
		common.SysError("failed to mark interrupted channel benchmarks: " + err.Error())
	}
	if _, err := getOrCreateChannelBenchmarkSchedule(); err != nil {
		common.SysError("failed to initialize channel benchmark schedule: " + err.Error())
		return
	}
	go func() {
		timer := time.NewTimer(10 * time.Second)
		defer timer.Stop()
		for {
			<-timer.C
			runDueChannelBenchmark()
			timer.Reset(30 * time.Second)
		}
	}()
}

func runDueChannelBenchmark() {
	channelBenchmarkState.Lock()
	active := channelBenchmarkState.job != nil &&
		(channelBenchmarkState.job.Status == "running" || channelBenchmarkState.job.Status == "cancelling")
	channelBenchmarkState.Unlock()
	if active {
		return
	}

	now := time.Now().Unix()
	active, err := model.HasActiveChannelBenchmarkRun(time.Now().Add(-24 * time.Hour).UnixMilli())
	if err != nil {
		common.SysError("failed to inspect active channel benchmarks: " + err.Error())
		return
	}
	if active {
		return
	}
	schedule, err := model.ClaimDueChannelBenchmarkSchedule(now)
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			common.SysError("failed to claim channel benchmark schedule: " + err.Error())
		}
		return
	}
	if schedule == nil {
		return
	}
	channelIDs, err := model.DecodeChannelBenchmarkChannelIds(schedule.ChannelIds)
	if err != nil {
		common.SysError("failed to decode channel benchmark schedule: " + err.Error())
		return
	}
	testUserID, err := resolveChannelTestUserID(nil)
	if err != nil {
		common.SysError("failed to resolve scheduled benchmark user: " + err.Error())
		return
	}
	_, err = startChannelBenchmark(channelBenchmarkConfig{
		Concurrency:    schedule.Concurrency,
		TimeoutSeconds: schedule.TimeoutSeconds,
		MaxTokens:      schedule.MaxTokens,
		Prompt:         schedule.Prompt,
		EnableThinking: schedule.EnableThinking,
		ChannelIDs:     channelIDs,
	}, testUserID, "scheduled")
	if err != nil {
		common.SysError("failed to start scheduled channel benchmark: " + err.Error())
	}
	if schedule.RetentionDays > 0 {
		cutoff := time.Now().Add(-time.Duration(schedule.RetentionDays) * 24 * time.Hour).Unix()
		if err := model.DeleteChannelBenchmarkDataBefore(cutoff); err != nil {
			common.SysError("failed to clean channel benchmark history: " + err.Error())
		}
	}
}
