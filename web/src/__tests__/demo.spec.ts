import { describe, expect, it } from 'vitest'

import {
  sampleFile,
  sampleFiles,
  sampleList,
  samplePurge,
  sampleSetVisibility,
  sampleUpload,
} from '@/lib/demo'

describe('sample listings', () => {
  it('lists the drive root with folders first and recycled files excluded', () => {
    const page = sampleList('drive', null)
    expect(page.total).toBeGreaterThan(0)
    expect(page.items.some((item) => item.is_marked_recycle)).toBe(false)
    const firstFile = page.items.findIndex((item) => !item.is_folder)
    expect(firstFile).toBeGreaterThan(0)
    expect(page.items.slice(0, firstFile).every((item) => item.is_folder)).toBe(true)
  })

  it('lists only recycled objects in the recycle bin', () => {
    const page = sampleList('recycle', null)
    expect(page.items.length).toBeGreaterThan(0)
    expect(page.items.every((item) => item.is_marked_recycle)).toBe(true)
  })

  it('filters by query, kind and paging', () => {
    const all = sampleList('drive', 'fld-orbital')
    expect(all.total).toBeGreaterThan(2)

    const query = sampleList('drive', 'fld-orbital', { query: 'anomaly' })
    expect(query.items.map((item) => item.name)).toEqual(['anomaly-report.md'])

    const folders = sampleList('drive', null, { isFolder: true })
    expect(folders.items.every((item) => item.is_folder)).toBe(true)

    const first = sampleList('drive', null, { take: 2 })
    expect(first.items).toHaveLength(2)
    expect(first.total).toBeGreaterThan(first.items.length)
  })

  it('sorts by name ascending', () => {
    const page = sampleList('drive', null, { order: 'name', orderDesc: false })
    const rootFiles = page.items.filter((item) => !item.is_folder).map((item) => item.name)
    const sorted = [...rootFiles].sort((a, b) => a.localeCompare(b, 'en', { numeric: true }))
    expect(rootFiles).toEqual(sorted)
  })
})

describe('sample mutations', () => {
  it('adds an uploaded file to its parent and updates the folder count', () => {
    const parent = sampleFile('fld-orbital')
    expect(parent).not.toBeNull()
    const before = parent?.children_count ?? 0

    const created = sampleUpload(
      new File(['x'], 'uplink-check.txt', { type: 'text/plain' }),
      'fld-orbital',
    )

    expect(sampleFile(created.id)?.name).toBe('uplink-check.txt')
    expect(parent?.children_count).toBe(before + 1)
    expect(sampleList('drive', 'fld-orbital').items.some((item) => item.id === created.id)).toBe(
      true,
    )

    samplePurge([created.id])
    expect(sampleFile(created.id)).toBeNull()
    expect(parent?.children_count).toBe(before)
  })

  it('toggles public visibility on the fixture', () => {
    const item = sampleFiles.find((entry) => entry.permission_status?.visibility === 'private')
    expect(item).toBeDefined()
    if (!item) return
    sampleSetVisibility(item.id, true)
    expect(sampleFile(item.id)?.permission_status?.visibility).toBe('public')
    sampleSetVisibility(item.id, false)
    expect(sampleFile(item.id)?.permission_status?.visibility).toBe('private')
  })
})
