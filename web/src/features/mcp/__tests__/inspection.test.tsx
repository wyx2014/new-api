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

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { MCPInspectDialog } from '../components/mcp-inspect-dialog'
import { MCPServersTable } from '../components/mcp-servers-table'
import type { MCPInspectResult } from '../types'

const result = {
  connected: false,
  tools_status: 'not_requested',
  checked_at: '2026-11-14T12:00:00Z',
  duration_ms: 25,
  protocol_version: '',
  stage: 'initialize',
  tools: [],
  complete: false,
  error: {
    message: 'MCP initialization failed',
    http_status: 401,
    rpc_code: -32600,
  },
} satisfies MCPInspectResult

function renderDialog(mode: 'connection' | 'tools' = 'connection', id = 1) {
  const client = new QueryClient({
    defaultOptions: { mutations: { retry: false } },
  })
  const view = render(
    <QueryClientProvider client={client}>
      <MCPInspectDialog
        server={{ id, display_name: 'Saved server' }}
        mode={mode}
        onOpenChange={vi.fn()}
      />
    </QueryClientProvider>
  )
  return { ...view, client }
}

afterEach(() => vi.restoreAllMocks())

describe('saved MCP inspection', () => {
  it('auto-runs and preserves diagnosed failure details, then retries with cleared results', async () => {
    const post = vi
      .spyOn(api, 'post')
      .mockResolvedValueOnce({ data: { success: true, data: result } })
      .mockResolvedValueOnce({
        data: {
          success: true,
          data: {
            ...result,
            connected: true,
            stage: 'complete',
            error: undefined,
            protocol_version: '2025-03-26',
          },
        },
      })
    renderDialog()
    expect(await screen.findByText('MCP initialization failed')).toBeVisible()
    expect(screen.getByText('401')).toBeVisible()
    expect(screen.getByText('-32600')).toBeVisible()
    expect(post).toHaveBeenCalledWith(
      '/api/mcp/servers/1/inspect',
      { mode: 'connection' },
      expect.objectContaining({ signal: expect.any(AbortSignal) })
    )
    await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(await screen.findByText('2025-03-26')).toBeVisible()
    expect(
      screen.queryByText('MCP initialization failed')
    ).not.toBeInTheDocument()
  })
  it.each([
    ['unsupported', true, 'Server does not declare tools capability'],
    ['complete', true, 'No tools available'],
    ['failed', false, 'Tool discovery failed'],
    ['partial', false, 'Partial tool list'],
  ] as const)(
    'distinguishes %s tools from connection success',
    async (status, complete, label) => {
      vi.spyOn(api, 'post').mockResolvedValue({
        data: {
          success: true,
          data: {
            ...result,
            connected: true,
            stage: 'tools',
            tools_status: status,
            complete,
            error: undefined,
          },
        },
      })
      renderDialog('tools')
      expect(await screen.findByText(label)).toBeVisible()
      expect(screen.getByText('Connection successful')).toBeVisible()
      if (!complete) {
        expect(screen.getByText('Fetched tools: 0 (incomplete)')).toBeVisible()
      }
    }
  )

  it('expands untrusted tool metadata as text with its JSON schema and clears it on refresh', async () => {
    const post = vi
      .spyOn(api, 'post')
      .mockResolvedValueOnce({
        data: {
          success: true,
          data: {
            ...result,
            connected: true,
            tools_status: 'partial',
            complete: false,
            tools: [
              {
                name: '<b>lookup</b>',
                description: '<script>secret()</script>',
                inputSchema: { type: 'object' },
              },
            ],
          },
        },
      })
      .mockImplementationOnce(() => new Promise(() => {}))
    renderDialog('tools')
    const tool = await screen.findByRole('button', { name: '<b>lookup</b>' })
    await userEvent.click(tool)
    expect(tool).toHaveAttribute('aria-expanded', 'true')
    expect(await screen.findByText('<script>secret()</script>')).toBeVisible()
    expect(screen.getByText('Input schema')).toBeVisible()
    expect(screen.getByText(/"object"/)).toBeVisible()
    await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(screen.queryByText('<b>lookup</b>')).not.toBeInTheDocument()
    expect(await screen.findByText('Checking MCP server...')).toBeVisible()
    expect(post).toHaveBeenLastCalledWith(
      '/api/mcp/servers/1/inspect',
      { mode: 'tools' },
      expect.objectContaining({ signal: expect.any(AbortSignal) })
    )
  })

  it('cancels pending work and ignores a late response before retrying', async () => {
    let resolve!: (value: unknown) => void
    const post = vi
      .spyOn(api, 'post')
      .mockImplementationOnce(
        () =>
          new Promise((done) => {
            resolve = done
          })
      )
      .mockResolvedValueOnce({
        data: {
          success: true,
          data: { ...result, connected: true, error: undefined },
        },
      })
    renderDialog()
    await screen.findByText('Checking MCP server...')
    const signal = post.mock.calls[0][2]?.signal
    await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(signal?.aborted).toBe(true)
    await act(async () => resolve({ data: { success: true, data: result } }))
    expect(
      screen.queryByText('MCP initialization failed')
    ).not.toBeInTheDocument()
    expect(screen.getByText('Inspection cancelled')).toBeVisible()
    await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(await screen.findByText('Connection successful')).toBeVisible()
  })

  it('aborts and clears previous results when switching server/mode and when closing', async () => {
    const post = vi
      .spyOn(api, 'post')
      .mockResolvedValueOnce({ data: { success: true, data: result } })
      .mockImplementation(() => new Promise(() => {}))
    const view = renderDialog()
    await screen.findByText('MCP initialization failed')
    const previousSignal = post.mock.calls[0][2]?.signal
    view.rerender(
      <QueryClientProvider client={view.client}>
        <MCPInspectDialog
          server={{ id: 2, display_name: 'Other server' }}
          mode='tools'
          onOpenChange={vi.fn()}
        />
      </QueryClientProvider>
    )
    expect(previousSignal?.aborted).toBe(true)
    expect(
      screen.queryByText('MCP initialization failed')
    ).not.toBeInTheDocument()
    await screen.findByText('Checking MCP server...')
    expect(post).toHaveBeenLastCalledWith(
      '/api/mcp/servers/2/inspect',
      { mode: 'tools' },
      expect.objectContaining({ signal: expect.any(AbortSignal) })
    )
    await userEvent.keyboard('{Escape}')
    expect(post.mock.calls[1][2]?.signal?.aborted).toBe(true)
    view.unmount()
  })

  it('shows envelope request errors inline without treating them as diagnostic results', async () => {
    vi.spyOn(api, 'post').mockResolvedValue({
      data: { success: false, message: 'Invalid inspection mode' },
    })
    renderDialog()
    expect(await screen.findByText('Invalid inspection mode')).toBeVisible()
    expect(screen.queryByText('Checked at')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Retry' })).toBeEnabled()
  })

  it.each(['Test connection', 'View tools'])(
    'runs %s from a saved row only',
    async (action) => {
      vi.spyOn(api, 'get').mockResolvedValue({
        data: {
          success: true,
          data: {
            items: [
              {
                id: 7,
                name: 'saved',
                display_name: 'Saved server',
                base_url: 'https://example.test/mcp',
                protocol_type: 'sse',
                timeout: 60,
                quota_per_call: 0,
                status: 2,
              },
            ],
            total: 1,
          },
        },
      })
      const post = vi
        .spyOn(api, 'post')
        .mockResolvedValue({ data: { success: true, data: result } })
      render(
        <QueryClientProvider client={new QueryClient()}>
          <MCPServersTable />
        </QueryClientProvider>
      )
      await userEvent.click(
        await screen.findByRole('button', { name: 'Actions for Saved server' })
      )
      await userEvent.click(
        await screen.findByRole('menuitem', { name: action })
      )
      expect(await screen.findByRole('dialog', { name: action })).toBeVisible()
      expect(
        screen.getByText(
          'Checks use saved configuration. Save new or edited configuration before checking.'
        )
      ).toBeVisible()
      expect(post).toHaveBeenCalledWith(
        '/api/mcp/servers/7/inspect',
        { mode: action === 'View tools' ? 'tools' : 'connection' },
        expect.objectContaining({ signal: expect.any(AbortSignal) })
      )
    }
  )
})
