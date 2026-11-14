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
import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useEffect } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { z } from 'zod'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { handleServerError } from '@/lib/handle-server-error'

import { createMCPServer, updateMCPServer } from '../api'
import {
  buildMCPHeaders,
  parseMCPHeaders,
  MCP_DEFAULT_TIMEOUT,
  MCP_MAX_TIMEOUT,
} from '../headers'
import type { MCPServer } from '../types'
import { MCPHeadersEditor } from './mcp-headers-editor'

const mcpServerSchema = z.object({
  name: z
    .string()
    .min(1, 'Name is required')
    .regex(
      /^[a-zA-Z0-9_-]+$/,
      'Identifier must contain only letters, numbers, hyphens, and underscores'
    ),
  display_name: z.string().min(1, 'Display name is required'),
  base_url: z
    .string()
    .min(1, 'Base URL is required')
    .url('Must be a valid URL (e.g. http://192.168.1.100:8000)'),
  quota_per_call: z.coerce
    .number()
    .min(0, 'Quota per call must be 0 or greater'),
  description: z.string().optional().default(''),
  status: z.number().default(1),
  protocol_type: z.enum(['sse', 'streamableHttp']),
  timeout: z.coerce.number().int().min(1).max(MCP_MAX_TIMEOUT),
  headers_input: z.string().refine((value) => {
    try {
      parseMCPHeaders(value)
      return true
    } catch {
      return false
    }
  }, 'Invalid MCP headers'),
  retained_headers: z.array(z.string()),
})

type MCPServerFormValues = z.infer<typeof mcpServerSchema>

type MCPServerDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  server?: MCPServer | null
}

