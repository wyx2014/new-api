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

import { useMutation } from '@tanstack/react-query'
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { CodeBlock } from '@/components/ai-elements/code-block'
import { Dialog } from '@/components/dialog'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import {
  Accordion,
  AccordionContent,
  AccordionItem,
  AccordionTrigger,
} from '@/components/ui/accordion'
import { Button } from '@/components/ui/button'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'
import {
  getServerErrorMessage,
  isServerErrorCancelled,
} from '@/lib/server-error-message'

import { inspectMCPServer } from '../api'
import type { MCPInspectMode, MCPServer } from '../types'

type Props = {
  server: Pick<MCPServer, 'id' | 'display_name'> | null
  mode: MCPInspectMode
  onOpenChange: (open: boolean) => void
}

export function MCPInspectDialog(props: Props) {
  // Remount the session on service/mode changes so even the first render cannot show old results.
  if (!props.server) return null
  return (
    <InspectSession
      key={`${props.server.id}:${props.mode}`}
      {...props}
      server={props.server}
    />
  )
}

function InspectSession(
  props: Props & { server: NonNullable<Props['server']> }
) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const controller = useRef<AbortController | null>(null)
  const [cancelled, setCancelled] = useState(false)
  const mutation = useMutation({
    mutationFn: async (signal: AbortSignal) => {
      const result = await inspectMCPServer(props.server.id, props.mode, signal)
      signal.throwIfAborted()
      return result
    },
    retry: false,
    meta: { errorToast: false },
    onError: () => {}, // Request failures are rendered inline; cancellation is silent.
  })
  const { mutate } = mutation
  useEffect(() => {
    const request = new AbortController()
    controller.current = request
    mutate(request.signal)
    return () => request.abort()
  }, [mutate])

  function run() {
    if (
      controller.current &&
      !controller.current.signal.aborted &&
      mutation.isPending
    ) {
      return
    }
    controller.current?.abort()
    mutation.reset()
    setCancelled(false)
    const request = new AbortController()
    controller.current = request
    mutate(request.signal)
  }

  const result = mutation.data
  const failed =
    mutation.isError ||
    result?.connected === false ||
    result?.tools_status === 'failed' ||
    result?.tools_status === 'partial'
  const stageLabels = {
    connection: t('Connection'),
    initialize: t('Initialization'),
    tools: t('Tools'),
    complete: t('Complete'),
  }
  const toolLabels = {
    not_requested: t('Tools not requested'),
    unsupported: t('Server does not declare tools capability'),
    complete: t('Tool discovery complete'),
    failed: t('Tool discovery failed'),
    partial: t('Partial tool list'),
  }

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) controller.current?.abort()
        props.onOpenChange(open)
      }}
      title={
        props.mode === 'connection' ? t('Test connection') : t('View tools')
      }
      description={t(
        'Checks use saved configuration. Save new or edited configuration before checking.'
      )}
      footer={
        mutation.isPending && !cancelled ? (
          <Button
            variant='outline'
            onClick={() => {
              controller.current?.abort()
              mutation.reset()
              setCancelled(true)
            }}
          >
            {t('Cancel')}
          </Button>
        ) : (
          <Button onClick={run}>
            {failed || cancelled ? t('Retry') : t('Refresh')}
          </Button>
        )
      }
    >
      <div className='space-y-4 break-words' aria-live='polite'>
        <p className='font-medium'>{props.server.display_name}</p>
        <p className='text-muted-foreground text-sm'>
          {t(
            'Inspection only initializes the connection and discovers tools. It does not execute tools or deduct gateway quota. Upstream providers may charge for these requests.'
          )}
        </p>
        {mutation.isPending && !cancelled && (
          <LoadingState message={t('Checking MCP server...')} />
        )}
        {cancelled && <p role='status'>{t('Inspection cancelled')}</p>}
        {mutation.isError && !isServerErrorCancelled(mutation.error) && (
          <ErrorState
            title={t('MCP inspection failed')}
            description={getServerErrorMessage(
              mutation.error,
              t('MCP inspection failed')
            )}
          />
        )}
        {result && (
          <>
            <p role='status'>
              {result.connected
                ? t('Connection successful')
                : t('Connection failed')}
            </p>
            <dl className='grid grid-cols-2 gap-2 text-sm'>
              <dt>{t('Checked at')}</dt>
              <dd>{new Date(result.checked_at).toLocaleString(locale)}</dd>
              <dt>{t('Duration (ms)')}</dt>
              <dd>{formatNumber(result.duration_ms, locale)}</dd>
              <dt>{t('Protocol version')}</dt>
              <dd>{result.protocol_version || t('Not available')}</dd>
              <dt>{t('Stage')}</dt>
              <dd>{stageLabels[result.stage]}</dd>
              {result.error?.http_status !== undefined && (
                <>
                  <dt>{t('HTTP status')}</dt>
                  <dd>{result.error.http_status}</dd>
                </>
              )}
              {result.error?.rpc_code !== undefined && (
                <>
                  <dt>{t('JSON-RPC code')}</dt>
                  <dd>{result.error.rpc_code}</dd>
                </>
              )}
            </dl>
            {result.error && (
              <p role='alert' className='text-destructive'>
                {t(result.error.message, { skipInterpolation: true })}
              </p>
            )}
            {props.mode === 'tools' && (
              <>
                <p>{toolLabels[result.tools_status]}</p>
                <p>
                  {result.complete
                    ? t('Total tools: {{count}}', {
                        count: result.tools.length,
                      })
                    : t('Fetched tools: {{count}} (incomplete)', {
                        count: result.tools.length,
                      })}
                </p>
                {result.tools_status === 'complete' &&
                  result.complete &&
                  result.tools.length === 0 && (
                    <EmptyState title={t('No tools available')} />
                  )}
                <Accordion>
                  {result.tools.map((tool, index) => (
                    <AccordionItem key={tool.name} value={String(index)}>
                      <AccordionTrigger>{tool.name}</AccordionTrigger>
                      <AccordionContent>
                        {tool.description && (
                          <p className='whitespace-pre-wrap'>
                            {tool.description}
                          </p>
                        )}
                        <p className='text-muted-foreground text-sm'>
                          {t('Input schema')}
                        </p>
                        <CodeBlock
                          code={JSON.stringify(tool.inputSchema, null, 2)}
                          language='json'
                        />
                      </AccordionContent>
                    </AccordionItem>
                  ))}
                </Accordion>
              </>
            )}
          </>
        )}
      </div>
    </Dialog>
  )
}
