const MAX_BUNDLE_BYTES = 25 * 1024 * 1024;
const MAX_ARCHIVE_BYTES = 512 * 1024 * 1024;
const MAX_COMPRESSED_BUNDLE_BYTES = 32 * 1024 * 1024;
const MAX_ZIP_CENTRAL_DIRECTORY_BYTES = 8 * 1024 * 1024;
const MAX_ZIP_ENTRIES = 4096;
const MAX_TAR_SCAN_BYTES = 32 * 1024 * 1024;
const ZIP_EOCD_MAX_BYTES = 0xffff + 22;

type DecompressionFormat = 'deflate-raw' | 'gzip';
type DecompressionStreamConstructor = new (
  format: DecompressionFormat,
) => TransformStream<Uint8Array, Uint8Array>;

interface ByteSource {
  size: number;
  read: (start: number, end: number) => Promise<ArrayBuffer>;
}

interface ZipEntry {
  name: string;
  flags: number;
  method: number;
  compressedSize: number;
  uncompressedSize: number;
  localOffset: number;
}

// TypeScript 4.9 自带的 DOM 声明还没有 DecompressionStream，但现代浏览器已实现。
// 通过 feature detection 保持旧浏览器的中文错误提示，同时不污染全局类型声明。
const decompressionStreamConstructor = (): DecompressionStreamConstructor | undefined => (
  (globalThis as typeof globalThis & {
    DecompressionStream?: DecompressionStreamConstructor;
  }).DecompressionStream
);

const checkedRange = (size: number, start: number, length: number, label: string): void => {
  if (
    !Number.isSafeInteger(start)
    || !Number.isSafeInteger(length)
    || start < 0
    || length < 0
    || start > size
    || length > size - start
  ) {
    throw new Error(`${label} 超出调查包边界。`);
  }
};

const dataViewHas = (view: DataView, offset: number, length: number): boolean => (
  Number.isSafeInteger(offset)
  && Number.isSafeInteger(length)
  && offset >= 0
  && length >= 0
  && offset <= view.byteLength
  && length <= view.byteLength - offset
);

const findEndOfCentralDirectory = (view: DataView): number => {
  if (view.byteLength < 22) {
    throw new Error('ZIP 文件过短，缺少中央目录。');
  }
  for (let offset = view.byteLength - 22; offset >= 0; offset -= 1) {
    if (view.getUint32(offset, true) !== 0x06054b50) continue;
    const commentLength = view.getUint16(offset + 20, true);
    if (offset + 22 + commentLength === view.byteLength) return offset;
  }
  throw new Error('ZIP 缺少有效的中央目录。');
};

const readStreamBytes = async (
  stream: ReadableStream<Uint8Array>,
  maximumBytes: number,
  tooLargeMessage: string,
): Promise<Uint8Array> => {
  const reader = stream.getReader();
  const chunks: Uint8Array[] = [];
  let total = 0;
  try {
    for (;;) {
      const next = await reader.read();
      if (next.done) break;
      total += next.value.byteLength;
      if (total > maximumBytes) {
        throw new Error(tooLargeMessage);
      }
      chunks.push(next.value);
    }
  } finally {
    try {
      await reader.cancel();
    } catch {
      // 流已正常结束或浏览器已经关闭解压器时无需覆盖主要错误。
    }
  }
  const result = new Uint8Array(total);
  let offset = 0;
  chunks.forEach((chunk) => {
    result.set(chunk, offset);
    offset += chunk.byteLength;
  });
  return result;
};

const inflateRaw = async (compressed: Blob): Promise<Uint8Array> => {
  const DecompressionStreamAPI = decompressionStreamConstructor();
  if (!DecompressionStreamAPI) {
    throw new Error('当前浏览器不支持解压 ZIP，请改为打开调查包内的 bundle.json。');
  }
  const stream = compressed.stream().pipeThrough(new DecompressionStreamAPI('deflate-raw'));
  return readStreamBytes(
    stream,
    MAX_BUNDLE_BYTES,
    'bundle.json 解压后超过 25 MiB，浏览器版 Viewer 拒绝加载。',
  );
};

const concatChunks = (chunks: Uint8Array[], total: number): Uint8Array => {
  const result = new Uint8Array(total);
  let offset = 0;
  chunks.forEach((chunk) => {
    result.set(chunk, offset);
    offset += chunk.byteLength;
  });
  return result;
};