export function MCPServerDialog({
  open,
  onOpenChange,
  server,
}: MCPServerDialogProps) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const isEditing = !!server

  const form = useForm<
    z.input<typeof mcpServerSchema>,
    unknown,
    MCPServerFormValues
  >({
    mode: 'onChange',
    resolver: zodResolver(mcpServerSchema),
    defaultValues: {
      name: '',
      display_name: '',
      base_url: '',
      quota_per_call: 0,
      description: '',
      status: 1,
      protocol_type: 'sse',
      timeout: MCP_DEFAULT_TIMEOUT,
      headers_input: '',
      retained_headers: [],
    },
  })

  useEffect(() => {
    if (server) {
      form.reset({
        name: server.name,
        display_name: server.display_name,
        base_url: server.base_url,
        quota_per_call: server.quota_per_call ?? 0,
        description: server.description ?? '',
        status: server.status,
        protocol_type: server.protocol_type,
        timeout: server.timeout,
        headers_input: '',
        retained_headers: Object.keys(
          JSON.parse(server.headers) as Record<string, null>
        ),
      })
    } else {
      form.reset({
        name: '',
        display_name: '',
        base_url: '',
        quota_per_call: 0,
        description: '',
        status: 1,
        protocol_type: 'sse',
        timeout: MCP_DEFAULT_TIMEOUT,
        headers_input: '',
        retained_headers: [],
      })
    }
  }, [server, form, open])

  const mutation = useMutation({
    mutationFn: async (values: MCPServerFormValues) => {
      const { headers_input, retained_headers, ...fields } = values
      const payload = {
        ...fields,
        headers: buildMCPHeaders(retained_headers, headers_input),
      }
      if (isEditing && server) {
        const { base_url, ...updates } = payload
        return updateMCPServer({
          ...updates,
          id: server.id,
          ...(base_url === server.base_url ? {} : { base_url }),
        })
      }
      return createMCPServer(payload)
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['mcp-servers'] })
      toast.success(
        isEditing
          ? t('MCP server updated successfully')
          : t('MCP server created successfully')
      )
      onOpenChange(false)
    },
    onError: (err) => {
      handleServerError(err)
    },
  })

  const onSubmit = (values: MCPServerFormValues) => {
    mutation.mutate(values)
  }

  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      title={isEditing ? t('Edit MCP Server') : t('Add MCP Server')}
      description={t(
        'Register or configure an upstream enterprise MCP service (SSE / HTTP)'
      )}
      contentClassName='max-w-lg'
    >
      <p className='text-muted-foreground text-sm'>
        {t(
          'Checks use saved configuration. Save new or edited configuration before checking.'
        )}
      </p>
      <Form {...form}>
        <form onSubmit={form.handleSubmit(onSubmit)} className='space-y-4'>
          <FormField
            control={form.control}
            name='name'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Identifier Name')}</FormLabel>
                <FormControl>
                  <Input
                    {...field}
                    placeholder='e.g. internal_kb'
                    disabled={isEditing}
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'Unique identifier used in gateway URL /modelgw/mcp/:name'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name='display_name'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Display Name')}</FormLabel>
                <FormControl>
                  <Input
                    {...field}
                    placeholder='e.g. Internal Knowledge Base'
                  />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name='base_url'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Upstream Base URL')}</FormLabel>
                <FormControl>
                  <Input {...field} placeholder='http://192.168.1.100:8000' />
                </FormControl>
                <FormDescription>
                  {t('Full upstream MCP endpoint; no /sse suffix is added.')}
                  {server?.has_url_query && (
                    <span>
                      {t(
                        'Saved URL query is hidden and retained when the URL is unchanged.'
                      )}
                    </span>
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name='protocol_type'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('MCP protocol')}</FormLabel>
                <FormControl>
                  <NativeSelect {...field}>
                    <NativeSelectOption value='sse'>SSE</NativeSelectOption>
                    <NativeSelectOption value='streamableHttp'>
                      Streamable HTTP
                    </NativeSelectOption>
                  </NativeSelect>
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name='timeout'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Response header timeout (seconds)')}</FormLabel>
                <FormControl>
                  <Input
                    {...field}
                    value={field.value as number}
                    type='number'
                    min={1}
                    max={MCP_MAX_TIMEOUT}
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'Limits waiting for upstream headers, not the lifetime of an active stream.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name='headers_input'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Upstream headers')}</FormLabel>
                <FormControl>
                  <MCPHeadersEditor
                    {...field}
                    retained={form.watch('retained_headers')}
                    onRetainedChange={(names) =>
                      form.setValue('retained_headers', names, {
                        shouldDirty: true,
                      })
                    }
                  />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name='quota_per_call'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Quota Deducted Per Call')}</FormLabel>
                <FormControl>
                  <Input
                    type='number'
                    min={0}
                    {...field}
                    value={field.value as number}
                    placeholder='0'
                  />
                </FormControl>
                <FormDescription>
                  {t('Quota deducted on each tool execution (0 for free)')}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name='description'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Description')}</FormLabel>
                <FormControl>
                  <Textarea
                    {...field}
                    placeholder={t(
                      'Brief description of what tools this MCP server provides'
                    )}
                    rows={2}
                  />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name='status'
            render={({ field }) => (
              <FormItem className='flex items-center justify-between rounded-lg border p-3'>
                <div className='space-y-0.5'>
                  <FormLabel>{t('Enabled')}</FormLabel>
                  <FormDescription>
                    {t(
                      'Whether this MCP server is available for incoming traffic'
                    )}
                  </FormDescription>
                </div>
                <FormControl>
                  <Switch
                    checked={field.value === 1}
                    onCheckedChange={(checked) =>
                      field.onChange(checked ? 1 : 2)
                    }
                  />
                </FormControl>
              </FormItem>
            )}
          />

          <div className='flex justify-end gap-2 pt-2'>
            <Button
              type='button'
              variant='outline'
              onClick={() => onOpenChange(false)}
            >
              {t('Cancel')}
            </Button>
            <Button type='submit' disabled={mutation.isPending}>
              {mutation.isPending ? t('Saving...') : t('Save')}
            </Button>
          </div>
        </form>
      </Form>
    </Dialog>
  )
}
