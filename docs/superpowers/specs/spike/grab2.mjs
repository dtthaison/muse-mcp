// node grab2.mjs <thread-url-substring|last> <outprefix> : list user/agent message structure and save every media attachment
import fs from 'node:fs';
const ts = (await (await fetch('http://127.0.0.1:9333/json')).json()).filter(t => t.type === 'page' && t.url.includes('/thread/'));
const t = process.argv[2] === 'last' ? ts[0] : ts.find(t => t.url.includes(process.argv[2]));
console.log('tab', t.url);
const ws = new WebSocket(t.webSocketDebuggerUrl); await new Promise(r => ws.onopen = r);
ws.onmessage = ev => { const m = JSON.parse(ev.data); if (m.id !== 1) return;
  if (m.result.exceptionDetails) { console.log(m.result.exceptionDetails); process.exit(1); }
  const v = m.result.result.value;
  console.log('transcript:', v.transcript);
  v.media.forEach((x, i) => { const f = `${process.argv[3]}-${i}.${x.mime.split('/')[1]}`; fs.writeFileSync(f, Buffer.from(x.b64, 'base64'));
    console.log('saved', f, x.mime, x.w + 'x' + x.h, x.dur ?? '', 'testid=' + x.tid, 'parentPath=' + x.where); });
  process.exit(0); };
ws.send(JSON.stringify({ id: 1, method: 'Runtime.evaluate', params: { awaitPromise: true, returnByValue: true, expression: `(async () => {
  const transcript = [...document.querySelectorAll('[aria-label^="User message"],[aria-label^="Assistant message"]')].map(e => e.getAttribute('aria-label').slice(0, 300));
  const media = [];
  for (const m of document.querySelectorAll('main img, main video, [data-testid^="hatch-chat-attachment"] img, [data-testid^="hatch-chat-attachment"] video')) {
    const src = m.currentSrc || m.src; if (!src || !src.startsWith('blob:') || media.some(x => x.src === src)) continue;
    const b = await (await fetch(src)).blob(); const u = new Uint8Array(await b.arrayBuffer()); let s = '';
    for (let i = 0; i < u.length; i += 0x8000) s += String.fromCharCode.apply(null, u.subarray(i, i + 0x8000));
    let where = [], e = m; for (let k = 0; k < 6 && e; k++, e = e.parentElement) where.push(e.getAttribute('data-testid') || e.getAttribute('aria-label')?.slice(0, 30) || e.tagName);
    media.push({ src, mime: b.type, b64: btoa(s), w: m.naturalWidth || m.videoWidth, h: m.naturalHeight || m.videoHeight, dur: m.duration, tid: m.closest('[data-testid]')?.getAttribute('data-testid'), where: where.join('<') });
  }
  return { transcript, media: media.map(({ src, ...x }) => x) }; })()` } }));
