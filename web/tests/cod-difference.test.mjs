import assert from 'node:assert/strict';
import test from 'node:test';
import { codDifference } from '../src/shared/cod.ts';
import { money } from '../src/shared/money.ts';

// P08-F01: the stored ledger direction, not a client subtraction, decides the label.
test('an unverified batch has no difference at all', () => {
  assert.equal(codDifference({ expected_fen: 1000, actual_fen: null, difference_fen: null }), '尚未复点');
});

test('the label follows expected_fen minus actual_fen', () => {
  assert.equal(codDifference({ expected_fen: 1000, actual_fen: 990, difference_fen: 10 }), '少缴 ¥0.10');
  assert.equal(codDifference({ expected_fen: 1000, actual_fen: 1010, difference_fen: -10 }), '多缴 ¥0.10');
  assert.equal(codDifference({ expected_fen: 1000, actual_fen: 1000, difference_fen: 0 }), '已对平');
});

test('a short payment never renders the reversed negative sign', () => {
  const label = codDifference({ expected_fen: 1000, actual_fen: 990, difference_fen: 10 });
  assert.equal(label, '少缴 ¥0.10');
  assert.equal(label.includes('-'), false);
  assert.equal(label.includes('¥-'), false);
});

test('a missing difference is reported, never guessed', () => {
  assert.equal(codDifference({ expected_fen: 1000, actual_fen: 990, difference_fen: null }), '差额缺失');
});

test('safe integer fen retain every cent without floating-point rounding', () => {
  const amount = 9_007_199_254_740_990;
  assert.equal(money(amount), '¥90071992547409.90');
  assert.equal(money(-amount), '¥-90071992547409.90');
  assert.equal(money(-1), '¥-0.01');
  assert.equal(money(0), '¥0.00');
  assert.equal(money(Number.MAX_SAFE_INTEGER), '¥90071992547409.91');
  assert.equal(codDifference({ expected_fen: 0, actual_fen: amount, difference_fen: -amount }), '多缴 ¥90071992547409.90');
});
