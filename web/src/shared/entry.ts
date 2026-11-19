import { createApp, h } from 'vue';
import { assertNoProductionMock } from './api.ts';
import './entry.css';

const labels = { user: '用户端', rider: '骑手端', admin: '管理端' } as const;
type Entry = keyof typeof labels;

assertNoProductionMock(import.meta.env.PROD, import.meta.env.VITE_ENABLE_MOCK);
const root = document.querySelector<HTMLElement>('#app');
const entry = root?.dataset.entry;
if (!root || !entry || !Object.hasOwn(labels, entry)) throw new Error('Invalid application entry');

createApp({
  render: () => h('main', { 'data-entry': entry }, [
    h('header', [h('span', '校零智选'), h('h1', labels[entry as Entry])]),
    h('p', { role: 'status' }, '服务尚未开放'),
  ]),
}).mount(root);
