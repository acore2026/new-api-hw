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
import { DashboardSpeed01Icon } from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from '@/components/ui/empty'
import {
  Field,
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
import { ScrollArea, ScrollBar } from '@/components/ui/scroll-area'
import { Spinner } from '@/components/ui/spinner'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import {
  cancelChannelBenchmark,
  getChannelBenchmark,
  startChannelBenchmark,
} from '../../api'
import { formatResponseTime } from '../../lib'
import type {
  ChannelBenchmarkConfig,
  ChannelBenchmarkJob,
  ChannelBenchmarkResultStatus,
} from '../../types'

type ChannelBenchmarkDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
}

const DEFAULT_CONFIG: ChannelBenchmarkConfig = {
  concurrency: 3,
  timeout_seconds: 120,
  max_tokens: 128,
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

export function ChannelBenchmarkDialog({
  open,
  onOpenChange,
}: ChannelBenchmarkDialogProps) {
  const { t } = useTranslation()
  const [config, setConfig] = useState<ChannelBenchmarkConfig>(DEFAULT_CONFIG)
  const [job, setJob] = useState<ChannelBenchmarkJob | null>(null)
  const [isStarting, setIsStarting] = useState(false)
  const [isCancelling, setIsCancelling] = useState(false)
  const [loadError, setLoadError] = useState('')

  const isActive = job?.status === 'running' || job?.status === 'cancelling'
  const progress = job?.total
    ? Math.min(100, (job.completed / job.total) * 100)
    : 0

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
    if (!open) return

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
  }, [job?.status, open, refreshJob, t])

  const handleStart = async () => {
    setIsStarting(true)
    try {
      const response = await startChannelBenchmark(config)
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

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className='flex max-h-[calc(100dvh-2rem)] flex-col max-sm:h-dvh max-sm:w-screen max-sm:max-w-none max-sm:rounded-none max-sm:p-4 sm:max-w-6xl'>
        <DialogHeader>
          <DialogTitle>{t('Channel Model Benchmark')}</DialogTitle>
          <DialogDescription>
            {t(
              'Measure latency and token throughput for every configured model in every channel.'
            )}
          </DialogDescription>
        </DialogHeader>

        <Alert>
          <AlertTitle>{t('Upstream usage warning')}</AlertTitle>
          <AlertDescription>
            {t(
              'Benchmark requests call upstream providers and may incur cost or consume quota.'
            )}
          </AlertDescription>
        </Alert>

        <FieldGroup className='grid gap-4 sm:grid-cols-3'>
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
              {t('Maximum parallel channels (1-10)')}
            </FieldDescription>
          </Field>
          <Field>
            <FieldLabel htmlFor='benchmark-timeout'>
              {t('Timeout per model')}
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
            <FieldDescription>{t('Seconds (10-600)')}</FieldDescription>
          </Field>
          <Field>
            <FieldLabel htmlFor='benchmark-max-tokens'>
              {t('Maximum output tokens')}
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
            <FieldDescription>{t('Tokens (16-2048)')}</FieldDescription>
          </Field>
        </FieldGroup>

        {loadError && (
          <Alert variant='destructive'>
            <AlertTitle>{t('Failed to load benchmark')}</AlertTitle>
            <AlertDescription>{loadError}</AlertDescription>
          </Alert>
        )}

        {job ? (
          <div className='flex min-h-0 flex-1 flex-col gap-3'>
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

            <ScrollArea className='min-h-0 flex-1 rounded-lg border'>
              <Table>
                <TableHeader>
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
                    <TableRow key={`${result.channel_id}:${result.model}`}>
                      <TableCell>
                        <div className='flex max-w-52 flex-col gap-0.5'>
                          <span className='truncate font-medium'>
                            {result.channel_name}
                          </span>
                          <span className='text-muted-foreground text-xs'>
                            #{result.channel_id} · {result.channel_type_name}
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
                          <Badge variant={getStatusVariant(result.status)}>
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
                        {result.output_tokens > 0 ? result.output_tokens : '—'}
                      </TableCell>
                      <TableCell>
                        {result.tps !== undefined ? result.tps.toFixed(2) : '—'}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
              <ScrollBar orientation='horizontal' />
            </ScrollArea>
          </div>
        ) : (
          <Empty className='min-h-64'>
            <EmptyHeader>
              <EmptyMedia variant='icon'>
                <HugeiconsIcon icon={DashboardSpeed01Icon} strokeWidth={2} />
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

        <DialogFooter>
          <Button variant='outline' onClick={() => onOpenChange(false)}>
            {t('Close')}
          </Button>
          {isActive ? (
            <Button
              variant='destructive'
              disabled={isCancelling || job?.status === 'cancelling'}
              onClick={handleCancel}
            >
              {isCancelling && <Spinner data-icon='inline-start' />}
              {t('Cancel Benchmark')}
            </Button>
          ) : (
            <Button disabled={isStarting} onClick={handleStart}>
              {isStarting && <Spinner data-icon='inline-start' />}
              {t(job ? 'Run Benchmark Again' : 'Run Benchmark')}
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
