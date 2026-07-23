package controller

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-gonic/gin"
)

const (
	defaultChannelBenchmarkConcurrency    = 3
	defaultChannelBenchmarkTimeoutSeconds = 120
	defaultChannelBenchmarkMaxTokens      = 128
	maxChannelBenchmarkConcurrency        = 10
	maxChannelBenchmarkTimeoutSeconds     = 600
	maxChannelBenchmarkMaxTokens          = 2048
)

type channelBenchmarkConfig struct {
	Concurrency    int `json:"concurrency"`
	TimeoutSeconds int `json:"timeout_seconds"`
	MaxTokens      int `json:"max_tokens"`
}

type channelBenchmarkResult struct {
	ChannelID       int      `json:"channel_id"`
	ChannelName     string   `json:"channel_name"`
	ChannelType     int      `json:"channel_type"`
	ChannelTypeName string   `json:"channel_type_name"`
	Model           string   `json:"model"`
	Status          string   `json:"status"`
	Stream          bool     `json:"stream"`
	TotalLatencyMs  int64    `json:"total_latency_ms"`
	TTFTMs          *int64   `json:"ttft_ms,omitempty"`
	OutputTokens    int      `json:"output_tokens"`
	TPS             *float64 `json:"tps,omitempty"`
	Error           string   `json:"error,omitempty"`
	ErrorCode       string   `json:"error_code,omitempty"`
}

type channelBenchmarkJob struct {
	ID          string                   `json:"id"`
	Status      string                   `json:"status"`
	Config      channelBenchmarkConfig   `json:"config"`
	Total       int                      `json:"total"`
	Completed   int                      `json:"completed"`
	Succeeded   int                      `json:"succeeded"`
	Failed      int                      `json:"failed"`
	Cancelled   int                      `json:"cancelled"`
	StartedAt   int64                    `json:"started_at"`
	CompletedAt int64                    `json:"completed_at,omitempty"`
	Results     []channelBenchmarkResult `json:"results"`
	cancel      context.CancelFunc
}

type channelBenchmarkCase struct {
	index   int
	model   string
	channel *model.Channel
}

type channelBenchmarkWork struct {
	channel *model.Channel
	cases   []channelBenchmarkCase
}

var channelBenchmarkState = struct {
	sync.Mutex
	job *channelBenchmarkJob
}{}

func StartChannelBenchmark(c *gin.Context) {
	config := channelBenchmarkConfig{
		Concurrency:    defaultChannelBenchmarkConcurrency,
		TimeoutSeconds: defaultChannelBenchmarkTimeoutSeconds,
		MaxTokens:      defaultChannelBenchmarkMaxTokens,
	}
	if c.Request.Body != nil && c.Request.ContentLength != 0 {
		if err := common.DecodeJson(c.Request.Body, &config); err != nil && !errors.Is(err, io.EOF) {
			common.ApiErrorMsg(c, "invalid benchmark configuration")
			return
		}
	}
	if err := validateChannelBenchmarkConfig(config); err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}

	channelBenchmarkState.Lock()
	if channelBenchmarkState.job != nil &&
		(channelBenchmarkState.job.Status == "running" || channelBenchmarkState.job.Status == "cancelling") {
		channelBenchmarkState.Unlock()
		common.ApiErrorMsg(c, "a channel benchmark is already running")
		return
	}
	channelBenchmarkState.Unlock()

	testUserID, err := resolveChannelTestUserID(c)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	channels, err := model.GetAllChannels(0, 0, true, false)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	work, results := buildChannelBenchmarkWork(channels)
	if len(results) == 0 {
		common.ApiErrorMsg(c, "no configured channel models to benchmark")
		return
	}

	jobContext, cancel := context.WithCancel(context.Background())
	job := &channelBenchmarkJob{
		ID:        common.GetUUID(),
		Status:    "running",
		Config:    config,
		Total:     len(results),
		StartedAt: time.Now().UnixMilli(),
		Results:   results,
		cancel:    cancel,
	}

	channelBenchmarkState.Lock()
	if channelBenchmarkState.job != nil &&
		(channelBenchmarkState.job.Status == "running" || channelBenchmarkState.job.Status == "cancelling") {
		channelBenchmarkState.Unlock()
		cancel()
		common.ApiErrorMsg(c, "a channel benchmark is already running")
		return
	}
	channelBenchmarkState.job = job
	snapshot := cloneChannelBenchmarkJob(job)
	channelBenchmarkState.Unlock()

	gopool.Go(func() {
		runChannelBenchmark(jobContext, job.ID, testUserID, work, config)
	})
	common.ApiSuccess(c, snapshot)
}

