export function moneyAmount(fen: number) {
  const absolute = Math.abs(fen);
  const cents = absolute % 100;
  const fraction = cents ? `.${String(cents).padStart(2, '0').replace(/0$/, '')}` : '';
  return `${fen < 0 ? '-' : ''}${(absolute - cents) / 100}${fraction}`;
}

export const money = (fen: number) => `${moneyAmount(fen)} 块`;

export const moneyPattern = String.raw`([0-9]+)(?:\.([0-9]{1,2}))?`;
const moneyInput = new RegExp(`^${moneyPattern}$`);

export function parseMoney(value: string) {
  const match = moneyInput.exec(value);
  if (!match) return undefined;
  const fen = Number(match[1] + (match[2] ?? '').padEnd(2, '0'));
  return Number.isSafeInteger(fen) ? fen : undefined;
}
