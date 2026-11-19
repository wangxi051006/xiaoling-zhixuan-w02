import { money } from './money.ts';

// difference_fen is the stored ledger direction (expected_fen - actual_fen):
// positive is short-paid, negative is over-paid and zero is balanced. Callers
// label that column; they must never subtract the two amounts themselves, and an
// unverified batch has no difference at all rather than a zero one.
export type CODDifference = { expected_fen: number; actual_fen: number | null; difference_fen: number | null };

export const codDifference = (batch: CODDifference): string => {
  const delta = batch.difference_fen;
  if (delta === null || delta === undefined) return batch.actual_fen === null ? '尚未复点' : '差额缺失';
  if (delta === 0) return '已对平';
  return `${delta > 0 ? '少缴' : '多缴'} ${money(Math.abs(delta))}`;
};
