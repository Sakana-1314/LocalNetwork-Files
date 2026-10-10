/** 拖放 / 选择得到的文件及其相对路径（保留文件夹结构） */
export interface PickedFile {
  file: File
  /** 相对路径，如 "照片/2024/a.jpg"；单个文件即文件名 */
  relPath: string
}

/** 从 <input type=file>（含 webkitdirectory）得到的 FileList 中提取 */
export function fromFileList(list: FileList | File[]): PickedFile[] {
  return Array.from(list).map((file) => ({
    file,
    relPath: (file as File & { webkitRelativePath?: string }).webkitRelativePath || file.name,
  }))
}

function readEntries(reader: FileSystemDirectoryReader): Promise<FileSystemEntry[]> {
  return new Promise((resolve, reject) => reader.readEntries(resolve, reject))
}

function entryFile(entry: FileSystemFileEntry): Promise<File> {
  return new Promise((resolve, reject) => entry.file(resolve, reject))
}

async function walk(entry: FileSystemEntry, prefix: string, out: PickedFile[]): Promise<void> {
  if (entry.isFile) {
    const file = await entryFile(entry as FileSystemFileEntry)
    out.push({ file, relPath: prefix + entry.name })
  } else if (entry.isDirectory) {
    const reader = (entry as FileSystemDirectoryEntry).createReader()
    // readEntries 每次最多返回 100 条，需要循环读取
    for (;;) {
      const batch = await readEntries(reader)
      if (!batch.length) break
      for (const child of batch) await walk(child, prefix + entry.name + '/', out)
    }
  }
}

/** 从拖放事件的 DataTransfer 中递归提取文件（支持文件夹） */
export async function fromDataTransfer(dt: DataTransfer): Promise<PickedFile[]> {
  const out: PickedFile[] = []
  const items = Array.from(dt.items ?? [])
  const entries = items
    .filter((i) => i.kind === 'file')
    .map((i) => (typeof i.webkitGetAsEntry === 'function' ? i.webkitGetAsEntry() : null))
  if (entries.length && entries.every((e) => e !== null)) {
    for (const e of entries) await walk(e as FileSystemEntry, '', out)
    return out
  }
  return fromFileList(dt.files)
}

export function hasFiles(dt: DataTransfer | null): boolean {
  return !!dt && Array.from(dt.types ?? []).includes('Files')
}
