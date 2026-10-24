import { describe, expect, it } from 'vitest'

import type { CloudFile } from '@/lib/api/types'
import { isSelectable, listingFilters } from '@/lib/picker/filtering'
import {
  normalizeOptions,
  optionsFromQuery,
  optionsToQuery,
  parseHostMessage,
  PICKER_CHANNEL,
} from '@/lib/picker/protocol'

function file(partial: Partial<CloudFile> & { name: string }): CloudFile {
  return {
    id: partial.name,
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
    created_at: '2026-10-01T10:00:00Z',
    updated_at: '2026-10-01T10:00:00Z',
    children: null,
    children_count: 0,
    permission_status: null,
    ...partial,
  } as CloudFile
}

const folder = file({ name: 'Images', is_folder: true })

function object(name: string, mime: string): CloudFile {
  return file({
    name,
    object: {
      id: `obj-${name}`,
      size: 1,
      mime_type: mime,
      hash: 'h',
      has_compression: false,
      has_thumbnail: false,
    },
  })
}

describe('picker acceptance', () => {
  it('accepts every file in a plain file view', () => {
    const options = normalizeOptions({ view: 'files' })
    expect(isSelectable(object('a.bin', 'application/octet-stream'), options)).toBe(true)
  })

  it('keeps folders navigable but unselectable in a file view', () => {
    expect(isSelectable(folder, normalizeOptions({ view: 'files' }))).toBe(false)
    expect(isSelectable(folder, normalizeOptions({ view: 'any' }))).toBe(true)
    expect(isSelectable(folder, normalizeOptions({ view: 'folders' }))).toBe(true)
  })

  it('refuses files in a folder-only view', () => {
    expect(isSelectable(object('a.png', 'image/png'), normalizeOptions({ view: 'folders' }))).toBe(
      false,
    )
  })

  it('matches a MIME family and an exact type', () => {
    const images = normalizeOptions({ mimeTypes: ['image/*'] })
    expect(isSelectable(object('a.png', 'image/png'), images)).toBe(true)
    expect(isSelectable(object('a.pdf', 'application/pdf'), images)).toBe(false)

    const pdf = normalizeOptions({ mimeTypes: ['application/pdf'] })
    expect(isSelectable(object('a.pdf', 'application/pdf'), pdf)).toBe(true)
    expect(isSelectable(object('a.png', 'image/png'), pdf)).toBe(false)
  })

  it('applies kinds and extensions together', () => {
    const documents = normalizeOptions({ kinds: ['document'], extensions: ['txt'] })
    expect(isSelectable(object('note.txt', 'text/plain'), documents)).toBe(true)
    // Right extension, wrong family.
    expect(isSelectable(file({ name: 'note.txt', object: null }), documents)).toBe(true)
    expect(isSelectable(object('photo.png', 'image/png'), documents)).toBe(false)
  })

  it('asks the API for folders alone only in a folder view', () => {
    expect(listingFilters(normalizeOptions({ view: 'folders' }))).toEqual({ is_folder: true })
    expect(listingFilters(normalizeOptions({ view: 'files' }))).toEqual({})
    expect(listingFilters(normalizeOptions({ view: 'any' }))).toEqual({})
  })
})

describe('picker options', () => {
  it('round-trips through a query string', () => {
    const options = normalizeOptions({
      view: 'any',
      multiple: true,
      upload: true,
      mimeTypes: ['image/*', 'application/pdf'],
      kinds: ['image'],
      extensions: ['png'],
      startFolderId: 'folder-1',
      title: 'Attach',
      theme: 'light',
      locale: 'zh-CN',
    })
    expect(optionsFromQuery(`?${optionsToQuery(options)}`)).toEqual(options)
  })

  it('reads a comma-separated query into lists', () => {
    const options = optionsFromQuery('?view=folders&multiple=1&mime=image/*,application/pdf')
    expect(options.view).toBe('folders')
    expect(options.multiple).toBe(true)
    expect(options.mimeTypes).toEqual(['image/*', 'application/pdf'])
  })

  it('falls back instead of throwing on unknown values', () => {
    const options = normalizeOptions({ view: 'spreadsheet', theme: 'neon', multiple: 'yes' })
    expect(options.view).toBe('files')
    expect(options.theme).toBe('auto')
    expect(options.multiple).toBe(false)
  })
})

describe('picker messages', () => {
  it('accepts an open request and drops foreign channels', () => {
    expect(parseHostMessage({ channel: PICKER_CHANNEL, type: 'picker:close' })).toEqual({
      channel: PICKER_CHANNEL,
      type: 'picker:close',
    })
    expect(parseHostMessage({ channel: 'other', type: 'picker:close' })).toBeNull()
    expect(parseHostMessage({ channel: PICKER_CHANNEL, type: 'picker:open' })).toEqual({
      channel: PICKER_CHANNEL,
      type: 'picker:open',
      options: {},
    })
    // A wrapper carrying only a code is a valid open request: a cross-site
    // host with no token of its own redeems one against the drive.
    expect(
      parseHostMessage({ channel: PICKER_CHANNEL, type: 'picker:open', options: { view: 'files' }, code: ' handoff-code ' }),
    ).toEqual({
      channel: PICKER_CHANNEL,
      type: 'picker:open',
      options: { view: 'files' },
      code: 'handoff-code',
    })
    // A token and a code never travel together, but a blank code is dropped
    // rather than poisoning the request.
    expect(
      parseHostMessage({ channel: PICKER_CHANNEL, type: 'picker:open', token: 'tok', code: '  ' }),
    ).toEqual({
      channel: PICKER_CHANNEL,
      type: 'picker:open',
      options: {},
      token: 'tok',
    })
  })

  it('ignores values that are not messages', () => {
    expect(parseHostMessage('picker:open')).toBeNull()
    expect(parseHostMessage(null)).toBeNull()
    expect(parseHostMessage({ channel: PICKER_CHANNEL })).toBeNull()
  })
})
