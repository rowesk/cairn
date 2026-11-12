(() => {
 // Keep unsaved writing in this browser, so a reload, a crash or going back
 // restores it. Each page has its own draft; nothing leaves the device and
 // saving clears it. An edited page only keeps a draft that differs from
 // what is saved.
 const form = document.querySelector('#editor-form'), title = document.querySelector('#page-title'), body = document.querySelector('#markdown'), slug = document.querySelector('#page-slug'), images = document.querySelector('#pasted-images');
 // Edits keep their draft by page id, so a deleted page's draft never fills
 // a new page that reuses its link name.
 const key = 'cairn.draft:' + (form.dataset.draftKey || location.pathname), editing = 'page' in form.dataset, base = form.dataset.draftBase || '';
 let store = null;
 try { store = window.localStorage; store.getItem(key); } catch { store = null; }
 if (!store) return;
 // Drafts last 30 days. Cairn names pages deleted in this browser in the
 // cairn_deleted cookie, and their drafts go too.
 try {
  const gone = (document.cookie.match(/(?:^|; )cairn_deleted=([0-9a-f.]*)/) || [])[1];
  if (gone) {
   for (const id of gone.split('.')) if (id) store.removeItem('cairn.draft:page:' + id);
   document.cookie = 'cairn_deleted=; Max-Age=0; Path=/; SameSite=Strict';
  }
  for (let i = store.length - 1; i >= 0; i--) {
   const name = store.key(i);
   if (!name || !name.startsWith('cairn.draft:')) continue;
   let saved = 0;
   try { saved = JSON.parse(store.getItem(name)).saved || 0; } catch {}
   if (Date.now() - saved > 30 * 864e5) store.removeItem(name);
  }
 } catch {}
 const read = () => { try { return JSON.parse(store.getItem(key) || 'null'); } catch { return null; } };
 const access = () => document.querySelector('[name=access]:checked')?.value || 'private';
 const saved = {title: title.defaultValue, markdown: body.defaultValue};
 let sent = false, restored = false;
 const unchanged = () => editing ? title.value === saved.title && body.value === saved.markdown && (images.value === '' || images.value === '{}') : !title.value.trim() && !body.value.trim();
 const write = () => {
  if (sent) return;
  if (unchanged()) { try { store.removeItem(key); } catch {} return; }
  const draft = {title: title.value, markdown: body.value, slug: slug.value, access: access(), images: images.value, saved: Date.now(), base};
  try { store.setItem(key, JSON.stringify(draft)); } catch {
   // Pasted images can outgrow the browser's storage; keep the words.
   try { draft.images = ''; draft.imagesDropped = images.value !== '' && images.value !== '{}'; store.setItem(key, JSON.stringify(draft)); } catch {}
  }
 };
 const draft = read();
 const differs = draft && (draft.title || draft.markdown) && (editing ? draft.title !== title.value || draft.markdown !== body.value : !title.value && !body.value);
 const errorsShown = form.querySelector('[aria-invalid="true"]');
 if (differs && !errorsShown) {
  title.value = draft.title || ''; body.value = draft.markdown || ''; slug.value = draft.slug || '';
  if (draft.images) images.value = draft.images;
  const choice = document.querySelector('[name=access][value="' + (draft.access === 'public' ? 'public' : 'private') + '"]');
  if (choice && !choice.disabled) choice.checked = true;
  const when = new Date(draft.saved), today = new Date().toDateString() === when.toDateString();
  const time = cairnTime(when);
  let text = 'Draft restored from ' + (today ? time : cairnDate(when.toISOString()).day.replace(/ \d{4}$/, '') + ' at ' + time) + '.';
  if (draft.imagesDropped) text += ' Pasted images were too large to keep, so paste them again.';
  if (editing && draft.base && draft.base !== base) text += ' The page has changed since, so check it before you save.';
  // The note is the page's own toast, in the toast region like every other.
  const discard = () => { try { store.removeItem(key); } catch {} sent = true; location.replace(location.pathname); };
  const note = cairnToast({kind: 'info', text, actions: [{label: 'Discard draft', onClick: discard}]});
  if (note) note.id = 'draft-note';
  restored = true;
  form.dispatchEvent(new Event('cairn:restored'));
 }
 let timer = 0;
 form.addEventListener('input', () => { clearTimeout(timer); timer = setTimeout(write, 400); });
 form.addEventListener('change', write);
 window.addEventListener('pagehide', () => { clearTimeout(timer); write(); });
 window.addEventListener('pageshow', event => { if (event.persisted) sent = false; });
 form.addEventListener('submit', event => {
  if (event.defaultPrevented || event.submitter?.id === 'preview-button') return;
  // A failed save shows the form again with the writing, which saves anew.
  sent = true; clearTimeout(timer); try { store.removeItem(key); } catch {}
 });
 if (restored) write();
})();

