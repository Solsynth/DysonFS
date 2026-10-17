import type { Component } from 'vue'
import {
  Archive16,
  Code16,
  DataBase16,
  DataTable16,
  Document16,
  DocumentPdf16,
  Folder16,
  Image16,
  Music16,
  PresentationFile16,
  Video16,
  DocumentBlank16,
} from '@carbon/icons-vue'

import type { CloudFile } from './api/types'
import { extensionOf, mimeOf } from './format'
import { translate } from './i18n'

export interface FileKind {
  label: string
  icon: Component
  tone: string
  group: 'folder' | 'image' | 'video' | 'audio' | 'document' | 'archive' | 'code' | 'data'
}

function extensionSet(names: string[]): Record<string, true> {
  return Object.fromEntries(names.map((name) => [name, true]))
}

const codeExtensions = extensionSet([
  'js',
  'ts',
  'tsx',
  'jsx',
  'vue',
  'py',
  'go',
  'rs',
  'rb',
  'java',
  'kt',
  'c',
  'h',
  'cpp',
  'cs',
  'sh',
  'zsh',
  'sql',
  'css',
  'scss',
  'html',
  'yaml',
  'yml',
  'toml',
])

const dataExtensions = extensionSet(['json', 'xml', 'csv', 'tsv', 'parquet', 'ndjson'])
const archiveExtensions = extensionSet([
  'zip',
  'tar',
  'gz',
  'tgz',
  'bz2',
  'xz',
  '7z',
  'rar',
  'dmg',
  'iso',
])
const imageExtensions = extensionSet([
  'png',
  'jpg',
  'jpeg',
  'gif',
  'webp',
  'avif',
  'heic',
  'svg',
  'bmp',
  'tif',
  'tiff',
])
const videoExtensions = extensionSet(['mp4', 'mov', 'mkv', 'webm', 'avi', 'm4v', 'mpg', 'mpeg'])
const audioExtensions = extensionSet(['mp3', 'flac', 'wav', 'ogg', 'm4a', 'aac', 'opus'])
const sheetExtensions = extensionSet(['xls', 'xlsx', 'csv', 'ods'])
const slideExtensions = extensionSet(['ppt', 'pptx', 'odp', 'key'])
const documentExtensions = extensionSet(['pdf', 'doc', 'docx', 'odt', 'rtf', 'txt', 'md', 'pages'])

export function fileKind(file: CloudFile): FileKind {
  if (file.is_folder) {
    return {
      label: translate('label.folder'),
      icon: Folder16,
      tone: 'text-dim',
      group: 'folder',
    }
  }

  const extension = extensionOf(file.name)
  const mime = mimeOf(file)
  const family = mime.split('/')[0]

  if (family === 'image' || imageExtensions[extension]) {
    return { label: translate('label.image'), icon: Image16, tone: 'text-faint', group: 'image' }
  }
  if (family === 'video' || videoExtensions[extension]) {
    return { label: translate('label.video'), icon: Video16, tone: 'text-faint', group: 'video' }
  }
  if (family === 'audio' || audioExtensions[extension]) {
    return { label: translate('label.audio'), icon: Music16, tone: 'text-faint', group: 'audio' }
  }
  if (extension === 'pdf') {
    return { label: translate('label.pdf'), icon: DocumentPdf16, tone: 'text-faint', group: 'document' }
  }
  if (slideExtensions[extension]) {
    return { label: translate('label.slides'), icon: PresentationFile16, tone: 'text-faint', group: 'document' }
  }
  if (sheetExtensions[extension]) {
    return { label: translate('label.sheet'), icon: DataTable16, tone: 'text-faint', group: 'data' }
  }
  if (archiveExtensions[extension]) {
    return { label: translate('label.archive'), icon: Archive16, tone: 'text-faint', group: 'archive' }
  }
  if (codeExtensions[extension]) {
    return { label: translate('label.source'), icon: Code16, tone: 'text-faint', group: 'code' }
  }
  if (dataExtensions[extension]) {
    return { label: translate('label.data'), icon: DataBase16, tone: 'text-faint', group: 'data' }
  }
  if (documentExtensions[extension] || family === 'text') {
    return { label: translate('label.document'), icon: Document16, tone: 'text-dim', group: 'document' }
  }
  return {
    label: extension ? extension.toUpperCase() : translate('label.file'),
    icon: DocumentBlank16,
    tone: 'text-faint',
    group: 'document',
  }
}

/**
 * How a file can be presented inside a preview tab: an inline browser surface
 * (image, video, audio, pdf, or text fetched and rendered as code), a stored
 * derivative, or nothing but its metadata.
 */
export type PreviewMode = 'image' | 'video' | 'audio' | 'pdf' | 'text' | 'none'

/** Extensions whose bytes are worth fetching and printing as source text. */
const textPreviewExtensions: Record<string, true> = {
  ...codeExtensions,
  ...dataExtensions,
  txt: true,
  md: true,
  markdown: true,
  log: true,
  ini: true,
  conf: true,
  env: true,
}

export function previewMode(file: CloudFile): PreviewMode {
  if (file.is_folder) return 'none'
  const kind = fileKind(file).group

  if (kind === 'image') return 'image'
  if (kind === 'video') return 'video'
  if (kind === 'audio') return 'audio'

  const extension = extensionOf(file.name)
  if (extension === 'pdf') return 'pdf'
  if (textPreviewExtensions[extension]) return 'text'
  return 'none'
}

export function canPreviewImage(file: CloudFile): boolean {
  return fileKind(file).group === 'image'
}

export function hasPreview(file: CloudFile): boolean {
  if (file.is_folder) return false
  if (fileKind(file).group === 'image') return true
  return file.object?.has_thumbnail === true
}
