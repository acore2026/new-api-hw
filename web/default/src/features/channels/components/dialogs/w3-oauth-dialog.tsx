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
import { useEffect, useState } from 'react'
import { Check, Copy, ExternalLink, Loader2, RefreshCcw } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { tryPrettyJson } from '@/lib/utils'
import { useCopyToClipboard } from '@/hooks/use-copy-to-clipboard'
import { Alert, AlertDescription } from '@/components/ui/alert'
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
  completeW3OAuth,
  startW3OAuth,
  type W3OAuthSettingsPayload,
} from '../../api'

const W3_COMPLETE_TIMEOUT_MS = 5 * 60 * 1000
const W3_COMPLETE_RETRY_DELAY_MS = 1000

type W3OAuthDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  channelId?: number | null
  settings?: W3OAuthSettingsPayload
  onKeyGenerated?: (key: string) => void
  onSaved?: () => void
}

function wait(ms: number) {
  return new Promise((resolve) => window.setTimeout(resolve, ms))
}

export function W3OAuthDialog({
  open,
  onOpenChange,
  channelId,
  settings,
  onKeyGenerated,
  onSaved,
}: W3OAuthDialogProps) {
  const { t } = useTranslation()
  const { copiedText, copyToClipboard } = useCopyToClipboard({ notify: false })
  const [state, setState] = useState({
    authorizeUrl: '',
    isStarting: false,
    isCompleting: false,
  })

  useEffect(() => {
    if (!open) {
      setState({
        authorizeUrl: '',
        isStarting: false,
        isCompleting: false,
      })
    }
  }, [open])

  const openAuthorizationUrl = (url: string) => {
    try {
      window.open(url, '_blank', 'noopener,noreferrer')
      toast.success(t('Opened authorization page'))
    } catch (error) {
      // eslint-disable-next-line no-console
      console.warn('Failed to open W3 authorization page:', error)
      toast.warning(t('Please manually copy and open the authorization link'))
    }
  }

  const handleStart = async () => {
    setState((prev) => ({ ...prev, isStarting: true }))
    try {
      const res = await startW3OAuth(settings, channelId || undefined)
      if (!res.success) {
        throw new Error(res.message || 'Failed to start W3 OAuth')
      }
      const url = res.data?.authorize_url || ''
      if (!url) {
        throw new Error('Missing authorize_url in response')
      }
      setState((prev) => ({ ...prev, authorizeUrl: url }))
      openAuthorizationUrl(url)
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : t('OAuth start failed')
      )
    } finally {
      setState((prev) => ({ ...prev, isStarting: false }))
    }
  }

  const handleOpenAuthorization = async () => {
    if (state.authorizeUrl) {
      openAuthorizationUrl(state.authorizeUrl)
      return
    }
    await handleStart()
  }

  const handleComplete = async () => {
    setState((prev) => ({ ...prev, isCompleting: true }))
    try {
      const deadline = Date.now() + W3_COMPLETE_TIMEOUT_MS

      while (true) {
        const res = await completeW3OAuth(channelId || undefined)
        if (!res.success) {
          const isPending =
            res.data?.pending === true ||
            res.message?.toLowerCase().includes('token is not ready')
          if (!isPending || Date.now() >= deadline) {
            throw new Error(res.message || 'OAuth failed')
          }
          await wait(W3_COMPLETE_RETRY_DELAY_MS)
          continue
        }

        const rawKey = res.data?.key || ''
        if (rawKey) {
          onKeyGenerated?.(tryPrettyJson(rawKey))
        } else {
          onSaved?.()
        }
        toast.success(
          rawKey ? t('Credential generated') : t('Credential saved')
        )
        onOpenChange(false)
        return
      }
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t('OAuth failed'))
    } finally {
      setState((prev) => ({ ...prev, isCompleting: false }))
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className='sm:max-w-2xl'>
        <DialogHeader>
          <DialogTitle>{t('Huawei W3 Authorization')}</DialogTitle>
          <DialogDescription>
            {t('Generate a W3 OAuth credential for this MiniMax channel.')}
          </DialogDescription>
        </DialogHeader>

        <div className='space-y-4'>
          <Alert>
            <AlertDescription>
              {t(
                'Click "Open authorization page", complete Huawei SSO login, then return here and click "Complete authorization".'
              )}
            </AlertDescription>
          </Alert>

          <div className='flex flex-wrap gap-2'>
            <Button
              onClick={handleOpenAuthorization}
              disabled={state.isStarting || state.isCompleting}
            >
              {state.isStarting ? (
                <Loader2 className='mr-2 h-4 w-4 animate-spin' />
              ) : (
                <ExternalLink className='mr-2 h-4 w-4' />
              )}
              {state.authorizeUrl
                ? t('Reopen authorization page')
                : t('Open authorization page')}
            </Button>

            <Button
              type='button'
              variant='outline'
              disabled={
                !state.authorizeUrl || state.isStarting || state.isCompleting
              }
              onClick={async () => {
                if (!state.authorizeUrl) return
                await copyToClipboard(state.authorizeUrl)
              }}
              aria-label={t('Copy authorization link')}
              title={t('Copy authorization link')}
            >
              {copiedText === state.authorizeUrl ? (
                <Check className='mr-2 h-4 w-4 text-green-600' />
              ) : (
                <Copy className='mr-2 h-4 w-4' />
              )}
              {t('Copy authorization link')}
            </Button>

            {state.authorizeUrl && (
              <Button
                type='button'
                variant='outline'
                disabled={state.isStarting || state.isCompleting}
                onClick={handleStart}
              >
                <RefreshCcw className='mr-2 h-4 w-4' />
                {t('Restart authorization')}
              </Button>
            )}
          </div>
        </div>

        <DialogFooter>
          <Button
            type='button'
            variant='outline'
            onClick={() => onOpenChange(false)}
            disabled={state.isStarting || state.isCompleting}
          >
            {t('Cancel')}
          </Button>
          <Button
            onClick={handleComplete}
            disabled={!state.authorizeUrl || state.isCompleting}
          >
            {state.isCompleting && (
              <Loader2 className='mr-2 h-4 w-4 animate-spin' />
            )}
            {state.isCompleting
              ? t('Completing...')
              : t('Complete authorization')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