func GetChannelBenchmark(c *gin.Context) {
	channelBenchmarkState.Lock()
	defer channelBenchmarkState.Unlock()
	if channelBenchmarkState.job == nil {
		common.ApiSuccess(c, nil)
		return
	}
	common.ApiSuccess(c, cloneChannelBenchmarkJob(channelBenchmarkState.job))
}

func CancelChannelBenchmark(c *gin.Context) {
	channelBenchmarkState.Lock()
	job := channelBenchmarkState.job
	if job == nil || (job.Status != "running" && job.Status != "cancelling") {
		channelBenchmarkState.Unlock()
		common.ApiErrorMsg(c, "no channel benchmark is running")
		return
	}
	job.Status = "cancelling"
	cancel := job.cancel
	snapshot := cloneChannelBenchmarkJob(job)
	channelBenchmarkState.Unlock()

	if cancel != nil {
		cancel()
	}
	common.ApiSuccess(c, snapshot)
}

func validateChannelBenchmarkConfig(config channelBenchmarkConfig) error {
	if config.Concurrency < 1 || config.Concurrency > maxChannelBenchmarkConcurrency {
		return fmt.Errorf("concurrency must be between 1 and %d", maxChannelBenchmarkConcurrency)
	}
	if config.TimeoutSeconds < 10 || config.TimeoutSeconds > maxChannelBenchmarkTimeoutSeconds {
		return fmt.Errorf("timeout_seconds must be between 10 and %d", maxChannelBenchmarkTimeoutSeconds)
	}
	if config.MaxTokens < 16 || config.MaxTokens > maxChannelBenchmarkMaxTokens {
		return fmt.Errorf("max_tokens must be between 16 and %d", maxChannelBenchmarkMaxTokens)
	}
	return nil
}

func buildChannelBenchmarkWork(channels []*model.Channel) ([]channelBenchmarkWork, []channelBenchmarkResult) {
	work := make([]channelBenchmarkWork, 0, len(channels))
	results := make([]channelBenchmarkResult, 0)
	for _, channel := range channels {
		if channel == nil {
			continue
		}
		modelNames := normalizeModelNames(channel.GetModels())
		if len(modelNames) == 0 && channel.TestModel != nil {
			modelNames = normalizeModelNames([]string{*channel.TestModel})
		}
		if len(modelNames) == 0 {
			continue
		}

		channelWork := channelBenchmarkWork{
			channel: channel,
			cases:   make([]channelBenchmarkCase, 0, len(modelNames)),
		}
		for _, modelName := range modelNames {
			index := len(results)
			results = append(results, channelBenchmarkResult{
				ChannelID:       channel.Id,
				ChannelName:     channel.Name,
				ChannelType:     channel.Type,
				ChannelTypeName: constant.GetChannelTypeName(channel.Type),
				Model:           modelName,
				Status:          "pending",
			})
			channelWork.cases = append(channelWork.cases, channelBenchmarkCase{
				index:   index,
				model:   modelName,
				channel: channel,
			})
		}
		work = append(work, channelWork)
	}
	return work, results
}

func runChannelBenchmark(
	ctx context.Context,
	jobID string,
	testUserID int,
	work []channelBenchmarkWork,
	config channelBenchmarkConfig,
) {
	workQueue := make(chan channelBenchmarkWork)
	var workers sync.WaitGroup
	workerCount := min(config.Concurrency, len(work))
	for range workerCount {
		workers.Add(1)
		gopool.Go(func() {
			defer workers.Done()
			for channelWork := range workQueue {
				for _, benchmarkCase := range channelWork.cases {
					if ctx.Err() != nil {
						return
					}
					setChannelBenchmarkResultStatus(jobID, benchmarkCase.index, "running")
					result := executeChannelBenchmarkCase(ctx, testUserID, benchmarkCase, config)
					recordChannelBenchmarkResult(jobID, benchmarkCase.index, result)
				}
			}
		})
	}

sendWork:
	for _, channelWork := range work {
		select {
		case <-ctx.Done():
			break sendWork
		case workQueue <- channelWork:
		}
	}
	close(workQueue)
	workers.Wait()
	finishChannelBenchmark(jobID, ctx.Err() != nil)
}

