import { describe, expect, it, vi } from 'vitest'

import {
  baseName,
  displayName,
  extensionOf,
  fileDuration,
  formatBytes,
  formatCount,
  formatDate,
  kindLabel,
  modifiedLabel,
  plural,
  shortHash,
  shortId,
} from '@/lib/format'
import type { CloudFile } from '@/lib/api/types'

function file(partial: Partial<CloudFile> & { name: string }): CloudFile {
  return {
    id: 'file-1',
    account_id: 'account-1',
    workspace_id: null,
    pool_id: null,
    object_id: null,
    parent_id: null,
    indexed: true,
    is_folder: false,
    is_marked_recycle: false,
    expired_at: null,
    storage_url: null,
    storage_key: null,
    file_meta: null,
    user_meta: null,
    usage: null,
    application_type: null,
    status: 3,
    created_at: '2026-10-17T10:00:00Z',
    updated_at: '2026-10-17T10:00:00Z',
    object: null,
    children: null,
    children_count: 0,
    permission_status: null,
    ...partial,
  } as CloudFile
}

describe('formatBytes', () => {
  it('renders zero, units and missing values', () => {
    expect(formatBytes(0)).toBe('0 B')
    expect(formatBytes(1024)).toBe('1 KB')
    expect(formatBytes(1536)).toBe('1.5 KB')
    expect(formatBytes(8.1 * 1024 * 1024)).toBe('8.1 MB')
    expect(formatBytes(null)).toBe('—')
    expect(formatBytes(-4)).toBe('—')
  })
})

describe('names and kinds', () => {
  it('splits extensions without treating dotfiles as extensions', () => {
    expect(extensionOf('orbit-001.json')).toBe('json')
    expect(extensionOf('.gitignore')).toBe('')
    expect(extensionOf('no-extension')).toBe('')
    expect(baseName('orbit-001.json')).toBe('orbit-001')
    expect(baseName('.gitignore')).toBe('.gitignore')
  })

  it('labels folders, extensions and mime-only files', () => {
    expect(kindLabel(file({ name: 'Orbital 7', is_folder: true }))).toBe('FOLDER')
    expect(kindLabel(file({ name: 'pass-042.csv' }))).toBe('CSV')
    expect(
      kindLabel(
        file({
          name: 'README',
          object: {
            id: 'obj-1',
            size: 10,
            mime_type: 'text/markdown',
            hash: '',
            meta: null,
            has_compression: false,
            has_thumbnail: false,
          },
        }),
      ),
    ).toBe('MARKDOWN')
  })

  it('falls back to Untitled for blank names', () => {
    expect(displayName(file({ name: '   ' }))).toBe('Untitled')
  })
})

describe('date formatting', () => {
  it('formats a fixed local date', () => {
    expect(formatDate(new Date(2026, 9, 17))).toBe('17 Oct 2026')
  })

  it('uses relative labels for recent edits', () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-10-17T12:00:00Z'))
    expect(modifiedLabel('2026-10-17T10:00:00Z')).toBe('2 hours ago')
    expect(modifiedLabel('2026-10-01T10:00:00Z')).toBe('01 Oct 26')
    vi.useRealTimers()
  })

  it('renders media durations', () => {
    const media = file({
      name: 'thermal-trace.csv',
      object: {
        id: 'obj-2',
        size: 10,
        mime_type: 'video/mp4',
        hash: 'abc',
        meta: { duration_ms: 214_800 },
        has_compression: false,
        has_thumbnail: true,
      },
    })
    expect(fileDuration(media)).toBe('3:35')
    expect(fileDuration(file({ name: 'no-meta.bin' }))).toBeNull()
  })
})

describe('small helpers', () => {
  it('pluralizes, abbreviates ids and counts', () => {
    expect(plural(1, 'folder')).toBe('folder')
    expect(plural(0, 'folder')).toBe('folders')
    expect(shortId('fld-orbital-7')).toBe('fld-orbi')
    expect(shortHash('5f2c9a1e8b7d4c3a')).toBe('5f2c9a1e8b…')
    expect(shortId(null)).toBe('—')
    expect(shortHash(undefined)).toBe('—')
    expect(formatCount(1200)).toBe('1,200')
  })
})
