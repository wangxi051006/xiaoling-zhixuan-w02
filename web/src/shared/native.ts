// 原生容器桥接：两端共用一个 window.Native 约定，浏览器里全部是空操作。
// 每个方法都可以缺席——商家端没有定位，骑手端没有打印机，缺了就自动退回网页行为。
export interface NativeBridge {
  version(): string;
  platform(): string;
  setAuth(csrf: string): void;
  alert(payload: string): void;
  stopAlert(): void;
  scan?(cbId: string): void;
  printReceipt?(text: string): boolean;
  printerName?(): string;
  startTracking?(): void;
  stopTracking?(): void;
  keepAwake?(on: boolean): void;
  enqueue?(method: string, path: string, body: string, idemKey: string): void;
}

const raw = (globalThis as unknown as { Native?: NativeBridge }).Native;

export const native = {
  available: raw !== undefined,
  version: () => raw?.version() ?? 'web',
  setAuth: (csrf: string) => raw?.setAuth(csrf),
  alert: (payload: unknown) => raw?.alert(JSON.stringify(payload)),
  stopAlert: () => raw?.stopAlert(),
  scan: (cbId: string) => raw?.scan?.(cbId),
  printReceipt: (text: string) => raw?.printReceipt?.(text) ?? false,
  printerName: () => raw?.printerName?.() ?? '',
  startTracking: () => raw?.startTracking?.(),
  stopTracking: () => raw?.stopTracking?.(),
  keepAwake: (on: boolean) => raw?.keepAwake?.(on),
  enqueue: (method: string, path: string, body: unknown, idemKey: string) =>
    raw?.enqueue?.(method, path, JSON.stringify(body), idemKey),
};

// 原生回调入口
(globalThis as unknown as Record<string, unknown>).__nativeScan = (cb: string, code: string) =>
  window.dispatchEvent(new CustomEvent('native:scan', { detail: { cb, code } }));
(globalThis as unknown as Record<string, unknown>).__nativeResume = () =>
  window.dispatchEvent(new Event('native:resume'));
