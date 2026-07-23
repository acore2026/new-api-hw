/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  ChartLineData01Icon,
  Clock01Icon,
  FloppyDiskIcon,
  PlayIcon,
  RefreshIcon,
  Settings02Icon,
} from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { useTranslation } from 'react-i18next'
import {
  Area,
  AreaChart,
  Bar,
  CartesianGrid,
  ComposedChart,
  Line,
  LineChart,
  XAxis,
  YAxis,
} from 'recharts'
import { toast } from 'sonner'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from '@/components/ui/chart'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from '@/components/ui/empty'
import {
  Field,
  FieldContent,
  FieldDescription,
  FieldGroup,
  FieldLabel,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { ScrollArea } from '@/components/ui/scroll-area'
import { Spinner } from '@/components/ui/spinner'
import { Switch } from '@/components/ui/switch'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { Textarea } from '@/components/ui/textarea'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { SectionPageLayout } from '@/components/layout'
import {
  formatLatency,
  formatThroughput,
  formatUptimePct,
} from '@/features/performance-metrics/lib/format'
import {
  getAllChannels,
  getChannelBenchmarkSchedule,
  getChannelBenchmarkTrends,
  startChannelBenchmark,
  updateChannelBenchmarkSchedule,
} from './api'
import { CHANNEL_STATUS } from './constants'
import type {
  ChannelBenchmarkSchedule,
  ChannelBenchmarkTrendResult,
  ChannelBenchmarkTrendSummary,
} from './types'

const DEFAULT_SCHEDULE: ChannelBenchmarkSchedule = {
  enabled: false,
  interval_minutes: 60,
  retention_days: 30,
  concurrency: 3,
  timeout_seconds: 120,
  max_tokens: 128,
  prompt:
    'Write a numbered list of concise, distinct facts. Continue until the response limit.',
  enable_thinking: true,
  channel_ids: null,
  next_run_at: 0,
  last_run_at: 0,
  updated_at: 0,
}

type TrendPoint = {
  ts: number
  tps: number
  ttft: number
  latency: number
  successRate: number
  outputTokens: number
  samples: number
}

type EntityRollup = {
  key: string
  channel: string
  model: string
  samples: number
  successRate: number
  averageTps: number
  averageTtft: number
  averageLatency: number
  latestAt: number
}

function average(values: number[]) {
  if (values.length === 0) return 0
  return values.reduce((sum, value) => sum + value, 0) / values.length
}

function percentile(values: number[], value: number) {
  if (values.length === 0) return 0
  const sorted = [...values].sort((a, b) => a - b)
  const index = Math.max(
    0,
    Math.min(sorted.length - 1, Math.ceil(sorted.length * value) - 1)
  )
  return sorted[index]
}

function summarize(results: ChannelBenchmarkTrendResult[]) {
  const succeeded = results.filter((result) => result.status === 'success')
  const failed = results.filter((result) => result.status === 'failed')
  const tps = succeeded
    .map((result) => result.tps)
    .filter((value): value is number => typeof value === 'number')
  const ttft = succeeded
    .map((result) => result.ttft_ms)
    .filter((value): value is number => typeof value === 'number')
  const latency = succeeded.map((result) => result.total_latency_ms)

  return {
    samples: results.length,
    succeeded: succeeded.length,
    failed: failed.length,
    success_rate:
      results.length > 0 ? (succeeded.length / results.length) * 100 : 0,
    average_tps: average(tps),
    median_tps: percentile(tps, 0.5),
    p95_tps: percentile(tps, 0.95),
    average_ttft_ms: average(ttft),
    p95_ttft_ms: percentile(ttft, 0.95),
    average_latency_ms: average(latency),
    p95_latency_ms: percentile(latency, 0.95),
    output_tokens: results.reduce(
      (sum, result) => sum + result.output_tokens,
      0
    ),
  } satisfies ChannelBenchmarkTrendSummary
}

function buildTrendPoints(results: ChannelBenchmarkTrendResult[]) {
  const buckets = new Map<number, ChannelBenchmarkTrendResult[]>()
  for (const result of results) {
    const current = buckets.get(result.recorded_at) ?? []
    current.push(result)
    buckets.set(result.recorded_at, current)
  }
  return Array.from(buckets.entries())
    .sort(([a], [b]) => a - b)
    .map(([ts, bucket]): TrendPoint => {
      const successful = bucket.filter((result) => result.status === 'success')
      const tps = successful
        .map((result) => result.tps)
        .filter((value): value is number => typeof value === 'number')
      const ttft = successful
        .map((result) => result.ttft_ms)
        .filter((value): value is number => typeof value === 'number')
      return {
        ts,
        tps: average(tps),
        ttft: average(ttft),
        latency: average(successful.map((result) => result.total_latency_ms)),
        successRate:
          bucket.length > 0 ? (successful.length / bucket.length) * 100 : 0,
        outputTokens: bucket.reduce(
          (sum, result) => sum + result.output_tokens,
          0
        ),
        samples: bucket.length,
      }
    })
}

function buildEntityRollups(results: ChannelBenchmarkTrendResult[]) {
  const groups = new Map<string, ChannelBenchmarkTrendResult[]>()
  for (const result of results) {
    const key = `${result.channel_id}:${result.model}`
    const current = groups.get(key) ?? []
    current.push(result)
    groups.set(key, current)
  }
  return Array.from(groups.entries())
    .map(([key, rows]): EntityRollup => {
      const successful = rows.filter((row) => row.status === 'success')
      const tps = successful
        .map((row) => row.tps)
        .filter((value): value is number => typeof value === 'number')
      const ttft = successful
        .map((row) => row.ttft_ms)
        .filter((value): value is number => typeof value === 'number')
      return {
        key,
        channel: rows[0]?.channel_name ?? '',
        model: rows[0]?.model ?? '',
        samples: rows.length,
        successRate:
          rows.length > 0 ? (successful.length / rows.length) * 100 : 0,
        averageTps: average(tps),
        averageTtft: average(ttft),
        averageLatency: average(successful.map((row) => row.total_latency_ms)),
        latestAt: Math.max(...rows.map((row) => row.recorded_at)),
      }
    })
    .sort((a, b) => b.averageTps - a.averageTps)
}

function formatTime(timestamp: number, milliseconds = false) {
  if (!timestamp) return '—'
  return new Date(timestamp * (milliseconds ? 1 : 1000)).toLocaleString(
    undefined,
    {
      month: 'short',
      day: 'numeric',
      hour: '2-digit',
      minute: '2-digit',
    }
  )
}

function formatChartTime(timestamp: number) {
  return new Date(timestamp * 1000).toLocaleTimeString(undefined, {
    hour: '2-digit',
    minute: '2-digit',
  })
}

function MetricCard(props: { label: string; value: string; detail: string }) {
  return (
    <Card>
      <CardHeader className='gap-1'>
        <CardDescription>{props.label}</CardDescription>
        <CardTitle className='font-mono text-2xl tabular-nums'>
          {props.value}
        </CardTitle>
      </CardHeader>
      <CardContent className='text-muted-foreground text-xs'>
        {props.detail}
      </CardContent>
    </Card>
  )
}

function TrendChartCard(props: {
  title: string
  description: string
  children: React.ReactNode
}) {
  return (
    <Card className='min-w-0'>
      <CardHeader>
        <CardTitle>{props.title}</CardTitle>
        <CardDescription>{props.description}</CardDescription>
      </CardHeader>
      <CardContent>{props.children}</CardContent>
    </Card>
  )
}

export function BenchmarkTrends() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [hours, setHours] = useState(24)
  const [selectedChannel, setSelectedChannel] = useState('all')
  const [selectedModel, setSelectedModel] = useState('all')
  const [draftOverride, setDraftOverride] =
    useState<ChannelBenchmarkSchedule | null>(null)
  const chartConfig = useMemo(
    () =>
      ({
        tps: { label: 'TPS', color: 'var(--chart-1)' },
        ttft: { label: 'TTFT', color: 'var(--chart-2)' },
        latency: {
          label: t('Total latency'),
          color: 'var(--chart-3)',
        },
        successRate: {
          label: t('Success rate'),
          color: 'var(--chart-4)',
        },
        outputTokens: {
          label: t('Output tokens'),
          color: 'var(--chart-5)',
        },
      }) satisfies ChartConfig,
    [t]
  )

  const channelsQuery = useQuery({
    queryKey: ['channels', 'benchmark-schedule'],
    queryFn: getAllChannels,
  })
  const scheduleQuery = useQuery({
    queryKey: ['channel-benchmark-schedule'],
    queryFn: getChannelBenchmarkSchedule,
  })
  const trendsQuery = useQuery({
    queryKey: ['channel-benchmark-trends', hours],
    queryFn: () => getChannelBenchmarkTrends({ hours }),
    refetchInterval: 30_000,
  })

  const draft = useMemo(() => {
    const schedule = scheduleQuery.data?.data
    const channels = channelsQuery.data
    if (draftOverride) return draftOverride
    if (!schedule || !channels) return DEFAULT_SCHEDULE
    return {
      ...schedule,
      channel_ids:
        schedule.channel_ids ??
        channels
          .filter((channel) => channel.status === CHANNEL_STATUS.ENABLED)
          .map((channel) => channel.id),
    }
  }, [channelsQuery.data, draftOverride, scheduleQuery.data?.data])

  const updateDraft = (
    update:
      | ChannelBenchmarkSchedule
      | ((current: ChannelBenchmarkSchedule) => ChannelBenchmarkSchedule)
  ) => {
    setDraftOverride((current) =>
      typeof update === 'function' ? update(current ?? draft) : update
    )
  }

  const saveMutation = useMutation({
    mutationFn: updateChannelBenchmarkSchedule,
    onSuccess: (response) => {
      if (!response.success || !response.data) {
        throw new Error(response.message || t('Failed to save schedule'))
      }
      setDraftOverride(response.data)
      queryClient.setQueryData(['channel-benchmark-schedule'], response)
      toast.success(t('Benchmark schedule saved'))
    },
    onError: (error) => {
      toast.error(
        error instanceof Error ? error.message : t('Failed to save schedule')
      )
    },
  })

  const runMutation = useMutation({
    mutationFn: () =>
      startChannelBenchmark({
        concurrency: draft.concurrency,
        timeout_seconds: draft.timeout_seconds,
        max_tokens: draft.max_tokens,
        prompt: draft.prompt.trim(),
        enable_thinking: draft.enable_thinking,
        channel_ids: draft.channel_ids ?? [],
      }),
    onSuccess: (response) => {
      if (!response.success) {
        throw new Error(response.message || t('Failed to start benchmark'))
      }
      toast.success(t('Benchmark started'))
    },
    onError: (error) => {
      toast.error(
        error instanceof Error ? error.message : t('Failed to start benchmark')
      )
    },
  })

  const data = trendsQuery.data?.data
  const allResults = useMemo(() => data?.results ?? [], [data?.results])
  const filteredResults = useMemo(
    () =>
      allResults.filter(
        (result) =>
          (selectedChannel === 'all' ||
            result.channel_id === Number(selectedChannel)) &&
          (selectedModel === 'all' || result.model === selectedModel)
      ),
    [allResults, selectedChannel, selectedModel]
  )
  const summary = useMemo(() => summarize(filteredResults), [filteredResults])
  const points = useMemo(
    () => buildTrendPoints(filteredResults),
    [filteredResults]
  )
  const entities = useMemo(
    () => buildEntityRollups(filteredResults),
    [filteredResults]
  )
  const failures = useMemo(
    () =>
      filteredResults
        .filter((result) => result.status === 'failed')
        .sort((a, b) => b.recorded_at - a.recorded_at)
        .slice(0, 10),
    [filteredResults]
  )
  const channelOptions = useMemo(() => {
    const map = new Map<number, string>()
    for (const result of allResults) {
      map.set(result.channel_id, result.channel_name)
    }
    return Array.from(map.entries()).sort((a, b) => a[1].localeCompare(b[1]))
  }, [allResults])
  const modelOptions = useMemo(
    () => Array.from(new Set(allResults.map((result) => result.model))).sort(),
    [allResults]
  )
  const selectedChannelIDs = useMemo(
    () => new Set(draft.channel_ids ?? []),
    [draft.channel_ids]
  )

  const toggleScheduledChannel = (channelID: number, checked: boolean) => {
    updateDraft((current) => {
      const next = new Set(current.channel_ids ?? [])
      if (checked) next.add(channelID)
      else next.delete(channelID)
      return { ...current, channel_ids: Array.from(next) }
    })
  }

  const isLoading =
    trendsQuery.isLoading || scheduleQuery.isLoading || channelsQuery.isLoading

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Benchmark trends')}</SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <Button
          variant='outline'
          disabled={trendsQuery.isFetching}
          onClick={() => void trendsQuery.refetch()}
        >
          {trendsQuery.isFetching ? (
            <Spinner data-icon='inline-start' />
          ) : (
            <HugeiconsIcon icon={RefreshIcon} data-icon='inline-start' />
          )}
          {t('Refresh')}
        </Button>
        <Button
          disabled={
            runMutation.isPending ||
            !draft.prompt.trim() ||
            (draft.channel_ids?.length ?? 0) === 0
          }
          onClick={() => runMutation.mutate()}
        >
          {runMutation.isPending ? (
            <Spinner data-icon='inline-start' />
          ) : (
            <HugeiconsIcon icon={PlayIcon} data-icon='inline-start' />
          )}
          {t('Run now')}
        </Button>
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
        <div className='mx-auto flex w-full max-w-[110rem] flex-col gap-4'>
          {!draft.enabled && !isLoading && (
            <Alert>
              <HugeiconsIcon icon={Clock01Icon} />
              <AlertTitle>{t('Automatic benchmarks are paused')}</AlertTitle>
              <AlertDescription>
                {t(
                  'Enable the schedule below to collect comparable performance samples throughout the day.'
                )}
              </AlertDescription>
            </Alert>
          )}

          <div className='flex flex-wrap items-end justify-between gap-3 rounded-lg border p-3'>
            <div className='flex min-w-0 flex-col gap-1'>
              <div className='flex items-center gap-2'>
                <HugeiconsIcon
                  icon={ChartLineData01Icon}
                  className='text-muted-foreground size-4'
                />
                <span className='text-sm font-semibold'>
                  {t('Observation window')}
                </span>
                <Badge variant={draft.enabled ? 'default' : 'secondary'}>
                  {draft.enabled ? t('Scheduled') : t('Paused')}
                </Badge>
              </div>
              <span className='text-muted-foreground text-xs'>
                {t('Last run')}: {formatTime(draft.last_run_at)}
                {' · '}
                {t('Next run')}: {formatTime(draft.next_run_at)}
              </span>
            </div>
            <div className='flex flex-wrap items-center gap-2'>
              <ToggleGroup
                value={[String(hours)]}
                onValueChange={(value) => {
                  const next = Number(value[0])
                  if (next) setHours(next)
                }}
                variant='outline'
                size='sm'
              >
                {[6, 12, 24, 72, 168].map((windowHours) => (
                  <ToggleGroupItem
                    key={windowHours}
                    value={String(windowHours)}
                  >
                    {windowHours < 24
                      ? `${windowHours}h`
                      : `${windowHours / 24}d`}
                  </ToggleGroupItem>
                ))}
              </ToggleGroup>
              <NativeSelect
                size='sm'
                aria-label={t('Filter by channel')}
                value={selectedChannel}
                onChange={(event) => setSelectedChannel(event.target.value)}
              >
                <NativeSelectOption value='all'>
                  {t('All channels')}
                </NativeSelectOption>
                {channelOptions.map(([id, name]) => (
                  <NativeSelectOption key={id} value={String(id)}>
                    {name}
                  </NativeSelectOption>
                ))}
              </NativeSelect>
              <NativeSelect
                size='sm'
                aria-label={t('Filter by model')}
                value={selectedModel}
                onChange={(event) => setSelectedModel(event.target.value)}
              >
                <NativeSelectOption value='all'>
                  {t('All models')}
                </NativeSelectOption>
                {modelOptions.map((model) => (
                  <NativeSelectOption key={model} value={model}>
                    {model}
                  </NativeSelectOption>
                ))}
              </NativeSelect>
            </div>
          </div>

          <div className='grid gap-3 sm:grid-cols-2 xl:grid-cols-5'>
            <MetricCard
              label={t('Average throughput')}
              value={formatThroughput(summary.average_tps)}
              detail={`${t('Median')} ${formatThroughput(summary.median_tps)} · P95 ${formatThroughput(summary.p95_tps)}`}
            />
            <MetricCard
              label={t('First-token delay')}
              value={formatLatency(summary.average_ttft_ms)}
              detail={`P95 ${formatLatency(summary.p95_ttft_ms)}`}
            />
            <MetricCard
              label={t('Total latency')}
              value={formatLatency(summary.average_latency_ms)}
              detail={`P95 ${formatLatency(summary.p95_latency_ms)}`}
            />
            <MetricCard
              label={t('Success rate')}
              value={formatUptimePct(summary.success_rate)}
              detail={`${summary.succeeded} ${t('succeeded')} · ${summary.failed} ${t('failed')}`}
            />
            <MetricCard
              label={t('Sample volume')}
              value={summary.samples.toLocaleString()}
              detail={`${summary.output_tokens.toLocaleString()} ${t('output tokens')}`}
            />
          </div>

          {isLoading ? (
            <Card>
              <CardContent className='flex min-h-80 items-center justify-center'>
                <Spinner />
              </CardContent>
            </Card>
          ) : (
            <div className='grid min-w-0 gap-4 xl:grid-cols-2'>
              {points.length === 0 ? (
                <Card>
                  <CardContent>
                    <Empty>
                      <EmptyHeader>
                        <EmptyMedia variant='icon'>
                          <HugeiconsIcon icon={ChartLineData01Icon} />
                        </EmptyMedia>
                        <EmptyTitle>
                          {t('No benchmark trend data yet')}
                        </EmptyTitle>
                        <EmptyDescription>
                          {t(
                            'Run a benchmark now or enable the schedule to begin building a performance timeline.'
                          )}
                        </EmptyDescription>
                      </EmptyHeader>
                    </Empty>
                  </CardContent>
                </Card>
              ) : (
                <>
                  <TrendChartCard
                    title={t('Throughput timeline')}
                    description={t(
                      'Average generated tokens per second for each benchmark run'
                    )}
                  >
                    <ChartContainer
                      config={chartConfig}
                      className='h-72 w-full'
                    >
                      <AreaChart data={points}>
                        <defs>
                          <linearGradient
                            id='benchmark-tps'
                            x1='0'
                            y1='0'
                            x2='0'
                            y2='1'
                          >
                            <stop
                              offset='5%'
                              stopColor='var(--color-tps)'
                              stopOpacity={0.38}
                            />
                            <stop
                              offset='95%'
                              stopColor='var(--color-tps)'
                              stopOpacity={0.02}
                            />
                          </linearGradient>
                        </defs>
                        <CartesianGrid vertical={false} strokeDasharray='3 3' />
                        <XAxis
                          dataKey='ts'
                          tickFormatter={formatChartTime}
                          tickLine={false}
                          axisLine={false}
                          minTickGap={28}
                        />
                        <YAxis
                          tickLine={false}
                          axisLine={false}
                          width={42}
                          tickFormatter={(value) => `${value}`}
                        />
                        <ChartTooltip
                          content={
                            <ChartTooltipContent
                              labelFormatter={(value) =>
                                formatTime(Number(value))
                              }
                            />
                          }
                        />
                        <Area
                          type='monotone'
                          dataKey='tps'
                          stroke='var(--color-tps)'
                          fill='url(#benchmark-tps)'
                          strokeWidth={2}
                          dot={{ r: 2 }}
                        />
                      </AreaChart>
                    </ChartContainer>
                  </TrendChartCard>

                  <TrendChartCard
                    title={t('Latency envelope')}
                    description={t(
                      'First-token delay compared with full request latency'
                    )}
                  >
                    <ChartContainer
                      config={chartConfig}
                      className='h-72 w-full'
                    >
                      <LineChart data={points}>
                        <CartesianGrid vertical={false} strokeDasharray='3 3' />
                        <XAxis
                          dataKey='ts'
                          tickFormatter={formatChartTime}
                          tickLine={false}
                          axisLine={false}
                          minTickGap={28}
                        />
                        <YAxis
                          tickLine={false}
                          axisLine={false}
                          width={52}
                          tickFormatter={(value) =>
                            value >= 1000
                              ? `${(value / 1000).toFixed(0)}s`
                              : value
                          }
                        />
                        <ChartTooltip
                          content={
                            <ChartTooltipContent
                              labelFormatter={(value) =>
                                formatTime(Number(value))
                              }
                            />
                          }
                        />
                        <Line
                          type='monotone'
                          dataKey='ttft'
                          stroke='var(--color-ttft)'
                          strokeWidth={2}
                          dot={{ r: 2 }}
                        />
                        <Line
                          type='monotone'
                          dataKey='latency'
                          stroke='var(--color-latency)'
                          strokeWidth={2}
                          dot={{ r: 2 }}
                        />
                      </LineChart>
                    </ChartContainer>
                  </TrendChartCard>

                  <TrendChartCard
                    title={t('Reliability and workload')}
                    description={t(
                      'Success percentage and generated output volume per run'
                    )}
                  >
                    <ChartContainer
                      config={chartConfig}
                      className='h-72 w-full'
                    >
                      <ComposedChart data={points}>
                        <CartesianGrid vertical={false} strokeDasharray='3 3' />
                        <XAxis
                          dataKey='ts'
                          tickFormatter={formatChartTime}
                          tickLine={false}
                          axisLine={false}
                          minTickGap={28}
                        />
                        <YAxis
                          yAxisId='rate'
                          domain={[0, 100]}
                          tickLine={false}
                          axisLine={false}
                          width={40}
                          tickFormatter={(value) => `${value}%`}
                        />
                        <YAxis
                          yAxisId='tokens'
                          orientation='right'
                          tickLine={false}
                          axisLine={false}
                          width={48}
                        />
                        <ChartTooltip
                          content={
                            <ChartTooltipContent
                              labelFormatter={(value) =>
                                formatTime(Number(value))
                              }
                            />
                          }
                        />
                        <Bar
                          yAxisId='tokens'
                          dataKey='outputTokens'
                          fill='var(--color-outputTokens)'
                          opacity={0.32}
                          radius={[4, 4, 0, 0]}
                        />
                        <Line
                          yAxisId='rate'
                          type='monotone'
                          dataKey='successRate'
                          stroke='var(--color-successRate)'
                          strokeWidth={2}
                          dot={{ r: 2 }}
                        />
                      </ComposedChart>
                    </ChartContainer>
                  </TrendChartCard>
                </>
              )}

              <Card className='min-w-0'>
                <CardHeader>
                  <CardTitle>{t('Schedule')}</CardTitle>
                  <CardDescription>
                    {t(
                      'Repeat the same benchmark recipe to make changes comparable.'
                    )}
                  </CardDescription>
                  <CardAction>
                    <Switch
                      aria-label={t('Enable automatic benchmarks')}
                      checked={draft.enabled}
                      onCheckedChange={(enabled) =>
                        updateDraft((current) => ({ ...current, enabled }))
                      }
                    />
                  </CardAction>
                </CardHeader>
                <CardContent>
                  <FieldGroup>
                    <div className='grid gap-3 sm:grid-cols-2 lg:grid-cols-5 xl:grid-cols-2 2xl:grid-cols-5'>
                      <Field>
                        <FieldLabel htmlFor='trend-interval'>
                          {t('Interval')}
                        </FieldLabel>
                        <Input
                          id='trend-interval'
                          type='number'
                          min={5}
                          max={1440}
                          value={draft.interval_minutes}
                          onChange={(event) =>
                            updateDraft((current) => ({
                              ...current,
                              interval_minutes: Number(event.target.value),
                            }))
                          }
                        />
                        <FieldDescription>{t('Minutes')}</FieldDescription>
                      </Field>
                      <Field>
                        <FieldLabel htmlFor='trend-retention'>
                          {t('Retention')}
                        </FieldLabel>
                        <Input
                          id='trend-retention'
                          type='number'
                          min={1}
                          max={365}
                          value={draft.retention_days}
                          onChange={(event) =>
                            updateDraft((current) => ({
                              ...current,
                              retention_days: Number(event.target.value),
                            }))
                          }
                        />
                        <FieldDescription>{t('Days')}</FieldDescription>
                      </Field>
                      <Field>
                        <FieldLabel htmlFor='trend-concurrency'>
                          {t('Concurrency')}
                        </FieldLabel>
                        <Input
                          id='trend-concurrency'
                          type='number'
                          min={1}
                          max={10}
                          value={draft.concurrency}
                          onChange={(event) =>
                            updateDraft((current) => ({
                              ...current,
                              concurrency: Number(event.target.value),
                            }))
                          }
                        />
                      </Field>
                      <Field>
                        <FieldLabel htmlFor='trend-timeout'>
                          {t('Timeout')}
                        </FieldLabel>
                        <Input
                          id='trend-timeout'
                          type='number'
                          min={10}
                          max={600}
                          value={draft.timeout_seconds}
                          onChange={(event) =>
                            updateDraft((current) => ({
                              ...current,
                              timeout_seconds: Number(event.target.value),
                            }))
                          }
                        />
                        <FieldDescription>{t('Seconds')}</FieldDescription>
                      </Field>
                      <Field>
                        <FieldLabel htmlFor='trend-output'>
                          {t('Output tokens')}
                        </FieldLabel>
                        <Input
                          id='trend-output'
                          type='number'
                          min={16}
                          max={2048}
                          value={draft.max_tokens}
                          onChange={(event) =>
                            updateDraft((current) => ({
                              ...current,
                              max_tokens: Number(event.target.value),
                            }))
                          }
                        />
                      </Field>
                    </div>

                    <Field orientation='horizontal'>
                      <FieldContent>
                        <FieldLabel htmlFor='trend-thinking'>
                          {t('Enable thinking')}
                        </FieldLabel>
                        <FieldDescription>
                          {t('Request high reasoning effort where supported')}
                        </FieldDescription>
                      </FieldContent>
                      <Switch
                        id='trend-thinking'
                        checked={draft.enable_thinking}
                        onCheckedChange={(enableThinking) =>
                          updateDraft((current) => ({
                            ...current,
                            enable_thinking: enableThinking,
                          }))
                        }
                      />
                    </Field>

                    <Field>
                      <FieldLabel htmlFor='trend-prompt'>
                        {t('Benchmark prompt')}
                      </FieldLabel>
                      <Textarea
                        id='trend-prompt'
                        rows={3}
                        maxLength={16000}
                        value={draft.prompt}
                        onChange={(event) =>
                          updateDraft((current) => ({
                            ...current,
                            prompt: event.target.value,
                          }))
                        }
                      />
                    </Field>

                    <Field>
                      <FieldLabel>{t('Scheduled channels')}</FieldLabel>
                      <FieldDescription>
                        {t('{{selected}} channels selected', {
                          selected: draft.channel_ids?.length ?? 0,
                        })}
                      </FieldDescription>
                      <ScrollArea className='h-36 rounded-lg border'>
                        <div className='grid gap-1 p-2 sm:grid-cols-2'>
                          {(channelsQuery.data ?? []).map((channel) => (
                            <label
                              key={channel.id}
                              className='hover:bg-muted flex cursor-pointer items-center gap-2 rounded-md px-2 py-1.5 text-xs'
                            >
                              <Checkbox
                                checked={selectedChannelIDs.has(channel.id)}
                                onCheckedChange={(checked) =>
                                  toggleScheduledChannel(
                                    channel.id,
                                    checked === true
                                  )
                                }
                              />
                              <span className='min-w-0 flex-1 truncate'>
                                {channel.name}
                              </span>
                              <Badge variant='outline'>
                                {channel.models?.split(',').filter(Boolean)
                                  .length ?? 0}
                              </Badge>
                            </label>
                          ))}
                        </div>
                      </ScrollArea>
                    </Field>

                    <Button
                      disabled={
                        saveMutation.isPending ||
                        !draft.prompt.trim() ||
                        (draft.channel_ids?.length ?? 0) === 0
                      }
                      onClick={() => saveMutation.mutate(draft)}
                    >
                      {saveMutation.isPending ? (
                        <Spinner data-icon='inline-start' />
                      ) : (
                        <HugeiconsIcon
                          icon={FloppyDiskIcon}
                          data-icon='inline-start'
                        />
                      )}
                      {t('Save schedule')}
                    </Button>
                  </FieldGroup>
                </CardContent>
              </Card>
            </div>
          )}

          <div className='grid min-w-0 gap-4 xl:grid-cols-[minmax(0,1.35fr)_minmax(20rem,0.65fr)]'>
            <Card className='min-w-0'>
              <CardHeader>
                <CardTitle>{t('Channel and model scorecard')}</CardTitle>
                <CardDescription>
                  {t(
                    'Averages from the selected observation window, ranked by throughput.'
                  )}
                </CardDescription>
              </CardHeader>
              <CardContent className='min-w-0 overflow-x-auto'>
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>{t('Channel')}</TableHead>
                      <TableHead>{t('Model')}</TableHead>
                      <TableHead className='text-right'>TPS</TableHead>
                      <TableHead className='text-right'>TTFT</TableHead>
                      <TableHead className='text-right'>
                        {t('Latency')}
                      </TableHead>
                      <TableHead className='text-right'>
                        {t('Success')}
                      </TableHead>
                      <TableHead className='text-right'>
                        {t('Samples')}
                      </TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {entities.slice(0, 50).map((entity) => (
                      <TableRow key={entity.key}>
                        <TableCell className='max-w-44 truncate font-medium'>
                          {entity.channel}
                        </TableCell>
                        <TableCell className='max-w-64 truncate font-mono text-xs'>
                          {entity.model}
                        </TableCell>
                        <TableCell className='text-right font-mono'>
                          {formatThroughput(entity.averageTps)}
                        </TableCell>
                        <TableCell className='text-right font-mono'>
                          {formatLatency(entity.averageTtft)}
                        </TableCell>
                        <TableCell className='text-right font-mono'>
                          {formatLatency(entity.averageLatency)}
                        </TableCell>
                        <TableCell className='text-right font-mono'>
                          {formatUptimePct(entity.successRate)}
                        </TableCell>
                        <TableCell className='text-right font-mono'>
                          {entity.samples}
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </CardContent>
            </Card>

            <Card className='min-w-0'>
              <CardHeader>
                <CardTitle>{t('Recent failures')}</CardTitle>
                <CardDescription>
                  {t('Latest failed model probes in this view')}
                </CardDescription>
              </CardHeader>
              <CardContent>
                {failures.length === 0 ? (
                  <div className='text-muted-foreground flex min-h-40 items-center justify-center text-sm'>
                    {t('No failures in this window')}
                  </div>
                ) : (
                  <ScrollArea className='h-[26rem]'>
                    <div className='flex flex-col gap-2 pr-3'>
                      {failures.map((failure) => (
                        <div
                          key={failure.id}
                          className='flex flex-col gap-1 rounded-lg border p-3'
                        >
                          <div className='flex items-center justify-between gap-2'>
                            <span className='truncate text-xs font-semibold'>
                              {failure.channel_name}
                            </span>
                            <Badge variant='destructive'>
                              {failure.error_code || t('Failed')}
                            </Badge>
                          </div>
                          <span className='truncate font-mono text-xs'>
                            {failure.model}
                          </span>
                          <p className='text-muted-foreground line-clamp-2 text-xs'>
                            {failure.error || t('Unknown upstream error')}
                          </p>
                          <span className='text-muted-foreground text-[11px]'>
                            {formatTime(failure.recorded_at)}
                          </span>
                        </div>
                      ))}
                    </div>
                  </ScrollArea>
                )}
              </CardContent>
            </Card>
          </div>

          <Card>
            <CardHeader>
              <CardTitle>{t('Run history')}</CardTitle>
              <CardDescription>
                {t('Manual and scheduled benchmark executions')}
              </CardDescription>
              <CardAction>
                <HugeiconsIcon
                  icon={Settings02Icon}
                  className='text-muted-foreground size-4'
                />
              </CardAction>
            </CardHeader>
            <CardContent className='overflow-x-auto'>
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>{t('Started')}</TableHead>
                    <TableHead>{t('Trigger')}</TableHead>
                    <TableHead>{t('Status')}</TableHead>
                    <TableHead className='text-right'>
                      {t('Succeeded')}
                    </TableHead>
                    <TableHead className='text-right'>{t('Failed')}</TableHead>
                    <TableHead className='text-right'>{t('Total')}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {(data?.runs ?? []).slice(0, 20).map((run) => (
                    <TableRow key={run.id}>
                      <TableCell>{formatTime(run.started_at, true)}</TableCell>
                      <TableCell>
                        <Badge variant='outline'>{t(run.trigger)}</Badge>
                      </TableCell>
                      <TableCell>
                        <Badge
                          variant={
                            run.status === 'completed'
                              ? 'secondary'
                              : run.status === 'interrupted'
                                ? 'destructive'
                                : 'outline'
                          }
                        >
                          {t(run.status)}
                        </Badge>
                      </TableCell>
                      <TableCell className='text-right font-mono'>
                        {run.succeeded}
                      </TableCell>
                      <TableCell className='text-right font-mono'>
                        {run.failed}
                      </TableCell>
                      <TableCell className='text-right font-mono'>
                        {run.total}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </CardContent>
          </Card>

          {data?.truncated && (
            <Alert>
              <AlertTitle>{t('Result limit reached')}</AlertTitle>
              <AlertDescription>
                {t(
                  'This view is showing the first 50,000 samples. Narrow the time window for complete detail.'
                )}
              </AlertDescription>
            </Alert>
          )}
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
