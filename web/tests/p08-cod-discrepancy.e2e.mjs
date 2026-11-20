import test from 'node:test';
import assert from 'node:assert/strict';
import { resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
import { writeFile } from 'node:fs/promises';

const { startHarness } = await import(pathToFileURL(resolve('tests/p06-harness.mjs')));

// P08-F01 / PAY-02. cod_batch_verifications.difference_fen is generated as
// expected_fen - actual_fen: positive is short-paid, negative is over-paid and
// zero is balanced. The API returns that column and the admin page only labels
// it. The page must never subtract the two amounts itself, and an unverified
// batch has no difference at all rather than a zero one.
const scenarios = [
  { name: 'unverified', amount: null, status: 'IN_BATCH', delta: null, label: '尚未复点', ledgerFen: 0, countedFen: '0' },
  { name: 'short-paid', amount: 990, status: 'DISCREPANCY', delta: 10, label: '少缴 0.1 块', ledgerFen: 10, countedFen: '990' },
  { name: 'over-paid', amount: 1010, status: 'DISCREPANCY', delta: -10, label: '多缴 0.1 块', ledgerFen: -10, countedFen: '1010' },
  { name: 'balanced', amount: 1000, status: 'VERIFIED', delta: 0, label: '已对平', ledgerFen: 0, countedFen: '1000' },
];

for (const scenario of scenarios) {
  test('PAY-02 ' + scenario.name + ': API, ledger and both viewports agree on the difference', async () => {
    const h = await startHarness();
    const call = async (actor, method, path, body, status = 200) => {
      const response = await h.request(actor, method, path, body);
      assert.equal(response.status, status, method + ' ' + path);
      return response.body;
    };
    try {
      assert.equal((await h.control('balance', { Enabled: false })).enabled, false);
      const [user, rider, op, maker, checker] = await Promise.all([
        h.actor('buyer1', 'user'), h.actor('r1', 'rider'), h.actor('op', 'admin'), h.actor('fin', 'admin'), h.actor('fin2', 'admin'),
      ]);
      assert.notEqual((await maker.me()).body.id, (await checker.me()).body.id, 'maker and checker are different finance actors');
      const quote = await call(user, 'POST', '/checkout/quote', { lines: [{ sku_id: h.info.sku, qty: 2 }], address_id: 'a1', delivery_mode: 'ASAP', payment_method: 'COD' }, 201);
      const order = await call(user, 'POST', '/orders', { quote_id: quote.quote_id }, 201);
      assert.equal(order.settlement_status, 'COD_UNCOLLECTED');
      await call(op, 'POST', '/admin/orders/' + order.id + '/prepare', { sorted: true });
      await call(op, 'POST', '/admin/orders/' + order.id + '/dispatch');
      await call(rider, 'PUT', '/rider/availability', { available: true });
      const tasks = await call(rider, 'GET', '/rider/tasks');
      assert.equal(tasks.items.length, 1);
      for (const action of ['claim', 'pickup', 'deliver']) {
        await call(rider, 'POST', '/rider/tasks/' + tasks.items[0].id + '/' + action, action === 'deliver' ? { cod_received: true } : undefined);
      }
      const receipts = await call(maker, 'GET', '/admin/cod/receipts');
      assert.equal(receipts.items.length, 1);
      const receipt = receipts.items[0];
      assert.equal(receipt.order_id, order.id);
      assert.equal(receipts.items[0].amount_fen, 1000, 'two 500-fen units are the synthetic receipt seed');

      await maker.page.getByRole('button', { name: 'COD 财务', exact: true }).click();
      await maker.page.getByLabel('上缴骑手', { exact: true }).selectOption('r1');
      await maker.page.getByLabel('凭证 ' + receipts.items[0].id, { exact: true }).check();
      const created = maker.page.waitForResponse(r => new URL(r.url()).pathname === '/api/v1/admin/cod/batches' && r.request().method() === 'POST');
      await maker.page.getByTestId('create-cod-batch').click();
      const createdResponse = await created;
      assert.equal(createdResponse.status(), 201);
      const batch = await createdResponse.json();
      assert.equal(batch.expected_fen, 1000);
      assert.equal(batch.actual_fen, null);
      assert.equal(batch.difference_fen, null, 'an unverified batch has no difference, not a zero one');
      assert.equal(batch.status, 'IN_BATCH');
      assert.equal(await maker.page.getByTestId('verify-cod-batch').isDisabled(), true, 'the maker cannot verify its own batch');

      let verified = batch;
      if (scenario.amount === null) {
        await maker.page.getByTestId('cod-batch-difference').getByText('尚未复点', { exact: true }).waitFor();
      } else {
        await call(maker, 'POST', '/admin/cod/batches/' + batch.id + '/verify', { actual_fen: scenario.amount, reason_code: 'COUNTED' }, 409);
        await checker.page.getByRole('button', { name: 'COD 财务', exact: true }).click();
        await checker.page.getByLabel('批次号', { exact: true }).fill(batch.id);
        await checker.page.getByLabel('实际清点金额（块）', { exact: true }).fill((scenario.amount / 100).toFixed(2));
        await checker.page.getByLabel('复点原因码', { exact: true }).fill('COUNTED');
        const verification = checker.page.waitForResponse(r => new URL(r.url()).pathname.endsWith('/' + batch.id + '/verify'));
        await checker.page.getByTestId('verify-cod-batch').click();
        const response = await verification;
        assert.equal(response.status(), 200);
        verified = await response.json();
      }
      assert.equal(verified.id, batch.id);
      assert.equal(verified.expected_fen, 1000);
      assert.equal(verified.actual_fen, scenario.amount);
      assert.equal(verified.status, scenario.status);
      assert.equal(verified.difference_fen, scenario.delta, 'the API difference must be expected_fen - actual_fen');

      const ledgerRow = await h.control('cod-ledger-row', { OrderID: order.id });
      const fenText = value => value === null ? null : String(value);
      assert.deepEqual(ledgerRow, {
        order_id: order.id,
        receipt_id: receipt.id,
        batch_id: batch.id,
        receipt_amount_fen: String(receipt.amount_fen),
        expected_fen: String(verified.expected_fen),
        actual_fen: fenText(verified.actual_fen),
        difference_fen: fenText(verified.difference_fen),
        status: verified.status,
      }, 'the persisted receipt membership and verification must match the API');

      const snapshot = await h.control('snapshot');
      assert.equal(snapshot.receipts, 1, 'exactly one receipt is collected');
      assert.equal(snapshot.earnings, 1, 'the rider is paid exactly once');
      assert.equal(snapshot.verified_batches, scenario.amount === null ? 0 : 1, 'an unverified batch has no verification row');
      assert.equal(Number(snapshot.cod.BatchExpectedFen), 1000);
      assert.equal(snapshot.cod.CountedFen, scenario.countedFen);
      assert.equal(Number(snapshot.cod.DifferenceFen), scenario.ledgerFen, 'the ledger direction must match the API');
      if (scenario.delta !== null) {
        assert.equal(Number(snapshot.cod.DifferenceFen), verified.expected_fen - verified.actual_fen, 'the ledger stores expected_fen - actual_fen');
      } else {
        assert.equal(Number(snapshot.cod.PendingCountFen), 1000, 'the uncounted batch stays pending');
      }

      const holder = scenario.amount === null ? maker : checker;
      const result = holder.page.getByTestId('cod-batch-result');
      const seen = [];
      for (const width of [1440, 390]) {
        await h.shot(holder, 'p08-' + scenario.name, width);
        const label = result.getByTestId('cod-batch-difference');
        await label.getByText(scenario.label, { exact: true }).waitFor();
        seen.push((await label.textContent()).trim());
        if (scenario.delta !== null) {
          assert.equal(await result.getByText('-' + Math.abs(scenario.delta) / 100 + ' 块', { exact: false }).count(), 0, 'the reversed sign must not appear in the result card');
        }
      }
      assert.deepEqual(seen, [scenario.label, scenario.label], 'both viewports show the same label and amount');

      await writeFile(resolve(h.evidence, 'p08-' + scenario.name + '.json'), JSON.stringify({
        scenario: scenario.name, amountFen: scenario.amount, viewports: [1440, 390], uiLabels: seen,
        createdBatch: batch, verifiedBatch: verified, ledgerRow, ledgerSnapshot: snapshot, schema: h.info.schema,
      }, null, 2));
    } finally {
      await h.close();
    }
  });
}
