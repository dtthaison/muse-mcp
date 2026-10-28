// Throwaway spike: drive the muse.ai tab over CDP and record its network traffic.
// Usage:
//   node spike.mjs state                 -> print login/page state
//   node spike.mjs send "<prompt>" [sec] -> new thread, send prompt, record for sec (default 240)
import fs from 'node:fs';
import path from 'node:path';

const PORT = 9333;
const OUT = path.dirname(new URL(import.meta.url).pathname);
const [mode = 'state', prompt = '', secs = '240'] = process.argv.slice(2);

const targets = await (await fetch(`http://127.0.0.1:${PORT}/json`)).json();
const page = targets.find(t => t.type === 'page' && t.url.includes('about:blank')) || targets.find(t => t.type === 'page' && t.url.includes('muse.ai')) || targets.find(t => t.type === 'page');
if (!page) throw new Error('no page target');

const ws = new WebSocket(page.webSocketDebuggerUrl);
await new Promise((ok, bad) => { ws.onopen = ok; ws.onerror = bad; });
let nextId = 1;
const pending = new Map();
const listeners = [];
ws.onmessage = ev => {
  const m = JSON.parse(ev.data);
  if (m.id && pending.has(m.id)) { const p = pending.get(m.id); pending.delete(m.id); m.error ? p.bad(new Error(m.error.message)) : p.ok(m.result); }
  else if (m.method) listeners.forEach(fn => fn(m.method, m.params));
};
const send = (method, params = {}) => new Promise((ok, bad) => {
  const id = nextId++; pending.set(id, { ok, bad }); ws.send(JSON.stringify({ id, method, params }));
});
const evaluate = async (expr, awaitPromise = false) => {
  const r = await send('Runtime.evaluate', { expression: expr, returnByValue: true, awaitPromise });
  if (r.exceptionDetails) throw new Error(r.exceptionDetails.text);
  return r.result.value;
};
const sleep = ms => new Promise(r => setTimeout(r, ms));

const STATE = `(() => {
  const body = document.body ? document.body.innerText : '';
  const bubbles = [...document.querySelectorAll('div[class*="hatch-agent-bubble-bg"]')];
  const atts = [...document.querySelectorAll('[data-testid^="hatch-chat-attachment-presentation-"]')].map(a => {
    const m = a.querySelector('video') || a.querySelector('img');
    return { tid: a.getAttribute('data-testid'), kind: m ? m.tagName : '?', src: m ? (m.currentSrc || m.src || '').slice(0, 200) : '' };
  });
  return { url: location.href, hasTextarea: !!document.querySelector('textarea'),
           hydration: document.querySelector('[data-hatch-shell-hydration-state]')?.getAttribute('data-hatch-shell-hydration-state') ?? null,
           agentBubbles: bubbles.length, lastText: bubbles.length ? (bubbles.at(-1).innerText || '').slice(-400) : '',
           stopButton: !!document.querySelector('button[aria-label*="Stop" i]'),
           approval: !!document.querySelector('[data-testid="hatch-inline-approval-card"]'),
           attachments: atts, head: body.slice(0, 300) };
})()`;

if (mode === 'state') {
  console.log(JSON.stringify(await evaluate(STATE), null, 2));
  process.exit(0);
}