func executeChannelBenchmarkCase(
	ctx context.Context,
	testUserID int,
	benchmarkCase channelBenchmarkCase,
	config channelBenchmarkConfig,
) channelBenchmarkResult {
	startedAt := time.Now()
	isStream := shouldStreamChannelBenchmark(benchmarkCase.channel, benchmarkCase.model)
	caseContext, cancel := context.WithTimeout(ctx, time.Duration(config.TimeoutSeconds)*time.Second)
	defer cancel()

	test := testChannelWithOptions(
		benchmarkCase.channel,
		testUserID,
		benchmarkCase.model,
		"",
		isStream,
		channelTestOptions{
			context:         caseContext,
			prompt:          "Write a numbered list of concise, distinct facts. Continue until the response limit.",
			maxOutputTokens: uint(config.MaxTokens),
			logLabel:        "模型基准测试",
		},
	)

	result := channelBenchmarkResult{
		ChannelID:       benchmarkCase.channel.Id,
		ChannelName:     benchmarkCase.channel.Name,
		ChannelType:     benchmarkCase.channel.Type,
		ChannelTypeName: constant.GetChannelTypeName(benchmarkCase.channel.Type),
		Model:           benchmarkCase.model,
		Status:          "success",
		Stream:          isStream,
		TotalLatencyMs:  time.Since(startedAt).Milliseconds(),
	}
	if test.metrics != nil {
		result.TotalLatencyMs = test.metrics.totalLatencyMs
		result.TTFTMs = test.metrics.ttftMs
		result.OutputTokens = test.metrics.outputTokens
		result.TPS = test.metrics.tps
		result.Stream = test.metrics.stream
	}
	if test.localErr != nil {
		result.Status = "failed"
		result.Error = test.localErr.Error()
		if test.newAPIError != nil {
			result.ErrorCode = string(test.newAPIError.GetErrorCode())
		}
	}
	return result
}

func shouldStreamChannelBenchmark(channel *model.Channel, modelName string) bool {
	normalized := strings.ToLower(strings.TrimSpace(modelName))
	if strings.Contains(normalized, "rerank") ||
		strings.Contains(normalized, "embedding") ||
		strings.Contains(normalized, "embed") ||
		strings.HasPrefix(normalized, "m3e") ||
		strings.Contains(normalized, "bge-") ||
		strings.HasSuffix(modelName, ratio_setting.CompactModelSuffix) {
		return false
	}
	if channel != nil &&
		channel.Type == constant.ChannelTypeVolcEngine &&
		strings.Contains(normalized, "seedream") {
		return false
	}
	return true
}

func setChannelBenchmarkResultStatus(jobID string, index int, status string) {
	channelBenchmarkState.Lock()
	defer channelBenchmarkState.Unlock()
	if channelBenchmarkState.job == nil || channelBenchmarkState.job.ID != jobID {
		return
	}
	if index >= 0 && index < len(channelBenchmarkState.job.Results) {
		channelBenchmarkState.job.Results[index].Status = status
	}
}

func recordChannelBenchmarkResult(jobID string, index int, result channelBenchmarkResult) {
	channelBenchmarkState.Lock()
	defer channelBenchmarkState.Unlock()
	job := channelBenchmarkState.job
	if job == nil || job.ID != jobID || index < 0 || index >= len(job.Results) {
		return
	}
	job.Results[index] = result
	job.Completed++
	if result.Status == "success" {
		job.Succeeded++
	} else {
		job.Failed++
	}
}

func finishChannelBenchmark(jobID string, cancelled bool) {
	channelBenchmarkState.Lock()
	defer channelBenchmarkState.Unlock()
	job := channelBenchmarkState.job
	if job == nil || job.ID != jobID {
		return
	}
	if cancelled {
		job.Status = "cancelled"
		for index := range job.Results {
			if job.Results[index].Status == "pending" || job.Results[index].Status == "running" {
				job.Results[index].Status = "cancelled"
				job.Completed++
				job.Cancelled++
			}
		}
	} else {
		job.Status = "completed"
	}
	job.CompletedAt = time.Now().UnixMilli()
	job.cancel = nil
}

func cloneChannelBenchmarkJob(job *channelBenchmarkJob) *channelBenchmarkJob {
	if job == nil {
		return nil
	}
	clone := *job
	clone.Results = append([]channelBenchmarkResult(nil), job.Results...)
	clone.cancel = nil
	return &clone
}