(() => {
 const form = document.querySelector('#editor-form'), title = document.querySelector('#page-title'), body = document.querySelector('#markdown');
 const frame = document.querySelector('#editor-preview'), previewButton = document.querySelector('#preview-button'), writeButton = document.querySelector('#write-button'), splitButton = document.querySelector('#split-button');
 const wide = matchMedia('(min-width: 1200px)');
 const keysOff = () => document.documentElement.dataset.shortcuts === 'off';
 const fit = area => {
  if (CSS.supports('field-sizing', 'content')) return;
  area.style.height = 'auto';
  area.style.height = area.scrollHeight + 2 + 'px';
 };
 const fitAll = () => { fit(title); fit(body); form.dispatchEvent(new Event('cairn:fitted')); };
 let dirty = false, mode = 'write';
 if (keysOff()) document.querySelectorAll('#editor-form [aria-keyshortcuts]').forEach(el => el.removeAttribute('aria-keyshortcuts'));

 // The title wraps but stays one line of text: Enter moves to the writing.
 title.addEventListener('keydown', event => { if (event.key === 'Enter' && !event.isComposing) { event.preventDefault(); body.focus(); } });
 title.addEventListener('input', () => {
  if (/[\r\n]/.test(title.value)) title.value = title.value.replace(/[\r\n]+/g, ' ');
  fit(title);
 });
 body.addEventListener('input', () => fit(body));
 window.addEventListener('resize', fitAll);
 form.addEventListener('cairn:restored', fitAll);
 fitAll();

 // Write, Preview and, on wide screens, both side by side. All three keep
 // the same toolbar and the same title band over the preview.
 writeButton.hidden = false;
 const bandTitle = () => { document.querySelector('#preview-title').textContent = title.value.trim() || 'Untitled page'; };
 const setMode = (next, remember) => {
  if (next === 'split' && !wide.matches) next = 'preview';
  mode = next;
  form.dataset.mode = next;
  writeButton.setAttribute('aria-pressed', String(next === 'write'));
  previewButton.setAttribute('aria-pressed', String(next === 'preview'));
  splitButton.setAttribute('aria-pressed', String(next === 'split'));
  bandTitle();
  if (remember) try { localStorage.setItem('cairn.editor.split', next === 'split' ? '1' : ''); } catch {}
  if (next !== 'preview') fitAll();
 };
 const preview = () => form.requestSubmit(previewButton);
 const showWide = () => {
  splitButton.hidden = !wide.matches;
  if (!wide.matches && mode === 'split') setMode('write');
 };
 wide.addEventListener('change', showWide);
 showWide();
 setMode('write');
 writeButton.addEventListener('click', () => { setMode('write', true); body.focus({preventScroll: true}); });
 splitButton.addEventListener('click', () => {
  setMode(mode === 'split' ? 'write' : 'split', true);
  if (mode === 'split') preview();
 });
 form.addEventListener('submit', event => {
  if (event.defaultPrevented) return;
  if (event.submitter === previewButton) {
   if (mode === 'write') setMode('preview', true);
   bandTitle();
  } else dirty = false;
 });
 // Side by side, the preview and its title follow the writing.
 let refresh = 0;
 form.addEventListener('input', event => {
  dirty = true;
  if (mode === 'split' && event.target !== document.querySelector('#page-slug')) {
   if (event.target === title) bandTitle();
   clearTimeout(refresh);
   refresh = setTimeout(preview, 700);
  }
 });
 // The preview takes the theme's report colours, and grows to its page like the reader.
 cairnFollowScheme(frame);
 window.addEventListener('message', event => {
  if (event.source !== frame.contentWindow || event.data?.type !== 'cairn:report-height') return;
  const height = Number(event.data.height);
  if (Number.isFinite(height) && height > 0) frame.style.height = Math.max(320, Math.min(height, 200000)) + 'px';
 });
 window.addEventListener('beforeunload', event => { if (dirty) { event.preventDefault(); event.returnValue = ''; } });
 // Command or Control S saves and P switches the preview, unless Settings
 // turned Cairn's shortcuts off; then the browser's own keys work.
 document.addEventListener('keydown', event => {
  if (keysOff() || !(event.metaKey || event.ctrlKey) || event.altKey) return;
  const key = event.key.toLowerCase();
  if (key === 's') { event.preventDefault(); form.requestSubmit(document.querySelector('#save-button')); }
  else if (key === 'p') {
   event.preventDefault();
   if (mode === 'write') preview();
   else { setMode('write'); body.focus({preventScroll: true}); }
  }
 });
 try { if (localStorage.getItem('cairn.editor.split') === '1' && wide.matches && (title.value || body.value)) { setMode('split'); preview(); } } catch {}
})();

