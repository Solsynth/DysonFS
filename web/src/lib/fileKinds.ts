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

export interface FileKind {
  label: string
  icon: Component
  tone: string
  group: 'folder' | 'image' | 'video' | 'audio' | 'document' | 'archive' | 'code' | 'data'
}

const codeExtensions = new Set([
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

const dataExtensions = new Set(['json', 'xml', 'csv', 'tsv', 'parquet', 'ndjson'])
const archiveExtensions = new Set(['zip', 'tar', 'gz', 'tgz', 'bz2', 'xz', '7z', 'rar', 'dmg', 'iso'])
const imageExtensions = new Set(['png', 'jpg', 'jpeg', 'gif', 'webp', 'avif', 'heic', 'svg', 'bmp', 'tif', 'tiff'])
const videoExtensions = new Set(['mp4', 'mov', 'mkv', 'webm', 'avi', 'm4v', 'mpg', 'mpeg'])
const audioExtensions = new Set(['mp3', 'flac', 'wav', 'ogg', 'm4a', 'aac', 'opus'])
const sheetExtensions = new Set(['xls', 'xlsx', 'csv', 'ods'])
const slideExtensions = new Set(['ppt', 'pptx', 'odp', 'key'])
const documentExtensions = new Set(['pdf', 'doc', 'docx', 'odt', 'rtf', 'txt', 'md', 'pages'])

export function fileKind(file: CloudFile): FileKind {
  if (file.is_folder) {
    return {
      label: 'Folder',
      icon: Folder16,
      tone: 'text-brass',
      group: 'folder',
    }
  }

  const extension = extensionOf(file.name)
  const mime = mimeOf(file)
  const family = mime.split('/')[0]

  if (family === 'image' || imageExtensions.has(extension)) {
    return { label: 'Image', icon: Image16, tone: 'text-info', group: 'image' }
  }
  if (family === 'video' || videoExtensions.has(extension)) {
    return { label: 'Video', icon: Video16, tone: 'text-primary', group: 'video' }
  }
  if (family === 'audio' || audioExtensions.has(extension)) {
    return { label: 'Audio', icon: Music16, tone: 'text-success', group: 'audio' }
  }
  if (extension === 'pdf') {
    return { label: 'PDF', icon: DocumentPdf16, tone: 'text-error', group: 'document' }
  }
  if (slideExtensions.has(extension)) {
    return { label: 'Slides', icon: PresentationFile16, tone: 'text-brass', group: 'document' }
  }
  if (sheetExtensions.has(extension)) {
    return { label: 'Sheet', icon: DataTable16, tone: 'text-success', group: 'data' }
  }
  if (archiveExtensions.has(extension)) {
    return { label: 'Archive', icon: Archive16, tone: 'text-brass', group: 'archive' }
  }
  if (codeExtensions.has(extension)) {
    return { label: 'Source', icon: Code16, tone: 'text-info', group: 'code' }
  }
  if (dataExtensions.has(extension)) {
    return { label: 'Data', icon: DataBase16, tone: 'text-info', group: 'data' }
  }
  if (documentExtensions.has(extension) || family === 'text') {
    return { label: 'Document', icon: Document16, tone: 'text-dim', group: 'document' }
  }
  return {
    label: extension ? extension.toUpperCase() : 'File',
    icon: DocumentBlank16,
    tone: 'text-faint',
    group: 'document',
  }
}

export function canPreviewImage(file: CloudFile): boolean {
  return fileKind(file).group === 'image'
}

export function hasPreview(file: CloudFile): boolean {
  if (file.is_folder) return false
  if (fileKind(file).group === 'image') return true
  return file.object?.has_thumbnail === true
}
