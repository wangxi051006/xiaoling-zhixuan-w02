import assert from 'node:assert/strict';
import test from 'node:test';
import { codDifference } from '../src/shared/cod.ts';
import * as amounts from '../src/shared/money.ts';
const { money } = amounts;

// P08-F01: the stored ledger direction, not a client subtraction, decides the label.
test('an unverified batch has no difference at all', () => {
  assert.equal(codDifference({ expected_fen: 1000, actual_fen: null, difference_fen: null }), '尚未复点');
});

test('the label follows expected_fen minus actual_fen', () => {
  assert.equal(codDifference({ expected_fen: 1000, actual_fen: 990, difference_fen: 10 }), '少缴 0.1 块');
  assert.equal(codDifference({ expected_fen: 1000, actual_fen: 1010, difference_fen: -10 }), '多缴 0.1 块');
  assert.equal(codDifference({ expected_fen: 1000, actual_fen: 1000, difference_fen: 0 }), '已对平');
});

test('a short payment never renders the reversed negative sign', () => {
  const label = codDifference({ expected_fen: 1000, actual_fen: 990, difference_fen: 10 });
  assert.equal(label, '少缴 0.1 块');
  assert.equal(label.includes('-'), false);
});

test('a missing difference is reported, never guessed', () => {
  assert.equal(codDifference({ expected_fen: 1000, actual_fen: 990, difference_fen: null }), '差额缺失');
});

test('safe integer fen retain every cent without floating-point rounding', () => {
  const amount = 9_007_199_254_740_990;
  assert.equal(money(amount), '90071992547409.9 块');
  assert.equal(money(-amount), '-90071992547409.9 块');
  assert.equal(money(-1), '-0.01 块');
  assert.equal(money(0), '0 块');
  assert.equal(money(Number.MAX_SAFE_INTEGER), '90071992547409.91 块');
  assert.equal(codDifference({ expected_fen: 0, actual_fen: amount, difference_fen: -amount }), '多缴 90071992547409.9 块');
});

test('amount settings convert decimal inputs to exact integer fen', () => {
  assert.equal(amounts.parseMoney('10'), 1000);
  assert.equal(amounts.parseMoney('6.5'), 650);
  assert.equal(amounts.parseMoney('2.35'), 235);
  assert.equal(amounts.parseMoney('0'), 0);
  assert.equal(amounts.parseMoney('90071992547409.91'), Number.MAX_SAFE_INTEGER);
  for (const invalid of ['', '1.001', '1e2', '-1', '90071992547409.92']) {
    assert.equal(amounts.parseMoney(invalid), undefined);
  }
});
