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
const headerName = /^[!#$%&'*+.^_`|~\dA-Za-z-]+$/
const reserved =
  /^(host|cookie|set-cookie|connection|proxy-.*|forwarded|x-forwarded-.*|content-length|transfer-encoding|trailer|te|upgrade|accept|accept-encoding|content-type|mcp-.*|last-event-id)$/i

export const MCP_DEFAULT_TIMEOUT = 60
export const MCP_MAX_TIMEOUT = 86400

export function parseMCPHeaders(input: string): Record<string, string> {
  const text = input.trim()
  if (!text) return {}
  let entries: [string, unknown][]
  if (text.startsWith('{')) {
    const parsed: unknown = JSON.parse(text)
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
      throw new Error('Invalid MCP headers')
    }
    entries = Object.entries(parsed)
    const keys = [
      ...text.matchAll(/"((?:\\.|[^"\\])*)"\s*:\s*"(?:\\.|[^"\\])*"/g),
    ].map((match) => JSON.parse(`"${match[1]}"`) as string)
    if (new Set(keys.map((key) => key.toLowerCase())).size !== keys.length) {
      throw new Error('Invalid MCP headers')
    }
  } else {
    entries = text
      .split(/\r?\n/)
      .filter((line) => line.trim())
      .map((line) => {
        const separator = line.search(/[:=]/)
        if (separator < 1) throw new Error('Invalid MCP headers')
        return [
          line.slice(0, separator).trim(),
          line.slice(separator + 1).trim(),
        ]
      })
  }
  const result: Record<string, string> = Object.create(null)
  const names = new Set<string>()
  for (const [name, value] of entries) {
    const key = name.toLowerCase()
    if (
      !headerName.test(name) ||
      reserved.test(name) ||
      typeof value !== 'string' ||
      [...value].some((char) => {
        const code = char.charCodeAt(0)
        return (code < 32 && code !== 9) || code === 127
      }) ||
      names.has(key)
    ) {
      throw new Error('Invalid MCP headers')
    }
    names.add(key)
    result[name] = value
  }
  return result
}

export function buildMCPHeaders(retained: string[], input: string): string {
  const result: Record<string, string | null> = Object.create(null)
  for (const name of retained) result[name] = null
  for (const [name, value] of Object.entries(parseMCPHeaders(input))) {
    const saved = retained.find(
      (key) => key.toLowerCase() === name.toLowerCase()
    )
    result[saved ?? name] = value
  }
  return JSON.stringify(result)
}