(() => {
 // Errors beside their fields, in Cairn's words, instead of browser bubbles.
 // Each sits under its own field; focus moves to the first one to fix.
 const form = document.querySelector('#editor-form'), panel = document.querySelector('#sharing-panel');
 const fields = {title: document.querySelector('#page-title'), markdown: document.querySelector('#markdown'), slug: document.querySelector('#page-slug')};
 const linkRule = 'Use letters, numbers, dashes or underscores, starting with a letter or number, up to 64 characters.';
 const show = (name, message) => cairnFields.show(fields[name], message);
 const clear = name => cairnFields.clear(fields[name]);
 window.cairnFieldError = {show, clear};
 const validSlug = value => value === '' || /^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$/.test(value);
 const addressShown = () => !document.querySelector('#address-field').hidden;
 for (const name of ['title', 'markdown']) {
  fields[name].addEventListener('input', () => { if (fields[name].getAttribute('aria-invalid') === 'true' && fields[name].value.trim()) clear(name); });
 }
 form.addEventListener('submit', event => {
  if (event.submitter?.id === 'preview-button') return;
  const problems = [];
  if (!fields.title.value.trim()) problems.push(['title', 'Give the page a title.']);
  if (!fields.markdown.value.trim()) problems.push(['markdown', 'Write something before you save the page.']);
  if (addressShown() && !validSlug(fields.slug.value.trim())) problems.push(['slug', linkRule]);
  else if (addressShown() && fields.slug.getAttribute('aria-invalid') === 'true') problems.push(['slug', document.querySelector('#page-slug-error span')?.textContent || linkRule]);
  if (!problems.length) return;
  event.preventDefault();
  event.stopImmediatePropagation();
  problems.forEach(([name, message]) => show(name, message));
  const [first] = problems[0];
  if (first === 'slug') panel.showPopover();
  document.documentElement.dataset.inputModality = 'keyboard';
  fields[first].focus();
 }, true);
})();

