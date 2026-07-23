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
import { useCallback, useEffect, useMemo, useState } from 'react'
import { useNavigate } from '@tanstack/react-router'
import {
  ChartLineData01Icon,
  DashboardSpeed01Icon,
} from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { useTranslation } from 'react-i18next'
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
import {
  Progress,
  ProgressLabel,
  ProgressValue,
} from '@/components/ui/progress'
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
import { SectionPageLayout } from '@/components/layout'
import {
  cancelChannelBenchmark,
  getAllChannels,
  getChannelBenchmark,
  startChannelBenchmark,
} from './api'
import {
  CHANNEL_STATUS,
  CHANNEL_STATUS_LABELS,
  CHANNEL_TYPES,
} from './constants'
import { formatResponseTime } from './lib'
import type {
  Channel,
  ChannelBenchmarkConfig,
  ChannelBenchmarkJob,
  ChannelBenchmarkResultStatus,
} from './types'

const DEFAULT_CONFIG: ChannelBenchmarkConfig = {
  concurrency: 3,
  timeout_seconds: 120,
  max_tokens: 128,
  prompt:
    'Write a numbered list of concise, distinct facts. Continue until the response limit.',
  enable_thinking: true,
  channel_ids: [],
}

const statusTranslationKeys: Record<ChannelBenchmarkResultStatus, string> = {
  pending: 'Pending',
  running: 'Running',
  success: 'Succeeded',
  failed: 'Failed',
  cancelled: 'Cancelled',
}

function getStatusVariant(status: ChannelBenchmarkResultStatus) {
  switch (status) {
    case 'failed':
      return 'destructive' as const
    case 'running':
      return 'default' as const
    case 'success':
      return 'outline' as const
    default:
      return 'secondary' as const
  }
}

function getChannelTypeLabel(channel: Channel) {
  return (
    CHANNEL_TYPES[channel.type as keyof typeof CHANNEL_TYPES] ||
    `Type ${channel.type}`
  )
}

