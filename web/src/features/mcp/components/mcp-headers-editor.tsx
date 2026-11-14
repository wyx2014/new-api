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
import { Eye, EyeOff } from 'lucide-react'
import { useState, type CSSProperties } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Textarea } from '@/components/ui/textarea'

type MCPHeadersEditorProps = {
  value: string
  onChange: (value: string) => void
  retained: string[]
  onRetainedChange: (value: string[]) => void
  id?: string
  'aria-describedby'?: string
  'aria-invalid'?: boolean
}

export function MCPHeadersEditor(props: MCPHeadersEditorProps) {
  const { t } = useTranslation()
  const [visible, setVisible] = useState(false)
  return (
    <div className='space-y-2'>
      {props.retained.map((name) => (
        <div key={name} className='flex items-center justify-between gap-2'>
          <span>
            {name}: {t('Saved value hidden')}
          </span>
          <Button
            type='button'
            variant='ghost'
            size='sm'
            aria-label={t('Remove saved header {{name}}', { name })}
            onClick={() =>
              props.onRetainedChange(
                props.retained.filter((key) => key !== name)
              )
            }
          >
            {t('Remove')}
          </Button>
        </div>
      ))}
      <Textarea
        id={props.id}
        aria-describedby={props['aria-describedby']}
        aria-invalid={props['aria-invalid']}
        value={props.value}
        onChange={(event) => props.onChange(event.target.value)}
        placeholder={t('Header: Value, Header=Value, or a JSON object')}
        autoComplete='off'
        spellCheck={false}
        rows={4}
        style={
          visible
            ? undefined
            : ({ WebkitTextSecurity: 'disc' } as CSSProperties)
        }
      />
      <div className='flex flex-wrap gap-2'>
        <Button
          type='button'
          variant='outline'
          size='sm'
          onClick={() =>
            props.onChange(
              `${props.value}${props.value.trim() ? '\n' : ''}Authorization: Bearer YOUR_UPSTREAM_KEY`
            )
          }
        >
          {t('Insert Bearer example')}
        </Button>
        <Button
          type='button'
          variant='ghost'
          size='sm'
          aria-label={t('Toggle new header visibility')}
          aria-pressed={visible}
          onClick={() => setVisible(!visible)}
        >
          {visible ? <EyeOff /> : <Eye />}
          {t('Toggle new header visibility')}
        </Button>
      </div>
      <p className='text-muted-foreground text-sm'>
        {t(
          'Saved headers are retained unless removed or replaced by name. Saved values cannot be revealed.'
        )}
      </p>
    </div>
  )
}