const tarOctal = (bytes: Uint8Array): number => {
  const text = new TextDecoder()
    .decode(bytes)
    .replace(/\0.*$/, '')
    .trim();
  if (text !== '' && !/^[0-7]+$/.test(text)) {
    throw new Error('tar.gz 包含无效的条目大小。');
  }
  const value = Number.parseInt(text || '0', 8);
  if (!Number.isSafeInteger(value) || value < 0) {
    throw new Error('tar.gz 条目大小超出浏览器可安全处理的范围。');
  }
  return value;
};

const isViewerEntry = (name: string): boolean => (
  name === 'bundle.json'
  || name === 'viewer.json'
  || name.endsWith('/bundle.json')
  || name.endsWith('/viewer.json')
);

// diagnostics archive 把 viewer/bundle 放在 tar 的第一个条目。流式读取让手机端
// 无需把几百 MiB 的 Flight Recorder 或日志一并读入内存。
const readBundleJSONFromTarGzip = async (source: Blob): Promise<string> => {
  const DecompressionStreamAPI = decompressionStreamConstructor();
  if (!DecompressionStreamAPI) {
    throw new Error('当前浏览器不支持解压 tar.gz，请从调查包中取出 bundle.json 再打开。');
  }
  const reader = source
    .stream()
    .pipeThrough(new DecompressionStreamAPI('gzip'))
    .getReader();
  let pending = new Uint8Array();
  let consumed = 0;

  const ensure = async (size: number): Promise<void> => {
    if (!Number.isSafeInteger(size) || size < 0 || size > MAX_TAR_SCAN_BYTES) {
      throw new Error('tar.gz 条目大小异常，已停止扫描。');
    }
    if (pending.byteLength >= size) return;
    const chunks = [pending];
    let total = pending.byteLength;
    while (total < size) {
      const next = await reader.read();
      if (next.done) throw new Error('tar.gz 在 bundle.json 完成前意外结束。');
      consumed += next.value.byteLength;
      if (consumed > MAX_TAR_SCAN_BYTES) {
        throw new Error('在调查包前 32 MiB 内没有找到 bundle.json。');
      }
      chunks.push(next.value);
      total += next.value.byteLength;
    }
    pending = concatChunks(chunks, total);
  };

  try {
    for (let entry = 0; entry < 32; entry += 1) {
      await ensure(512);
      const header = pending.slice(0, 512);
      // 两个全零块是 tar 的正常结束标记。
      if (header.every((value) => value === 0)) break;
      const name = new TextDecoder().decode(header.slice(0, 100)).replace(/\0.*$/, '');
      const size = tarOctal(header.slice(124, 136));
      if (isViewerEntry(name) && size > MAX_BUNDLE_BYTES) {
        throw new Error(`${name} 超过 25 MiB，浏览器版 Viewer 拒绝加载。`);
      }
      const paddedSize = Math.ceil(size / 512) * 512;
      if (!Number.isSafeInteger(paddedSize) || paddedSize > MAX_TAR_SCAN_BYTES - 512) {
        throw new Error('tar.gz 条目过大，无法在安全扫描窗口内跳过。');
      }
      await ensure(512 + paddedSize);
      if (isViewerEntry(name)) {
        return new TextDecoder().decode(pending.slice(512, 512 + size));
      }
      pending = pending.slice(512 + paddedSize);
    }
    throw new Error('tar.gz 内没有 bundle.json 或 viewer.json。');
  } finally {
    try {
      await reader.cancel();
    } catch {
      // 浏览器可能已在 gzip 尾部自动关闭流。
    }
  }
};

const arrayBufferSource = (buffer: ArrayBuffer): ByteSource => ({
  size: buffer.byteLength,
  read: async (start, end) => {
    checkedRange(buffer.byteLength, start, end - start, 'ZIP 读取范围');
    return buffer.slice(start, end);
  },
});

const blobSource = (blob: Blob): ByteSource => ({
  size: blob.size,
  read: async (start, end) => {
    checkedRange(blob.size, start, end - start, 'ZIP 读取范围');
    return blob.slice(start, end).arrayBuffer();
  },
});

