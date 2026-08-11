import { readBundleJSONFromZip } from './zip';
import { TextDecoder as NodeTextDecoder } from 'util';

Object.defineProperty(globalThis, 'TextDecoder', {
  configurable: true,
  value: NodeTextDecoder,
});

const writeUint16 = (bytes: Uint8Array, offset: number, value: number) => {
  new DataView(bytes.buffer).setUint16(offset, value, true);
};

const writeUint32 = (bytes: Uint8Array, offset: number, value: number) => {
  new DataView(bytes.buffer).setUint32(offset, value, true);
};

const storedZip = (
  name: string,
  text: string,
  declaredUncompressedSize?: number,
): ArrayBuffer => {
  const nameBytes = Uint8Array.from(Buffer.from(name, 'utf8'));
  const data = Uint8Array.from(Buffer.from(text, 'utf8'));
  const localSize = 30 + nameBytes.length + data.length;
  const centralSize = 46 + nameBytes.length;
  const result = new Uint8Array(localSize + centralSize + 22);

  writeUint32(result, 0, 0x04034b50);
  writeUint16(result, 4, 20);
  writeUint16(result, 8, 0);
  writeUint32(result, 18, data.length);
  writeUint32(result, 22, declaredUncompressedSize ?? data.length);
  writeUint16(result, 26, nameBytes.length);
  result.set(nameBytes, 30);
  result.set(data, 30 + nameBytes.length);

  const central = localSize;
  writeUint32(result, central, 0x02014b50);
  writeUint16(result, central + 4, 20);
  writeUint16(result, central + 6, 20);
  writeUint16(result, central + 10, 0);
  writeUint32(result, central + 20, data.length);
  writeUint32(result, central + 24, declaredUncompressedSize ?? data.length);
  writeUint16(result, central + 28, nameBytes.length);
  writeUint32(result, central + 42, 0);
  result.set(nameBytes, central + 46);

  const eocd = central + centralSize;
  writeUint32(result, eocd, 0x06054b50);
  writeUint16(result, eocd + 8, 1);
  writeUint16(result, eocd + 10, 1);
  writeUint32(result, eocd + 12, centralSize);
  writeUint32(result, eocd + 16, central);
  return result.buffer;
};

describe('诊断 ZIP 的有界读取', () => {
  test.each([
    'bundle.json',
    'viewer.json',
    'diagnostics/viewer.json',
  ])('只读取 %s，不要求解压其它附件', async (name) => {
    const json = '{"schema":"bililive.diagnostic-bundle/v1"}';
    await expect(readBundleJSONFromZip(storedZip(name, json))).resolves.toBe(json);
  });

  test('拒绝中央目录声明的超大解压结果', async () => {
    await expect(readBundleJSONFromZip(
      storedZip('bundle.json', '{}', 25 * 1024 * 1024 + 1),
    )).rejects.toThrow('解压后超过 25 MiB');
  });

  test('损坏的中央目录偏移不会触发越界读取', async () => {
    const zip = new Uint8Array(storedZip('bundle.json', '{}'));
    writeUint32(zip, zip.length - 22 + 16, 0xfffffff0);

    await expect(readBundleJSONFromZip(zip.buffer)).rejects.toThrow('超出调查包边界');
  });

  test('拒绝异常多的 ZIP 条目，避免在手机端长时间扫描', async () => {
    const zip = new Uint8Array(storedZip('bundle.json', '{}'));
    writeUint16(zip, zip.length - 22 + 8, 4097);
    writeUint16(zip, zip.length - 22 + 10, 4097);

    await expect(readBundleJSONFromZip(zip.buffer)).rejects.toThrow('超过 4096 个 ZIP 条目');
  });
});
