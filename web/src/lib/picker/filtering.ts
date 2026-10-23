/**
 * Which objects an embedder will accept.
 *
 * The listing API filters by folder/file, but a picker's rules are broader (a
 * family such as `image/*`, or a set of extensions), and a folder must stay
 * navigable so a person can reach the objects inside it. So navigation
 * filtering and acceptance filtering are separate questions, answered here.
 */

import type { CloudFile } from '@/lib/api/types'
import { extensionOf, mimeOf } from '@/lib/format'
import { fileKind } from '@/lib/fileKinds'
import type { PickerOptions } from './protocol'

function matchesMime(file: CloudFile, accepted: string[]): boolean {
  const mime = mimeOf(file).toLowerCase()
  return accepted.some((rule) => {
    const wanted = rule.trim().toLowerCase()
    if (!wanted) return false
    if (wanted.endsWith('/*')) return mime.startsWith(wanted.slice(0, -1))
    return mime === wanted
  })
}

function hasAcceptanceRules(options: PickerOptions): boolean {
  return Boolean(options.mimeTypes?.length || options.kinds?.length || options.extensions?.length)
}

/** True when a folder may be entered (or chosen, for a folder view). */
export function isFolderView(options: PickerOptions): boolean {
  return options.view === 'folders' || options.view === 'any'
}

/**
 * True when an object satisfies every rule set the embedder gave. Folders are
 * selectable only in a folder or any view; files only when the view is not
 * folders-only.
 */
export function isSelectable(file: CloudFile, options: PickerOptions): boolean {
  if (file.is_folder) return isFolderView(options)
  if (options.view === 'folders') return false
  if (!hasAcceptanceRules(options)) return true

  if (options.mimeTypes?.length && !matchesMime(file, options.mimeTypes)) return false
  if (options.kinds?.length) {
    const group = fileKind(file).group
    if (!options.kinds.some((kind) => kind.toLowerCase() === group)) return false
  }
  if (options.extensions?.length) {
    const extension = extensionOf(file.name)
    if (!options.extensions.some((wanted) => wanted.trim().toLowerCase() === extension)) {
      return false
    }
  }
  return true
}

/**
 * The listing request for one folder. A folder-only view asks the API for
 * folders alone; every other view lists everything, because filtering files
 * out of the response would hide the rows that explain why an object cannot
 * be chosen.
 */
export function listingFilters(options: PickerOptions): { is_folder?: boolean } {
  return options.view === 'folders' ? { is_folder: true } : {}
}