(() => {
 const body = document.querySelector('#markdown'), field = document.querySelector('#pasted-images'), status = document.querySelector('#image-status');
 let images = JSON.parse(field.value || '{}'), pending = 0;
 const form = document.querySelector('#editor-form'), panel = document.querySelector('#sharing-panel'), trigger = document.querySelector('#access-trigger');
 const slug = document.querySelector('#page-slug'), addressField = document.querySelector('#address-field'), full = document.querySelector('#address-full');
 const editing = 'page' in panel.dataset, page = panel.dataset.page || '', host = 'http://' + panel.dataset.host + '/';
 const publicBase = panel.dataset.publicBase ? panel.dataset.publicBase.replace(/\/$/, '') + '/' : '';
 const choices = [...document.querySelectorAll('[name=access]')];
 const linkRule = 'Use letters, numbers, dashes or underscores, starting with a letter or number, up to 64 characters.';
 const shared = () => choices.some(el => el.checked && el.value === 'public');
 const say = (prefix, address) => { full.replaceChildren(prefix, Object.assign(document.createElement('strong'), {textContent: address})); };
 // The full address the page will have, checked against other pages while
 // typing. A name that can't be a link name, or that another page already
 // uses, never shows as an address: only the error under the field does.
 // Private or Shared, the line names the address the page will really get.
 let check = 0, asked = 0;
 const describe = () => {
  const base = shared() ? publicBase : host, name = slug.value.trim();
  clearTimeout(check);
  asked++;
  if (editing && !shared()) { say('This page stays at ', host + page); return; }
  if (!name) {
   if (editing) say('The page will be at ', base + (slug.placeholder || page));
   else full.textContent = 'Cairn picks a short link name when you save. Type one to choose it.';
   window.cairnFieldError.clear('slug');
   return;
  }
  if (!/^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$/.test(name)) {
   full.textContent = '';
   window.cairnFieldError.show('slug', linkRule);
   return;
  }
  // Until the check answers, a name already refused stays without an address.
  if (slug.getAttribute('aria-invalid') === 'true') full.textContent = '';
  else say('The page will be at ', base + name);
  check = setTimeout(async () => {
   const ask = ++asked;
   try {
    const params = new URLSearchParams({name, access: shared() ? 'shared' : 'private'});
    if (page) params.set('page', page);
    const response = await fetch('/_library/link-name?' + params, {headers: {'X-Cairn-Token': panel.dataset.csrf}});
    const answer = await response.json();
    if (ask !== asked || slug.value.trim() !== name) return;
    if (answer.ok) {
     window.cairnFieldError.clear('slug');
     say('The page will be at ', (shared() ? publicBase : host) + name);
    } else {
     full.textContent = '';
     window.cairnFieldError.show('slug', answer.message);
    }
   } catch {}
  }, 250);
 };
 const updateAccess = () => {
  const on = shared();
  document.querySelector('#save-access').textContent = 'Markdown · ' + (on ? 'Shared' : 'Private') + (editing ? '' : ' when saved');
  document.querySelector('#access-label').textContent = on ? 'Shared' : 'Private';
  if (!editing) document.querySelector('#save-rest').textContent = on ? ' & share' : ' page';
  document.querySelector('#address-origin').textContent = (on && publicBase ? new URL(publicBase).host : panel.dataset.host) + '/';
  trigger.classList.toggle('is-public', on);
  document.querySelector('#access-private-icon').hidden = on;
  document.querySelector('#access-public-icon').hidden = !on;
  if (editing) addressField.hidden = !on;
  describe();
  // Save and the access button change width with the words; keep the panel on its button.
  if (panel.matches(':popover-open')) requestAnimationFrame(() => positionPanel());
 };
 choices.forEach(el => el.addEventListener('change', updateAccess));
 updateAccess();
 slug.addEventListener('input', describe);
 form.addEventListener('cairn:restored', updateAccess);
 // The panel opens under its button, its right edge on the button's, so it
 // stays over the writing column rather than running off past it.
 const positionPanel = () => {
  const rect = trigger.getBoundingClientRect(), width = panel.offsetWidth;
  panel.style.left = Math.max(16, Math.min(rect.right - width, innerWidth - width - 16)) + 'px';
  panel.style.top = Math.max(12, Math.min(rect.bottom + 8, innerHeight - panel.offsetHeight - 12)) + 'px';
 };
 panel.addEventListener('toggle', () => {
  const open = panel.matches(':popover-open');
  trigger.setAttribute('aria-expanded', String(open));
  if (open) positionPanel();
 });
 window.addEventListener('resize', () => { if (panel.matches(':popover-open')) positionPanel(); });
 if (slug.getAttribute('aria-invalid') === 'true') { panel.showPopover(); positionPanel(); }
 body.addEventListener('paste', async event => {
  const files = [...(event.clipboardData?.items || [])].filter(i => i.kind === 'file' && i.type.startsWith('image/')).map(i => i.getAsFile());
  if (!files.length) return;
  event.preventDefault();
  pending++;
  try {
   for (const file of files) {
    const ext = {'image/png': 'png', 'image/jpeg': 'jpg', 'image/gif': 'gif', 'image/webp': 'webp'}[file.type];
    if (!ext) throw Error('Paste a PNG, JPEG, GIF or WebP image.');
    if (file.size > 4 * 1024 * 1024) throw Error('Each image can be up to 4 MB.');
    const total = Object.values(images).reduce((sum, v) => sum + v.length * 3 / 4, 0);
    if (total + file.size > 12 * 1024 * 1024 || Object.keys(images).length >= 32) throw Error('Use up to 32 images, adding up to 12 MB at most.');
    const data = await new Promise((resolve, reject) => {
     const r = new FileReader();
     r.onload = () => resolve(r.result.split(',')[1]);
     r.onerror = () => reject(Error('Could not read that image. Try pasting it again.'));
     r.readAsDataURL(file);
    });
    const name = 'pasted-' + Date.now().toString(36) + '-' + Math.random().toString(36).slice(2, 8) + '.' + ext;
    images[name] = data;
    field.value = JSON.stringify(images);
    body.setRangeText('\n![Pasted image](?asset=' + name + ')\n', body.selectionStart, body.selectionEnd, 'end');
    body.dispatchEvent(new Event('input', {bubbles: true}));
    const n = Object.keys(images).length;
    status.textContent = n + (n === 1 ? ' image attached. Preview to see it.' : ' images attached. Preview to see them.');
   }
  } catch (error) { status.textContent = error.message; } finally { pending--; }
 });
 form.addEventListener('submit', event => {
  if (pending) { event.preventDefault(); status.textContent = 'Wait for the image to finish attaching, then try again.'; }
 }, true);
})();

