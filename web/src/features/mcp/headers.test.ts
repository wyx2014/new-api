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
import { describe, expect, it } from 'vitest'

import { buildMCPHeaders, parseMCPHeaders } from './headers'

describe('MCP header editing contract', () => {
  it('parses colon, equals, JSON, and values containing separators', () => {
    expect(
      parseMCPHeaders('Authorization: Bearer test\nX-API-Key=a:b=c')
    ).toEqual({ Authorization: 'Bearer test', 'X-API-Key': 'a:b=c' })
    expect(parseMCPHeaders('{"X-Test":"value"}')).toEqual({ 'X-Test': 'value' })
    const quoted = { 'X-Test': 'quoted "Name": value', Name: 'other' }
    expect(parseMCPHeaders(JSON.stringify(quoted))).toEqual(quoted)
    expect(parseMCPHeaders('')).toEqual({})
  })
  it('rejects duplicate names, invalid names, nonstrings and reserved headers', () => {
    for (const input of [
      'X-Test: a\nx-test=b',
      '{"X-Test":1}',
      '{"X-Test":"a","x-test":"b"}',
      'Host: example.com',
      'Accept-Encoding: gzip',
      'Bad Name: a',
      '{"X-Test":"a\\r\\nb"}',
    ]) {
      expect(() => parseMCPHeaders(input)).toThrow()
    }
  })
  it('keeps saved values with explicit null and replaces or removes by name', () => {
    expect(
      JSON.parse(
        buildMCPHeaders(
          ['Authorization', 'X-Test'],
          'authorization: literal****secret'
        )
      )
    ).toEqual({ Authorization: 'literal****secret', 'X-Test': null })
    expect(JSON.parse(buildMCPHeaders([], ''))).toEqual({})
    expect(() => parseMCPHeaders('{"X-Test":null}')).toThrow()
  })
})