export function ChannelBenchmark() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const [config, setConfig] = useState<ChannelBenchmarkConfig>(DEFAULT_CONFIG)
  const [channels, setChannels] = useState<Channel[]>([])
  const [job, setJob] = useState<ChannelBenchmarkJob | null>(null)
  const [isStarting, setIsStarting] = useState(false)
  const [isCancelling, setIsCancelling] = useState(false)
  const [isLoadingChannels, setIsLoadingChannels] = useState(true)
  const [loadError, setLoadError] = useState('')
  const [channelLoadError, setChannelLoadError] = useState('')

  const isActive = job?.status === 'running' || job?.status === 'cancelling'
  const progress = job?.total
    ? Math.min(100, (job.completed / job.total) * 100)
    : 0
  const selectedChannelIDs = useMemo(
    () => new Set(config.channel_ids),
    [config.channel_ids]
  )

  const refreshJob = useCallback(async () => {
    const response = await getChannelBenchmark()
    if (!response.success) {
      throw new Error(response.message || 'Failed to load benchmark')
    }
    setJob(response.data || null)
    setLoadError('')
    return response.data || null
  }, [])

  useEffect(() => {
    let disposed = false
    void getAllChannels()
      .then((loadedChannels) => {
        if (disposed) return
        setChannels(loadedChannels)
        setConfig((current) => ({
          ...current,
          channel_ids: loadedChannels
            .filter((channel) => channel.status === CHANNEL_STATUS.ENABLED)
            .map((channel) => channel.id),
        }))
        setChannelLoadError('')
      })
      .catch((error) => {
        if (!disposed) {
          setChannelLoadError(
            error instanceof Error
              ? error.message
              : t('Failed to load channels')
          )
        }
      })
      .finally(() => {
        if (!disposed) setIsLoadingChannels(false)
      })

    return () => {
      disposed = true
    }
  }, [t])

  useEffect(() => {
    let disposed = false
    let timer: ReturnType<typeof setTimeout> | undefined
    const poll = async () => {
      try {
        const currentJob = await refreshJob()
        if (
          !disposed &&
          (currentJob?.status === 'running' ||
            currentJob?.status === 'cancelling')
        ) {
          timer = setTimeout(poll, 1000)
        }
      } catch (error) {
        if (!disposed) {
          setLoadError(
            error instanceof Error
              ? error.message
              : t('Failed to load benchmark')
          )
        }
      }
    }
    void poll()

    return () => {
      disposed = true
      if (timer) clearTimeout(timer)
    }
  }, [job?.status, refreshJob, t])

  const toggleChannel = (channelID: number, checked: boolean) => {
    setConfig((current) => {
      const selected = new Set(current.channel_ids)
      if (checked) {
        selected.add(channelID)
      } else {
        selected.delete(channelID)
      }
      return { ...current, channel_ids: Array.from(selected) }
    })
  }

  const selectEnabledChannels = () => {
    setConfig((current) => ({
      ...current,
      channel_ids: channels
        .filter((channel) => channel.status === CHANNEL_STATUS.ENABLED)
        .map((channel) => channel.id),
    }))
  }

  const handleStart = async () => {
    if (config.channel_ids.length === 0) {
      toast.error(t('Select at least one channel'))
      return
    }
    if (!config.prompt.trim()) {
      toast.error(t('Benchmark prompt must not be empty'))
      return
    }

    setIsStarting(true)
    try {
      const response = await startChannelBenchmark({
        ...config,
        prompt: config.prompt.trim(),
      })
      if (!response.success || !response.data) {
        throw new Error(response.message || t('Failed to start benchmark'))
      }
      setJob(response.data)
      setLoadError('')
      toast.success(t('Benchmark started'))
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : t('Failed to start benchmark')
      )
    } finally {
      setIsStarting(false)
    }
  }

  const handleCancel = async () => {
    setIsCancelling(true)
    try {
      const response = await cancelChannelBenchmark()
      if (!response.success) {
        throw new Error(response.message || t('Failed to cancel benchmark'))
      }
      if (response.data) setJob(response.data)
      toast.success(t('Benchmark cancellation requested'))
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : t('Failed to cancel benchmark')
      )
    } finally {
      setIsCancelling(false)
    }
  }

  const summary = useMemo(
    () => [
      { label: t('Completed'), value: job?.completed ?? 0 },
      { label: t('Succeeded'), value: job?.succeeded ?? 0 },
      { label: t('Failed'), value: job?.failed ?? 0 },
      { label: t('Cancelled'), value: job?.cancelled ?? 0 },
    ],
    [job, t]
  )

  const primaryAction = isActive ? (
    <Button
      variant='destructive'
      disabled={isCancelling || job?.status === 'cancelling'}
      onClick={handleCancel}
    >
      {isCancelling && <Spinner data-icon='inline-start' />}
      {t('Cancel Benchmark')}
    </Button>
  ) : (
    <Button
      disabled={
        isStarting ||
        isLoadingChannels ||
        config.channel_ids.length === 0 ||
        !config.prompt.trim()
      }
      onClick={handleStart}
    >
      {isStarting && <Spinner data-icon='inline-start' />}
      {t(job ? 'Run Benchmark Again' : 'Run Benchmark')}
    </Button>
  )

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>
        {t('Channel Model Benchmark')}
      </SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <Button
          variant='outline'
          onClick={() => navigate({ to: '/benchmarks/trends' })}
        >
          <HugeiconsIcon icon={ChartLineData01Icon} data-icon='inline-start' />
          {t('View trends')}
        </Button>
        {primaryAction}
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
        <div className='mx-auto grid w-full max-w-[100rem] min-w-0 gap-4 xl:grid-cols-[minmax(20rem,27rem)_minmax(0,1fr)]'>
          <div className='flex min-w-0 flex-col gap-4'>
            <Alert>
              <AlertTitle>{t('Upstream usage warning')}</AlertTitle>
              <AlertDescription>
                {t(
                  'Benchmark requests call upstream providers and may incur cost or consume quota.'
                )}
              </AlertDescription>
            </Alert>

            <Card>
              <CardHeader>
                <CardTitle>{t('Benchmark configuration')}</CardTitle>
                <CardDescription>
                  {t(
                    'Use the same prompt and generation settings across selected channels.'
                  )}
                </CardDescription>
              </CardHeader>
              <CardContent>
                <FieldGroup>
                  <div className='grid gap-4 sm:grid-cols-3 xl:grid-cols-1 2xl:grid-cols-3'>
                    <Field>
                      <FieldLabel htmlFor='benchmark-concurrency'>
                        {t('Concurrency')}
                      </FieldLabel>
                      <Input
                        id='benchmark-concurrency'
                        type='number'
                        min={1}
                        max={10}
                        disabled={isActive}
                        value={config.concurrency}
                        onChange={(event) =>
                          setConfig((current) => ({
                            ...current,
                            concurrency: Number(event.target.value),
                          }))
                        }
                      />
                      <FieldDescription>
                        {t('Parallel channels (1-10)')}
                      </FieldDescription>
                    </Field>
                    <Field>
                      <FieldLabel htmlFor='benchmark-timeout'>
                        {t('Timeout')}
                      </FieldLabel>
                      <Input
                        id='benchmark-timeout'
                        type='number'
                        min={10}
                        max={600}
                        disabled={isActive}
                        value={config.timeout_seconds}
                        onChange={(event) =>
                          setConfig((current) => ({
                            ...current,
                            timeout_seconds: Number(event.target.value),
                          }))
                        }
                      />
                      <FieldDescription>
                        {t('Seconds (10-600)')}
                      </FieldDescription>
                    </Field>
                    <Field>
                      <FieldLabel htmlFor='benchmark-max-tokens'>
                        {t('Output tokens')}
                      </FieldLabel>
                      <Input
                        id='benchmark-max-tokens'
                        type='number'
                        min={16}
                        max={2048}
                        disabled={isActive}
                        value={config.max_tokens}
                        onChange={(event) =>
                          setConfig((current) => ({
                            ...current,
                            max_tokens: Number(event.target.value),
                          }))
                        }
                      />
                      <FieldDescription>
                        {t('Tokens (16-2048)')}
                      </FieldDescription>
                    </Field>
                  </div>

                  <Field
                    orientation='horizontal'
                    className='rounded-lg border p-3'
                    data-disabled={isActive}
                  >
                    <FieldContent>
                      <FieldLabel htmlFor='benchmark-enable-thinking'>
                        {t('Enable thinking')}
                      </FieldLabel>
                      <FieldDescription>
                        {t('Request high reasoning effort where supported')}
                      </FieldDescription>
                    </FieldContent>
                    <Switch
                      id='benchmark-enable-thinking'
                      checked={config.enable_thinking}
                      disabled={isActive}
                      onCheckedChange={(checked) =>
                        setConfig((current) => ({
                          ...current,
                          enable_thinking: checked,
                        }))
                      }
                    />
                  </Field>

                  <Field>
                    <FieldLabel htmlFor='benchmark-prompt'>
                      {t('Benchmark prompt')}
                    </FieldLabel>
                    <Textarea
                      id='benchmark-prompt'
                      rows={5}
                      maxLength={16000}
                      disabled={isActive}
                      value={config.prompt}
                      onChange={(event) =>
                        setConfig((current) => ({
                          ...current,
                          prompt: event.target.value,
                        }))
                      }
                    />
                    <FieldDescription>
                      {t('Sent to every text generation model')}
                    </FieldDescription>
                  </Field>
                </FieldGroup>
              </CardContent>
            </Card>

            <Card className='min-w-0'>
              <CardHeader>
                <CardTitle>{t('Channels')}</CardTitle>
                <CardDescription>
                  {t('{{selected}} of {{total}} channels selected', {
                    selected: config.channel_ids.length,
                    total: channels.length,
                  })}
                </CardDescription>
                <CardAction className='flex gap-2'>
                  <Button
                    type='button'
                    size='xs'
                    variant='outline'
                    disabled={isActive || isLoadingChannels}
                    onClick={selectEnabledChannels}
                  >
                    {t('Select enabled')}
                  </Button>
                  <Button
                    type='button'
                    size='xs'
                    variant='ghost'
                    disabled={isActive || isLoadingChannels}
                    onClick={() =>
                      setConfig((current) => ({
                        ...current,
                        channel_ids: [],
                      }))
                    }
                  >
                    {t('Clear')}
                  </Button>
                </CardAction>
              </CardHeader>
              <CardContent className='min-w-0'>
                <ScrollArea className='h-[min(38dvh,24rem)] w-full max-w-full rounded-lg border'>
                  {isLoadingChannels ? (
                    <div className='flex h-full items-center justify-center'>
                      <Spinner />
                    </div>
                  ) : (
                    <FieldGroup className='gap-1 p-2'>
                      {channels.map((channel) => (
                        <Field
                          key={channel.id}
                          orientation='horizontal'
                          className='hover:bg-muted/50 rounded-md p-2'
                        >
                          <Checkbox
                            id={`benchmark-channel-${channel.id}`}
                            checked={selectedChannelIDs.has(channel.id)}
                            disabled={isActive}
                            onCheckedChange={(checked) =>
                              toggleChannel(channel.id, checked)
                            }
                          />
                          <FieldLabel
                            htmlFor={`benchmark-channel-${channel.id}`}
                            className='min-w-0 flex-1 cursor-pointer font-normal'
                          >
                            <span className='truncate'>
                              #{channel.id} · {channel.name}
                            </span>
                            <span className='text-muted-foreground shrink-0'>
                              {t(getChannelTypeLabel(channel))}
                            </span>
                          </FieldLabel>
                          <Badge
                            variant={
                              channel.status === CHANNEL_STATUS.ENABLED
                                ? 'outline'
                                : 'secondary'
                            }
                          >
                            {t(
                              CHANNEL_STATUS_LABELS[
                                channel.status as keyof typeof CHANNEL_STATUS_LABELS
                              ] || 'Unknown'
                            )}
                          </Badge>
                        </Field>
                      ))}
                    </FieldGroup>
                  )}
                </ScrollArea>
              </CardContent>
            </Card>
          </div>

          <Card className='min-w-0 self-start'>
            <CardHeader>
              <CardTitle>{t('Benchmark results')}</CardTitle>
              <CardDescription>
                {t(
                  'Total latency, first-token delay, and output throughput update while the benchmark runs.'
                )}
              </CardDescription>
              {job && (
                <CardAction>
                  <Badge variant='secondary'>
                    {job.completed}/{job.total}
                  </Badge>
                </CardAction>
              )}
            </CardHeader>
            <CardContent className='min-w-0'>
              {(loadError || channelLoadError) && (
                <Alert variant='destructive' className='mb-4'>
                  <AlertTitle>
                    {t(
                      channelLoadError
                        ? 'Failed to load channels'
                        : 'Failed to load benchmark'
                    )}
                  </AlertTitle>
                  <AlertDescription>
                    {channelLoadError || loadError}
                  </AlertDescription>
                </Alert>
              )}

              {job ? (
                <div className='flex min-w-0 flex-col gap-4'>
                  <Progress value={progress}>
                    <ProgressLabel>{t('Benchmark progress')}</ProgressLabel>
                    <ProgressValue>
                      {() =>
                        t('{{completed}} of {{total}} completed', {
                          completed: job.completed,
                          total: job.total,
                        })
                      }
                    </ProgressValue>
                  </Progress>

                  <div className='flex flex-wrap gap-2'>
                    {summary.map((item) => (
                      <Badge key={item.label} variant='secondary'>
                        {item.label}: {item.value}
                      </Badge>
                    ))}
                  </div>

                  <ScrollArea className='h-[min(65dvh,46rem)] w-full max-w-full min-w-0 overflow-hidden rounded-lg border'>
                    <Table className='min-w-[64rem]'>
                      <TableHeader className='bg-card sticky top-0'>
                        <TableRow>
                          <TableHead>{t('Channel')}</TableHead>
                          <TableHead>{t('Model')}</TableHead>
                          <TableHead>{t('Status')}</TableHead>
                          <TableHead>{t('Total latency')}</TableHead>
                          <TableHead>{t('TTFT')}</TableHead>
                          <TableHead>{t('Output tokens')}</TableHead>
                          <TableHead>{t('TPS')}</TableHead>
                        </TableRow>
                      </TableHeader>
                      <TableBody>
                        {job.results.map((result) => (
                          <TableRow
                            key={`${result.channel_id}:${result.model}`}
                          >
                            <TableCell>
                              <div className='flex max-w-52 flex-col gap-0.5'>
                                <span className='truncate font-medium'>
                                  {result.channel_name}
                                </span>
                                <span className='text-muted-foreground text-xs'>
                                  #{result.channel_id} ·{' '}
                                  {result.channel_type_name}
                                </span>
                              </div>
                            </TableCell>
                            <TableCell>
                              <span className='block max-w-64 truncate font-mono'>
                                {result.model}
                              </span>
                            </TableCell>
                            <TableCell>
                              <div className='flex max-w-64 flex-col gap-1'>
                                <Badge
                                  variant={getStatusVariant(result.status)}
                                >
                                  {t(statusTranslationKeys[result.status])}
                                </Badge>
                                {result.error && (
                                  <span
                                    className='text-destructive truncate text-xs'
                                    title={result.error}
                                  >
                                    {result.error}
                                  </span>
                                )}
                              </div>
                            </TableCell>
                            <TableCell>
                              {result.total_latency_ms > 0
                                ? formatResponseTime(result.total_latency_ms, t)
                                : '—'}
                            </TableCell>
                            <TableCell>
                              {result.ttft_ms !== undefined
                                ? formatResponseTime(result.ttft_ms, t)
                                : '—'}
                            </TableCell>
                            <TableCell>
                              {result.output_tokens > 0
                                ? result.output_tokens
                                : '—'}
                            </TableCell>
                            <TableCell>
                              {result.tps !== undefined
                                ? result.tps.toFixed(2)
                                : '—'}
                            </TableCell>
                          </TableRow>
                        ))}
                      </TableBody>
                    </Table>
                  </ScrollArea>
                </div>
              ) : (
                <Empty className='min-h-[32rem]'>
                  <EmptyHeader>
                    <EmptyMedia variant='icon'>
                      <HugeiconsIcon
                        icon={DashboardSpeed01Icon}
                        strokeWidth={2}
                      />
                    </EmptyMedia>
                    <EmptyTitle>{t('No benchmark results yet')}</EmptyTitle>
                    <EmptyDescription>
                      {t(
                        'Configure and run the benchmark to compare channel and model performance.'
                      )}
                    </EmptyDescription>
                  </EmptyHeader>
                </Empty>
              )}
            </CardContent>
          </Card>
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
