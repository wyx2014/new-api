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
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import { updateMCPServer } from '../api'
import type { MCPServer } from '../types'
import { MCPServerDialog } from './mcp-server-dialog'

vi.mock('../api', () => ({
  createMCPServer: vi.fn(),
  updateMCPServer: vi.fn().mockResolvedValue({}),
}))

const saved: MCPServer = {
  id: 1,
  name: 'test',
  display_name: 'Test',
  description: '',
  base_url: 'https://example.com/mcp',
  has_url_query: true,
  headers: '{"Authorization":null}',
  protocol_type: 'streamableHttp',
  timeout: 120,
  quota_per_call: 0,
  status: 1,
  created_at: 0,
  updated_at: 0,
}

describe('MCP server editing', () => {
  it.each(['preserve', 'replace', 'delete'])(
    'saves explicit %s without retrieving a secret',
    async (operation) => {
      const user = userEvent.setup()
      render(
        <QueryClientProvider
          client={
            new QueryClient({ defaultOptions: { queries: { retry: false } } })
          }
        >
          <MCPServerDialog open onOpenChange={vi.fn()} server={saved} />
        </QueryClientProvider>
      )
      const input = await screen.findByLabelText('Upstream headers')
      expect(input).toHaveValue('')
      expect(
        screen.getByText('Authorization: Saved value hidden')
      ).toBeInTheDocument()
      await user.click(
        screen.getByRole('button', { name: 'Toggle new header visibility' })
      )
      expect(input).toHaveValue('')
      if (operation === 'replace') {
        await user.type(input, 'Authorization: literal****secret')
      }
      if (operation === 'delete') {
        await user.click(
          screen.getByRole('button', {
            name: 'Remove saved header Authorization',
          })
        )
      }
      await user.click(screen.getByRole('button', { name: /^Save$/ }))
      await waitFor(() => expect(updateMCPServer).toHaveBeenCalledTimes(1))
      const payload = vi.mocked(updateMCPServer).mock.calls[0][0]
      expect(payload).not.toHaveProperty('base_url')
      expect(payload).toMatchObject({
        protocol_type: 'streamableHttp',
        timeout: 120,
      })
      expect(JSON.parse(payload.headers ?? '{}')).toEqual(
        operation === 'delete'
          ? {}
          : {
              Authorization:
                operation === 'preserve' ? null : 'literal****secret',
            }
      )
    }
  )
})