const readZipEntry = async (source: ByteSource): Promise<ZipEntry> => {
  if (source.size > MAX_ARCHIVE_BYTES) {
    throw new Error('浏览器版 Viewer 暂时只接受不超过 512 MiB 的压缩调查包。');
  }
  const tailStart = Math.max(0, source.size - ZIP_EOCD_MAX_BYTES);
  const tail = new DataView(await source.read(tailStart, source.size));
  const eocd = findEndOfCentralDirectory(tail);
  if (!dataViewHas(tail, eocd, 22)) {
    throw new Error('ZIP 中央目录尾记录不完整。');
  }
  const diskNumber = tail.getUint16(eocd + 4, true);
  const centralDisk = tail.getUint16(eocd + 6, true);
  const entriesOnDisk = tail.getUint16(eocd + 8, true);
  const entryCount = tail.getUint16(eocd + 10, true);
  const centralSize = tail.getUint32(eocd + 12, true);
  const centralOffset = tail.getUint32(eocd + 16, true);
  if (
    diskNumber !== 0
    || centralDisk !== 0
    || entriesOnDisk !== entryCount
    || entryCount === 0xffff
    || centralSize === 0xffffffff
    || centralOffset === 0xffffffff
  ) {
    throw new Error('浏览器版 Viewer 不支持分卷或 ZIP64 调查包。');
  }
  if (entryCount > MAX_ZIP_ENTRIES) {
    throw new Error(`调查包包含超过 ${MAX_ZIP_ENTRIES} 个 ZIP 条目，已停止解析。`);
  }
  if (centralSize > MAX_ZIP_CENTRAL_DIRECTORY_BYTES) {
    throw new Error('ZIP 中央目录超过 8 MiB，已停止解析。');
  }
  const eocdGlobalOffset = tailStart + eocd;
  checkedRange(source.size, centralOffset, centralSize, 'ZIP 中央目录');
  if (centralOffset + centralSize > eocdGlobalOffset) {
    throw new Error('ZIP 中央目录与尾记录重叠。');
  }

  const centralBuffer = await source.read(centralOffset, centralOffset + centralSize);
  const view = new DataView(centralBuffer);
  const bytes = new Uint8Array(centralBuffer);
  const decoder = new TextDecoder();
  let cursor = 0;
  let selected: ZipEntry | undefined;

  for (let index = 0; index < entryCount; index += 1) {
    if (!dataViewHas(view, cursor, 46) || view.getUint32(cursor, true) !== 0x02014b50) {
      throw new Error('ZIP 中央目录已损坏。');
    }
    const flags = view.getUint16(cursor + 8, true);
    const method = view.getUint16(cursor + 10, true);
    const compressedSize = view.getUint32(cursor + 20, true);
    const uncompressedSize = view.getUint32(cursor + 24, true);
    const nameLength = view.getUint16(cursor + 28, true);
    const extraLength = view.getUint16(cursor + 30, true);
    const commentLength = view.getUint16(cursor + 32, true);
    const localOffset = view.getUint32(cursor + 42, true);
    const recordLength = 46 + nameLength + extraLength + commentLength;
    if (!dataViewHas(view, cursor, recordLength)) {
      throw new Error('ZIP 中央目录条目被截断。');
    }
    if (
      compressedSize === 0xffffffff
      || uncompressedSize === 0xffffffff
      || localOffset === 0xffffffff
    ) {
      throw new Error('bundle.json 使用 ZIP64，浏览器版 Viewer 暂不支持。');
    }
    const name = decoder.decode(bytes.slice(cursor + 46, cursor + 46 + nameLength));
    if (!selected && isViewerEntry(name)) {
      selected = {
        name,
        flags,
        method,
        compressedSize,
        uncompressedSize,
        localOffset,
      };
    }
    cursor += recordLength;
  }

  if (!selected) throw new Error('调查包内没有 bundle.json 或 viewer.json。');
  return selected;
};

