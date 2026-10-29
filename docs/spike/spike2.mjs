// Throwaway spike: new muse.ai chat in a fresh tab, attach a reference image, send a prompt,
// wait for an image/video attachment in the reply and save it.
// Usage: node spike2.mjs <image|video> <ref-file> <out-file> <timeout-sec> "<prompt>"
import fs from 'node:fs';
import path from 'node:path';

const [kind, refFile, outFile, secs, prompt] = process.argv.slice(2);
const PORT = 9333;
const sleep = ms => new Promise(r => setTimeout(r, ms));
const t0 = Date.now();
const say = (...a) => console.log(`[${kind} ${Math.round((Date.now() - t0) / 1000)}s]`, ...a);

const tab = await (await fetch(`http://127.0.0.1:${PORT}/json/new?about:blank`, { method: 'PUT' })).json();
const ws = new WebSocket(tab.webSocketDebuggerUrl);
await new Promise((ok, bad) => { ws.onopen = ok; ws.onerror = bad; });
let nextId = 1;
const pending = new Map();
ws.onmessage = ev => {
  const m = JSON.parse(ev.data);
  if (m.id && pending.has(m.id)) { const p = pending.get(m.id); pending.delete(m.id); m.error ? p.bad(new Error(m.error.message)) : p.ok(m.result); }
};
const send = (method, params = {}) => new Promise((ok, bad) => {
  const id = nextId++; pending.set(id, { ok, bad }); ws.send(JSON.stringify({ id, method, params }));
});
const evaluate = async (expr, awaitPromise = false) => {
  const r = await send('Runtime.evaluate', { expression: expr, returnByValue: true, awaitPromise });
  if (r.exceptionDetails) throw new Error(r.exceptionDetails.exception?.description || r.exceptionDetails.text);
  return r.result.value;
};

const STATE = `(() => {
  const bubbles = [...document.querySelectorAll('div[class*="hatch-agent-bubble-bg"]')];
  const atts = [...document.querySelectorAll('[data-testid^="hatch-chat-attachment-presentation-"]')].map(a => {
    const v = a.querySelector('video'), i = a.querySelector('img'), m = v || i;
    return { tid: a.getAttribute('data-testid'), kind: v ? 'video' : (i ? 'image' : '?'),
             src: m ? (m.currentSrc || m.src || '') : '', w: m ? (m.videoWidth || m.naturalWidth || 0) : 0,
             h: m ? (m.videoHeight || m.naturalHeight || 0) : 0, dur: v ? v.duration : null,
             inAgent: !!a.closest('div[class*="hatch-agent-bubble-bg"]') };
  });
  return { url: location.href, hasTextarea: !!document.querySelector('textarea'),
           hydration: document.querySelector('[data-hatch-shell-hydration-state]')?.getAttribute('data-hatch-shell-hydration-state') ?? null,
           agentBubbles: bubbles.length, lastText: bubbles.length ? (bubbles.at(-1).innerText || '') : '',
           stop: !!document.querySelector('button[aria-label*="Stop" i]'),
           approval: !!document.querySelector('[data-testid="hatch-inline-approval-card"]'),
           atts, head: (document.body?.innerText || '').slice(0, 200) };
})()`;

await send('Page.enable');
await send('Runtime.enable');
await send('DOM.enable');
await send('Page.navigate', { url: 'https://muse.ai/thread/new' });
let st;
for (let i = 0; i < 120; i++) {
  await sleep(500);
  try { st = await evaluate(STATE); } catch { continue; }
  if (st.hasTextarea && st.hydration === 'hydrated' && !st.head.includes('Connecting...')) break;
}
if (!st?.hasTextarea) { say('page not ready', st?.head); process.exit(1); }
say('ready', st.url);

// attach the reference image through the real file input
const { root } = await send('DOM.getDocument', { depth: -1, pierce: true });
const { nodeId } = await send('DOM.querySelector', { nodeId: root.nodeId, selector: 'input[type="file"]' });
if (!nodeId) { say('no file input'); process.exit(1); }
await send('DOM.setFileInputFiles', { nodeId, files: [path.resolve(refFile)] });
await sleep(3000);
say('attached', path.basename(refFile), 'attachments on page:', (await evaluate(STATE)).atts.length);

await evaluate(`(() => { const ta = document.querySelector('textarea'); ta.focus();
  Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set.call(ta, ${JSON.stringify(prompt)});
  ta.dispatchEvent(new Event('input', { bubbles: true })); })()`);
let clicked = '';
for (let i = 0; i < 100 && clicked !== 'clicked'; i++) {
  clicked = await evaluate(`(() => { const b = [...document.querySelectorAll('button,[role=button]')].filter(b => b.offsetParent !== null)
    .find(b => /send/i.test((b.getAttribute('aria-label')||'') + ' ' + (b.getAttribute('data-testid')||'')));
    if (!b) return 'missing'; if (b.disabled) return 'disabled'; b.click(); return 'clicked'; })()`);
  if (clicked !== 'clicked') await sleep(200);
}
say('send:', clicked);
if (clicked !== 'clicked') process.exit(1);

const deadline = Date.now() + Number(secs) * 1000;
let last = '', doneAt = null;
while (Date.now() < deadline) {
  await sleep(3000);
  try { st = await evaluate(STATE); } catch (e) { say('eval error', e.message); continue; }
  if (st.approval) {
    const denied = await evaluate(`[...document.querySelectorAll('[data-testid="hatch-inline-approval-card"]')].map(c => {
      const b = [...c.querySelectorAll('button,[role=button]')].find(b => /^(deny|decline|reject|don.t allow)$/i.test((b.innerText||'').trim()));
      if (b) b.click(); return (c.innerText || '').slice(0, 200); })`);
    say('DENIED approval:', JSON.stringify(denied));
  }
  const results = st.atts.filter(a => a.kind === kind);
  const line = JSON.stringify({ bubbles: st.agentBubbles, stop: st.stop, atts: st.atts.map(a => `${a.kind}${a.inAgent ? '@agent' : '@user'}${a.src ? '' : '(loading)'}`), text: st.lastText.slice(-160) });
  if (line !== last) { say(line); last = line; }
  const ready = results.filter(a => a.src);
  if (ready.length && !st.stop) {
    doneAt ??= Date.now();
    if (Date.now() - doneAt < 4000) continue; // let the src settle
    const a = ready.at(-1);
    const r = await evaluate(`(async () => { const b = await (await fetch(${JSON.stringify(a.src)})).blob();
      const u = new Uint8Array(await b.arrayBuffer()); let s = '';
      for (let i = 0; i < u.length; i += 0x8000) s += String.fromCharCode.apply(null, u.subarray(i, i + 0x8000));
      return { mime: b.type, b64: btoa(s) }; })()`, true);
    fs.writeFileSync(outFile, Buffer.from(r.b64, 'base64'));
    say('SAVED', outFile, r.mime, Buffer.from(r.b64, 'base64').length, 'bytes', `${a.w}x${a.h}`, a.dur ? `${a.dur.toFixed(1)}s` : '');
    say('final text:', st.lastText.slice(0, 600));
    process.exit(0);
  }
}
say('TIMEOUT. final text:', st.lastText.slice(0, 600));
process.exit(2);