// The writing comes alive without leaving the textarea: a copy of the text
// sits behind it, coloured by its Markdown (headings, links, code, quotes
// and the marks themselves dimmed), and the textarea's own text turns
// clear, so the caret, selection, spelling and typing stay the browser's.
// Only colours change, never sizes or weights, so every glyph sits where
// the textarea puts it. If the copy ever measures differently from the
// textarea, or colours are forced, the plain textarea comes back.
(() => {
 const field = document.querySelector('#markdown-field'), body = document.querySelector('#markdown'), layer = document.querySelector('#markdown-highlight');
 if (!field || !layer || matchMedia('(forced-colors: active)').matches) return;
 const limit = 200000;
 const escape = text => text.replace(/[&<>]/g, c => ({'&': '&amp;', '<': '&lt;', '>': '&gt;'}[c]));
 const span = (kind, text) => '<span class="md-' + kind + '">' + text + '</span>';
 // Inline marks on an already escaped line: code, links, emphasis.
 const inline = line => line
  .replace(/(`+)([^`]+?)\1/g, (m, tick, code) => span('mark', tick) + span('code', code) + span('mark', tick))
  .replace(/(!?\[)([^\]]*)(\]\()([^)]*)(\))/g, (m, open, label, mid, href, close) => span('mark', open) + span('link', label) + span('mark', mid) + span('url', href) + span('mark', close))
  .replace(/(\*\*|__)(?=\S)(.+?)(?<=\S)\1/g, (m, mark, text) => span('mark', mark) + span('strong', text) + span('mark', mark))
  .replace(/(^|[^*\w])(\*|_)(?=\S)([^*_]+?)(?<=\S)\2(?![*\w])/g, (m, lead, mark, text) => lead + span('mark', mark) + span('em', text) + span('mark', mark))
  .replace(/(\[\^[^\]]+\])/g, m => span('mark', m));
 const render = text => {
  let fenced = false;
  return text.split('\n').map(raw => {
   const line = escape(raw);
   if (/^\s*(```|~~~)/.test(raw)) { fenced = !fenced; return span('fence', line); }
   if (fenced) return span('code', line);
   let m;
   if ((m = raw.match(/^(#{1,6})(\s+)(.*)$/))) return span('mark', m[1]) + m[2] + span('heading', inline(escape(m[3])));
   if ((m = raw.match(/^(\s*>\s?)(\[!\w+\])?(.*)$/))) return span('mark', escape(m[1])) + (m[2] ? span('callout', m[2]) : '') + span('quote', inline(escape(m[3])));
   if ((m = raw.match(/^(\s*)([-*+]|\d+[.)])(\s+)(\[[ xX]\]\s)?(.*)$/))) return m[1] + span('bullet', escape(m[2])) + m[3] + (m[4] ? span('mark', m[4]) : '') + inline(escape(m[5]));
   if (/^\s*(\|.*\|)\s*$/.test(raw)) return line.replace(/\|/g, span('mark', '|'));
   if (/^\s*([-*_])(\s*\1){2,}\s*$/.test(raw)) return span('mark', line);
   return inline(line);
  }).join('\n');
 };
 let frame = 0, on = false;
 const off = () => { on = false; field.classList.remove('is-highlighted'); layer.textContent = ''; };
 const paint = () => {
  frame = 0;
  if (body.value.length > limit) { off(); return; }
  // A trailing newline still makes a line in the textarea; give the copy one too.
  layer.innerHTML = render(body.value) + (body.value.endsWith('\n') || !body.value ? '\u200b' : '');
  // The copy takes the textarea's own box, whatever the stylesheet gave it.
  layer.style.width = body.offsetWidth + 'px';
  if (!on) { on = true; field.classList.add('is-highlighted'); }
  // The copy must wrap exactly as the textarea does.
  if (Math.abs(layer.scrollHeight - body.scrollHeight) > 2 || Math.abs(layer.clientWidth - body.clientWidth) > 1) off();
 };
 const soon = () => { if (!frame) frame = requestAnimationFrame(paint); };
 body.addEventListener('input', soon);
 body.form.addEventListener('cairn:restored', soon);
 body.form.addEventListener('cairn:fitted', soon);
 addEventListener('resize', soon);
 document.fonts?.ready.then(soon);
 paint();
})();