const extractZipEntry = async (source: ByteSource, selected: ZipEntry): Promise<string> => {
  if ((selected.flags & 0x1) !== 0) {
    throw new Error('bundle.json 已加密，浏览器版 Viewer 无法打开。');
  }
  if (selected.uncompressedSize > MAX_BUNDLE_BYTES) {
    throw new Error('bundle.json 解压后超过 25 MiB，浏览器版 Viewer 拒绝加载。');
  }
  if (selected.compressedSize > MAX_COMPRESSED_BUNDLE_BYTES) {
    throw new Error('bundle.json 压缩数据超过 32 MiB，浏览器版 Viewer 拒绝加载。');
  }
  if (
    selected.compressedSize > 0
    && selected.uncompressedSize / selected.compressedSize > 250
  ) {
    throw new Error('调查包压缩比异常，已阻止可能的 ZIP bomb。');
  }

  checkedRange(source.size, selected.localOffset, 30, 'bundle.json 本地文件头');
  const localHeader = new DataView(await source.read(selected.localOffset, selected.localOffset + 30));
  if (localHeader.getUint32(0, true) !== 0x04034b50) {
    throw new Error('bundle.json 的本地文件头无效。');
  }
  const localFlags = localHeader.getUint16(6, true);
  const localMethod = localHeader.getUint16(8, true);
  const localNameLength = localHeader.getUint16(26, true);
  const localExtraLength = localHeader.getUint16(28, true);
  if ((localFlags & 0x1) !== 0 || localMethod !== selected.method) {
    throw new Error('bundle.json 的本地文件头与中央目录不一致。');
  }
  const nameOffset = selected.localOffset + 30;
  const dataOffset = nameOffset + localNameLength + localExtraLength;
  checkedRange(source.size, nameOffset, localNameLength + localExtraLength, 'bundle.json 文件名与扩展区');
  checkedRange(source.size, dataOffset, selected.compressedSize, 'bundle.json 压缩数据');
  const localName = new TextDecoder().decode(
    new Uint8Array(await source.read(nameOffset, nameOffset + localNameLength)),
  );
  if (localName !== selected.name) {
    throw new Error('bundle.json 的本地文件名与中央目录不一致。');
  }

  let result: Uint8Array;
  if (selected.method === 0) {
    if (selected.compressedSize !== selected.uncompressedSize) {
      throw new Error('未压缩的 bundle.json 大小声明不一致。');
    }
    result = new Uint8Array(await source.read(
      dataOffset,
      dataOffset + selected.compressedSize,
    ));
  } else if (selected.method === 8) {
    result = await inflateRaw(new Blob([
      await source.read(dataOffset, dataOffset + selected.compressedSize),
    ]));
  } else {
    throw new Error(`调查包使用了不支持的 ZIP 压缩方法 ${selected.method}。`);
  }
  if (result.byteLength !== selected.uncompressedSize) {
    throw new Error('bundle.json 解压后的大小与中央目录声明不一致。');
  }
  return new TextDecoder().decode(result);
};

// 只读取调查包中的 bundle/viewer JSON，不解压日志和 Flight Recorder。
// ArrayBuffer 入口供单元测试和既有调用使用；文件导入会走下面的 Blob 分片入口。
export const readBundleJSONFromZip = async (buffer: ArrayBuffer): Promise<string> => {
  const source = arrayBufferSource(buffer);
  return extractZipEntry(source, await readZipEntry(source));
};

const readBundleJSONFromZipBlob = async (blob: Blob): Promise<string> => {
  const source = blobSource(blob);
  return extractZipEntry(source, await readZipEntry(source));
};

export const readDiagnosticFile = async (file: File): Promise<string> => {
  if (file.size === 0) throw new Error('调查包是空文件。');
  if (file.size > MAX_ARCHIVE_BYTES) {
    throw new Error('浏览器版 Viewer 暂时只接受不超过 512 MiB 的压缩调查包。');
  }
  const signature = new Uint8Array(await file.slice(0, 4).arrayBuffer());
  const isZip = signature.length >= 4
    && signature[0] === 0x50
    && signature[1] === 0x4b
    && signature[2] === 0x03
    && signature[3] === 0x04;
  const isGzip = signature.length >= 2 && signature[0] === 0x1f && signature[1] === 0x8b;
  if (isZip) return readBundleJSONFromZipBlob(file);
  if (isGzip) return readBundleJSONFromTarGzip(file);
  if (file.size > MAX_BUNDLE_BYTES) {
    throw new Error('JSON 调查包超过 25 MiB，浏览器版 Viewer 拒绝加载。');
  }
  return file.text();
};
