import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import vm from 'node:vm';

function load(capable = true) {
  const els = new Map(), listeners = new Map(), vibrations = [], speech = [];
  const make = () => ({ dataset: {}, classList: { add() {}, remove() {}, toggle() {}, contains() { return false; } }, style: {}, addEventListener() {}, setAttribute() {}, querySelector() { return null; } });
  const document = {
    hidden: false,
    querySelector(key) { if (!els.has(key)) els.set(key, make()); return els.get(key); },
    querySelectorAll() { return []; },
    getElementById(id) { return els.get('#' + id) || null; },
    addEventListener(type, handler) { listeners.set(type, handler); }
  };
  const env = {
    document, location: { hash: '' }, history: { replaceState() {} },
    addEventListener() {}, requestAnimationFrame() {}, setInterval() {}, setTimeout() {}, clearTimeout() {},
    navigator: capable ? { vibrate(pattern) { if (pattern) vibrations.push(Array.from(pattern)); return true; } } : {},
    ...(capable ? {
      speechSynthesis: { speak(utterance) { speech.push(utterance.text); }, cancel() {} },
      SpeechSynthesisUtterance: function (text) { this.text = text; }
    } : {})
  };
  env.window = env;
  const ctx = vm.createContext(env);
  const scripts = [...readFileSync(new URL('index.html', import.meta.url), 'utf8').matchAll(/<script src="([^"]+)"/g)];
  for (const [, file] of scripts) vm.runInContext(readFileSync(new URL(file, import.meta.url), 'utf8'), ctx, { filename: file });
  env.render();
  return { ...env, listeners, vibrations, speech, screen: () => els.get('#screen').innerHTML };
}

test('order alert is targeted, delivered once, and can be acknowledged', () => {
  const app = load();
  app.switchClient('delivery');
  assert.doesNotMatch(app.screen(), />急<\/span>/);
  app.ACTIONS.alertTest();
  app.speech.length = 0;
  app.vibrations.length = 0;
  app.switchClient('user');
  app.ACTIONS.urge('A2381');
  assert.equal(app.speech.length, 0);
  app.switchClient('delivery');
  assert.match(app.screen(), /data-act="ackUrge" data-val="A2381"/);
  assert.equal(app.speech.length, 1);
  assert.doesNotMatch(app.speech[0], /508|A2381/);
  assert.deepEqual(app.vibrations, [[170, 100, 170]]);
  app.ACTIONS.refreshTasks(false);
  app.switchClient('user');
  app.switchClient('delivery');
  assert.equal(app.speech.length, 1);
  app.ACTIONS.ackUrge('A2381');
  assert.match(app.screen(), /urge-history/);
  assert.doesNotMatch(app.screen(), />急<\/span>/);
  app.ACTIONS.dStep();
  app.ACTIONS.urge('A2381');
  assert.doesNotMatch(app.screen(), /data-act="ackUrge"/);
  assert.equal(app.speech.length, 1);
});

test('text fallback works without device APIs; closed orders do not alert', () => {
  const app = load(false);
  app.ACTIONS.urge('A2377');
  app.switchClient('delivery');
  assert.doesNotMatch(app.screen(), /data-act="ackUrge"/);
  app.ACTIONS.urge('A2381');
  assert.match(app.screen(), /data-act="ackUrge" data-val="A2381"/);
});

test('cooldown, page visibility, and independent voice preference', () => {
  const app = load();
  app.switchClient('delivery');
  app.ACTIONS.alertTest();
  app.ACTIONS.alertToggle('voice');
  app.speech.length = 0;
  app.vibrations.length = 0;
  app.document.hidden = true;
  app.ACTIONS.urge('A2381');
  app.ACTIONS.urge('A2381');
  assert.equal(app.unreadMsg(), 1);
  assert.equal(app.vibrations.length, 0);
  app.document.hidden = false;
  app.listeners.get('visibilitychange')();
  assert.deepEqual(app.vibrations, [[170, 100, 170]]);
  assert.equal(app.speech.length, 0);
});

test('unassigned order only reveals its alert after assignment', () => {
  const app = load();
  app.state.orders.unshift({ no: 'A2384', status: '备货中', rider: '待分配', items: [], logs: [] });
  app.ACTIONS.urge('A2384');
  app.switchClient('delivery');
  assert.doesNotMatch(app.screen(), /data-act="ackUrge"/);
  app.ACTIONS.dStep();
  app.ACTIONS.finishTask();
  app.ACTIONS.accept('T1');
  assert.match(app.screen(), /data-act="ackUrge" data-val="A2384"/);
});

test('cancelled order no longer appears as an urgent or active rider task', () => {
  const app = load();
  app.ACTIONS.urge('A2381');
  app.ACTIONS.doCancel('A2381');
  app.switchClient('delivery');
  assert.doesNotMatch(app.screen(), /A2381/);
  assert.equal(app.unreadMsg(), 0);
});