// ---- record network
const log = fs.createWriteStream(path.join(OUT, `log-${Date.now()}.jsonl`));
const SECRET = /^(cookie|set-cookie|authorization|x-.*token.*)$/i;
const scrub = h => Object.fromEntries(Object.entries(h || {}).map(([k, v]) => [k, SECRET.test(k) ? '<redacted>' : v]));
const STATIC = /\.(js|css|woff2?|png|jpe?g|svg|ico|webp|gif|map)(\?|$)/i;
const reqs = new Map();
const rec = o => log.write(JSON.stringify({ t: Date.now(), ...o }) + '\n');
listeners.push(async (method, p) => {
  switch (method) {
    case 'Network.requestWillBeSent': {
      const u = p.request.url;
      if (u.startsWith('data:') || (STATIC.test(u) && p.type !== 'Fetch' && p.type !== 'XHR')) return;
      reqs.set(p.requestId, { url: u, type: p.type });
      rec({ ev: 'req', id: p.requestId, type: p.type, method: p.request.method, url: u,
            headers: scrub(p.request.headers), body: p.request.postData?.slice(0, 4000) });
      break;
    }
    case 'Network.responseReceived':
      if (!reqs.has(p.requestId)) return;
      rec({ ev: 'resp', id: p.requestId, status: p.response.status, mime: p.response.mimeType, headers: scrub(p.response.headers) });
      break;
    case 'Network.loadingFinished': {
      const r = reqs.get(p.requestId);
      if (!r || !['Fetch', 'XHR', 'EventSource'].includes(r.type)) return;
      try {
        const b = await send('Network.getResponseBody', { requestId: p.requestId });
        rec({ ev: 'body', id: p.requestId, url: r.url, b64: b.base64Encoded, body: b.base64Encoded ? `<${b.body.length} b64 chars>` : b.body.slice(0, 8000) });
      } catch (e) { rec({ ev: 'body-err', id: p.requestId, err: e.message }); }
      break;
    }
    case 'Network.eventSourceMessageReceived':
      rec({ ev: 'sse', id: p.requestId, name: p.eventName, data: p.data.slice(0, 2000) });
      break;
    case 'Network.webSocketCreated':
      rec({ ev: 'ws-open', id: p.requestId, url: p.url });
      break;
    case 'Network.webSocketFrameSent':
    case 'Network.webSocketFrameReceived':
      rec({ ev: method.endsWith('Sent') ? 'ws-out' : 'ws-in', id: p.requestId, op: p.response.opcode, data: (p.response.payloadData || '').slice(0, 3000) });
      break;
  }
});
await send('Network.enable', { maxPostDataSize: 65536 });
await send('Page.enable');
await send('Runtime.enable');

// ---- new thread, send prompt
await send('Page.navigate', { url: 'https://muse.ai/thread/new' });
let st;
for (let i = 0; i < 120; i++) {
  await sleep(500);
  try { st = await evaluate(STATE); } catch { continue; }
  if (st.hasTextarea && st.hydration !== 'hydrated' && st.hydration !== null) continue;
  if (st.hasTextarea && !st.head.includes('Connecting...')) break;
}
console.log('ready:', JSON.stringify({ url: st.url, hasTextarea: st.hasTextarea, hydration: st.hydration }));
if (!st.hasTextarea) { console.log(st.head); process.exit(1); }

const filled = await evaluate(`(() => {
  const ta = document.querySelector('textarea'); ta.focus();
  Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set.call(ta, ${JSON.stringify(prompt)});
  ta.dispatchEvent(new Event('input', { bubbles: true })); return ta.value.length; })()`);
let clicked = 'missing';
for (let i = 0; i < 50 && clicked !== 'clicked'; i++) {
  clicked = await evaluate(`(() => { const b = [...document.querySelectorAll('button,[role=button]')].filter(b => b.offsetParent !== null)
    .find(b => /send/i.test((b.getAttribute('aria-label')||'') + ' ' + (b.getAttribute('data-testid')||'')));
    if (!b) return 'missing'; if (b.disabled) return 'disabled'; b.click(); return 'clicked'; })()`);
  if (clicked !== 'clicked') await sleep(100);
}
console.log('filled', filled, 'chars; send button:', clicked);
rec({ ev: 'mark', note: 'sent', clicked });

const end = Date.now() + Number(secs) * 1000;
let last = '';
while (Date.now() < end) {
  await sleep(3000);
  try { st = await evaluate(STATE); } catch (e) { console.log('eval error', e.message); continue; }
  const line = JSON.stringify({ s: Math.round((Date.now() - (end - secs * 1000)) / 1000), url: st.url, bubbles: st.agentBubbles, stop: st.stopButton, approval: st.approval, atts: st.attachments.length, text: st.lastText.slice(-120) });
  if (line.replace(/"s":\d+,/, '') !== last) { console.log(line); last = line.replace(/"s":\d+,/, ''); }
  rec({ ev: 'dom', ...st });
  if (st.attachments.some(a => a.src) && !st.stopButton) { console.log('attachments:', JSON.stringify(st.attachments)); await sleep(5000); break; }
}
log.end();
console.log('log written to', log.path);
process.exit(0);
