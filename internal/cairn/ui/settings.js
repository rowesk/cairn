// Settings. Everything here adds to forms that work without JavaScript,
// except agent keys and photos, which ship hidden and appear only here.

(() => {
 // Controls that need this script.
 document.querySelector('#connect-agent').hidden = false;
 document.querySelectorAll('.manage-key, .person-menu-trigger').forEach(control => { control.hidden = false; });
})();

// Photos: pick an image, frame it in a circle, save it for an author.
(() => {
 const $ = selector => document.querySelector(selector);
 const picker = $('#photo-picker'), dialog = $('#photo-dialog'), canvas = $('#photo-canvas'), zoom = $('#photo-zoom');
 const feedback = $('#photo-feedback'), status = $('#crop-status'), save = $('#save-photo');
 const csrf = document.querySelector('[name=csrf]').value;
 let name = '', bitmap = null, scale = 1, x = 0, y = 0, drag = null, busy = false, opener = null;

 function pick(who) {
  name = who;
  opener = document.activeElement;
  document.querySelectorAll('.person-menu:popover-open').forEach(menu => menu.hidePopover());
  picker.value = '';
  picker.click();
 }
 window.cairnPickPhoto = pick;

 function draw() {
  if (!bitmap) return;
  const size = 256, ratio = Math.max(size / bitmap.width, size / bitmap.height) * scale;
  const w = bitmap.width * ratio, h = bitmap.height * ratio;
  x = Math.max((size - w) / 2, Math.min((w - size) / 2, x));
  y = Math.max((size - h) / 2, Math.min((h - size) / 2, y));
  const ctx = canvas.getContext('2d');
  ctx.clearRect(0, 0, size, size);
  ctx.drawImage(bitmap, (size - w) / 2 + x, (size - h) / 2 + y, w, h);
 }

 async function upload(author, action, blob) {
  const form = new FormData();
  form.set('csrf', csrf);
  form.set('name', author);
  form.set('action', action);
  if (blob) form.set('photo', blob, 'avatar.png');
  const response = await fetch('/settings', {method: 'POST', body: form});
  if (!response.ok) {
   const doc = new DOMParser().parseFromString(await response.text(), 'text/html');
   throw Error(doc.querySelector('.toast-text')?.textContent || 'Cairn couldn’t save the photo. Try again.');
  }
 }

 document.addEventListener('click', async event => {
  const pickButton = event.target.closest('[data-pick]');
  if (pickButton) { pick(pickButton.dataset.pick); return; }
  const remove = event.target.closest('[data-remove]');
  if (!remove) return;
  remove.disabled = true;
  try {
   await upload(remove.dataset.remove, 'remove_photo');
   location.assign('/settings?saved=photo-removed#people');
  } catch (error) {
   remove.disabled = false;
   feedback.textContent = error.message;
   document.querySelectorAll('.person-menu:popover-open').forEach(menu => menu.hidePopover());
  }
 });

 // Rows can be redrawn after a key changes, so menus are placed from one listener.
 document.addEventListener('toggle', event => {
  const menu = event.target;
  if (!(menu instanceof HTMLElement) || !menu.classList.contains('person-menu') || !menu.matches(':popover-open')) return;
  const button = document.querySelector('[popovertarget="' + menu.id + '"]'), r = button.getBoundingClientRect();
  menu.style.left = Math.max(12, Math.min(r.right - menu.offsetWidth, innerWidth - menu.offsetWidth - 12)) + 'px';
  menu.style.top = Math.max(12, Math.min(r.bottom + 6, innerHeight - menu.offsetHeight - 12)) + 'px';
 }, true);

 picker.addEventListener('change', async () => {
  const file = picker.files[0];
  if (!file) return;
  feedback.textContent = '';
  status.textContent = '';
  if (file.size > 4 * 1024 * 1024) { feedback.textContent = 'Choose an image of 4 MB or less.'; return; }
  try {
   const image = await createImageBitmap(file);
   if (image.width > 4096 || image.height > 4096 || image.width * image.height > 16000000) {
    image.close();
    throw Error('Choose an image up to 4,096 pixels per side and 16 megapixels.');
   }
   bitmap?.close();
   bitmap = image;
   scale = 1; x = 0; y = 0;
   zoom.value = '1';
   draw();
   $('#photo-for').textContent = name;
   if (!dialog.open) dialog.showModal();
  } catch (error) {
   feedback.textContent = error.message.startsWith('Choose') ? error.message : 'Your browser could not open that image. Try WebP, AVIF, PNG or JPEG.';
   if (dialog.open) status.textContent = feedback.textContent;
  }
 });

 zoom.addEventListener('input', () => { scale = Number(zoom.value); draw(); });
 canvas.addEventListener('pointerdown', event => {
  if (busy) return;
  drag = {x: event.clientX, y: event.clientY};
  canvas.setPointerCapture(event.pointerId);
 });
 canvas.addEventListener('pointermove', event => {
  if (!drag) return;
  const factor = 256 / canvas.getBoundingClientRect().width;
  x += (event.clientX - drag.x) * factor;
  y += (event.clientY - drag.y) * factor;
  drag = {x: event.clientX, y: event.clientY};
  draw();
 });
 canvas.addEventListener('pointerup', () => { drag = null; });
 canvas.addEventListener('pointercancel', () => { drag = null; });
 canvas.tabIndex = 0;
 canvas.addEventListener('keydown', event => {
  const moves = {ArrowLeft: [-8, 0], ArrowRight: [8, 0], ArrowUp: [0, -8], ArrowDown: [0, 8]};
  if (!moves[event.key]) return;
  event.preventDefault();
  x += moves[event.key][0];
  y += moves[event.key][1];
  draw();
 });

 const setBusy = on => {
  busy = on;
  save.disabled = on;
  save.textContent = on ? 'Saving…' : 'Save photo';
  $('#cancel-photo').disabled = on;
  $('#choose-another').disabled = on;
 };
 save.addEventListener('click', async () => {
  if (!bitmap || busy) return;
  setBusy(true);
  try {
   const blob = await new Promise(resolve => canvas.toBlob(resolve, 'image/png'));
   if (!blob) throw Error('Could not prepare that photo. Try another image.');
   await upload(name, 'photo', blob);
   location.assign('/settings?saved=photo#people');
  } catch (error) {
   status.textContent = error.message;
   setBusy(false);
  }
 });
 $('#cancel-photo').addEventListener('click', () => dialog.close());
 dialog.addEventListener('cancel', event => { if (busy) event.preventDefault(); });
 dialog.addEventListener('close', () => { bitmap?.close(); bitmap = null; opener?.focus({preventScroll: true}); });
 $('#choose-another').addEventListener('click', () => { picker.value = ''; picker.click(); });
})();

// Agent keys: connect an agent by name and its key is made at once, then
// copied, handed over and tested in one dialog. The key exists only in this
// dialog's field.
(() => {
 const $ = selector => document.querySelector(selector);
 const dialog = $('#key-dialog'), statusLine = $('#key-status'), state = $('#key-state'), error = $('#key-error');
 const secret = $('#key-secret'), copyKey = $('#key-copy');
 const confirmPanel = $('#key-confirm'), confirmText = $('#key-confirm-text'), confirmYes = $('#key-confirm-yes'), confirmNo = $('#key-confirm-no');
 const guide = $('#agent-setup-guide');
 const origin = (guide.dataset.origin || location.origin).replace(/\/$/, '');
 const step = id => $('#step-' + id);
 let person = '', active = false, busy = false, changed = false, copied = true, opener = null, confirming = '';

 const announce = text => { statusLine.textContent = ''; requestAnimationFrame(() => { statusLine.textContent = text; }); };
 const fail = text => { error.querySelector('span').textContent = text; error.hidden = false; };
 const setupText = () => guide.value.replaceAll('{{ORIGIN}}', origin).replaceAll('{{AUTHOR}}', person);
 const command = () => 'CAIRN_URL=' + origin + ' cairn publish page.md -request "the owner’s words"';
 const test = () => 'curl -sS -H "Authorization: Bearer $CAIRN_API_KEY" "' + origin + '/api/pages?limit=1"';

 function render() {
  dialog.querySelectorAll('.key-name').forEach(el => { el.textContent = person; });
  $('#key-person').textContent = person;
  const fresh = !!secret.value;
  step('create').hidden = active || busy;
  step('copy').hidden = !fresh;
  step('setup').hidden = !active;
  step('test').hidden = !active;
  $('#key-change').hidden = !active || !!confirming;
  $('#command-text').textContent = command();
  $('#test-command').textContent = test();
  $('#setup-text').textContent = setupText();
 }

 function describe(created, used) {
  if (!active) return 'No key yet. ' + person + ' can’t publish until you create one.';
  if (!created) return 'Key active.';
  // The row's own words, such as "created 4 Nov" and "last used 2 hours ago".
  return 'Key active. Created ' + created + ', ' + (used ? 'last used ' + used : 'not used yet') + '.';
 }

 function open(name, data, from) {
  person = name; opener = from; active = data.active;
  changed = false; copied = true; confirming = ''; busy = false;
  secret.value = '';
  error.hidden = true;
  confirmPanel.hidden = true;
  statusLine.textContent = '';
  state.textContent = describe(data.created, data.used);
  dialog.querySelector('.setup-preview').open = false;
  render();
  dialog.showModal();
  (active ? $('#setup-copy') : $('#key-create')).focus();
 }

 // A row with a key opens the dialog to manage it. A row without one, and
 // the connect form, make the key straight away: one click, not two.
 const createFor = (name, from) => {
  open(name, {active: false}, from);
  state.textContent = 'Creating a key for ' + name + '…';
  change('key_create');
 };
 document.addEventListener('click', event => {
  const button = event.target.closest('[data-key-name]');
  if (!button) return;
  if ('keyCreate' in button.dataset) { createFor(button.dataset.keyName, button); return; }
  // The row's dates, as this browser's clock shows them.
  const times = [...(button.closest('.person-row')?.querySelectorAll('time') || [])].map(t => t.textContent);
  open(button.dataset.keyName, {active: true, created: times[0] || button.dataset.keyCreated, used: times[1] || button.dataset.keyUsed}, button);
 });

 const connect = $('#connect-agent'), nameField = $('#new-person-name');
 // The default author is the owner's own byline. The owner writes under it
 // without a key, and a key for it would reach every page they write, so it
 // is refused here, beside the field, before any dialog opens.
 const owner = () => (connect.dataset.owner || '').trim();
 const ownName = name => owner() !== '' && name.toLowerCase() === owner().toLowerCase();
 function checkName(forKey) {
  const name = nameField.value.trim();
  let problem = '';
  if (!name) problem = 'Enter a name. It becomes the byline on the agent’s pages.';
  else if (new TextEncoder().encode(name).length > 100) problem = 'Shorten the name to 100 characters or fewer.';
  else if (forKey && ownName(name)) problem = 'You write as ' + owner() + ' without a key, so give the agent a name of its own.';
  if (problem) {
   cairnFields.show(nameField, problem);
   document.documentElement.dataset.inputModality = 'keyboard';
   nameField.focus();
  } else cairnFields.clear(nameField);
  return problem ? '' : name;
 }
 nameField.addEventListener('input', () => { if (nameField.getAttribute('aria-invalid') === 'true' && nameField.value.trim()) checkName(true); });
 connect.addEventListener('submit', event => {
  event.preventDefault();
  const name = checkName(true);
  if (!name) return;
  const row = [...document.querySelectorAll('[data-key-name]')].find(b => b.dataset.keyName.toLowerCase() === name.toLowerCase());
  if (row && row.dataset.keyActive === 'true') open(row.dataset.keyName, {active: true, created: row.dataset.keyCreated, used: row.dataset.keyUsed}, $('#new-person-key'));
  else createFor(row ? row.dataset.keyName : name, $('#new-person-key'));
 });
 $('#new-person-photo').addEventListener('click', () => { const name = checkName(); if (name) window.cairnPickPhoto(name); });

 async function change(action) {
  if (busy) return;
  busy = true;
  error.hidden = true;
  let next = null;
  render();
  dialog.querySelectorAll('button').forEach(b => { b.disabled = true; });
  announce(action === 'key_revoke' ? 'Revoking the key…' : 'Creating a key…');
  try {
   const form = new URLSearchParams({csrf: document.querySelector('[name=csrf]').value, name: person, action});
   const response = await fetch('/settings', {method: 'POST', body: form});
   if (!response.ok) throw Error((await response.text()).trim() || 'Cairn couldn’t change the key. Try again.');
   const data = await response.json();
   changed = true;
   confirming = '';
   confirmPanel.hidden = true;
   active = !!data.key;
   secret.value = data.key || '';
   copied = !data.key;
   busy = false;
   if (active) {
    state.textContent = action === 'key_rotate' ? 'New key created just now. The old key no longer works.' : 'Key created just now. Not used yet.';
    render();
    next = copyKey;
    announce('Key created. Copy it now: Cairn shows it only once.');
   } else {
    state.textContent = 'Key revoked. ' + person + ' can’t publish or read its pages. Its pages stay in your library.';
    render();
    next = $('#key-create');
    announce('Key revoked.');
   }
  } catch (problem) {
   busy = false;
   state.textContent = describe();
   render();
   fail(problem.message);
   announce('');
   next = confirming ? confirmYes : $('#key-create');
  } finally {
   busy = false;
   // Buttons were disabled while the request ran, so focus moves after.
   dialog.querySelectorAll('button').forEach(b => { b.disabled = false; });
   next?.focus();
  }
 }

 // Destructive changes and leaving an uncopied key confirm in place.
 function ask(kind) {
  confirming = kind;
  const texts = {
   key_rotate: ['Rotate the key for ' + person + '? The current key stops working at once, and ' + person + ' can’t publish until you give it the new one.', 'Rotate key', 'Keep current key', 'danger'],
   key_revoke: ['Revoke the key for ' + person + '? ' + person + ' can no longer publish or read its pages. Its pages stay in your library.', 'Revoke key', 'Keep key', 'danger'],
   close: ['You haven’t copied the key. Once this closes, Cairn can’t show it again.', 'Copy key', 'Close without copying', ''],
  }[kind];
  confirmText.textContent = texts[0];
  confirmYes.textContent = texts[1];
  confirmNo.textContent = texts[2];
  confirmYes.className = texts[3];
  confirmPanel.classList.toggle('is-info', kind === 'close');
  confirmPanel.hidden = false;
  $('#key-change').hidden = true;
  confirmPanel.scrollIntoView({block: 'nearest'});
  confirmYes.focus();
 }
 function stopAsking(focusTarget) {
  confirming = '';
  confirmPanel.hidden = true;
  render();
  focusTarget?.focus();
 }
 // Once the key is copied, anywhere, the question about leaving goes away.
 const keyCopied = () => {
  copied = true;
  if (confirming === 'close') stopAsking(null);
 };
 confirmYes.addEventListener('click', async () => {
  if (confirming === 'close') {
   if (await copy(secret.value, copyKey, 'Key copied. Save it in the agent’s secret store.')) keyCopied();
   copyKey.focus();
   return;
  }
  change(confirming);
 });
 confirmNo.addEventListener('click', () => {
  if (confirming === 'close') { copied = true; dialog.close(); return; }
  stopAsking(confirming === 'key_rotate' ? $('#key-rotate') : $('#key-revoke'));
 });
 $('#key-create').addEventListener('click', () => change('key_create'));
 $('#key-rotate').addEventListener('click', () => ask('key_rotate'));
 $('#key-revoke').addEventListener('click', () => ask('key_revoke'));

 async function copy(text, button, message) {
  const label = button.querySelector('.copy-label');
  const rest = label.dataset.rest || label.textContent;
  label.dataset.rest = rest;
  try {
   if (!navigator.clipboard || !isSecureContext) throw Error();
   await navigator.clipboard.writeText(text);
  } catch {
   // Without the clipboard API, select the text so the person can copy it.
   const scratch = document.createElement('textarea');
   scratch.value = text;
   scratch.setAttribute('readonly', '');
   scratch.className = 'sr-only';
   dialog.append(scratch);
   scratch.select();
   const done = document.execCommand('copy');
   scratch.remove();
   if (!done) {
    if (button === copyKey) { secret.focus(); secret.select(); }
    announce('Press Command or Control and C to copy.');
    return false;
   }
  }
  button.classList.add('is-done');
  label.textContent = 'Copied';
  announce(message);
  clearTimeout(button.timer);
  button.timer = setTimeout(() => { button.classList.remove('is-done'); label.textContent = rest; }, 2000);
  return true;
 }
 copyKey.addEventListener('click', async () => { if (await copy(secret.value, copyKey, 'Key copied. Save it in the agent’s secret store.')) keyCopied(); });
 secret.addEventListener('copy', keyCopied);
 $('#setup-copy').addEventListener('click', event => copy(setupText(), event.currentTarget, 'Setup text copied. It holds no key.'));
 $('#command-copy').addEventListener('click', event => copy(command(), event.currentTarget, 'Command copied.'));

 function leave(event) {
  if (busy) { event?.preventDefault(); return; }
  if (!copied) { event?.preventDefault(); ask('close'); return; }
  if (!event) dialog.close();
 }
 // Keep Tab inside the dialog: past the last control it returns to the first.
 dialog.addEventListener('keydown', event => {
  if (event.key !== 'Tab') return;
  const stops = [...dialog.querySelectorAll('button,input,summary,pre[tabindex],a[href]')].filter(el => !el.disabled && el.getClientRects().length && !el.closest('[hidden]'));
  if (!stops.length) return;
  const first = stops[0], last = stops[stops.length - 1];
  if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus(); }
  else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); }
 });
 $('#key-close').addEventListener('click', () => leave());
 dialog.addEventListener('cancel', leave);
 dialog.addEventListener('close', async () => {
  secret.value = '';
  const name = person, from = opener;
  if (!changed) { from?.focus({preventScroll: true}); return; }
  // Redraw the people list so each row shows its key's state, then return focus.
  try {
   const page = new DOMParser().parseFromString(await (await fetch('/settings')).text(), 'text/html');
   const fresh = page.querySelector('.people-list'), list = $('.people-list');
   if (fresh && list) {
    fresh.querySelectorAll('.manage-key, .person-menu-trigger').forEach(control => { control.hidden = false; });
    list.replaceWith(fresh);
   }
  } catch {}
  const row = [...document.querySelectorAll('[data-key-name]')].find(b => b.dataset.keyName === name);
  if (from?.id === 'new-person-key') nameField.value = '';
  ((from?.isConnected ? from : null) || row || nameField).focus({preventScroll: false});
 });
})();

// Appearance: choosing a theme applies it to this page at once and saves it.
// Without JavaScript the cards are radio buttons with a Save button.
(() => {
 const form = document.querySelector('[data-theme-form]');
 if (!form) return;
 const root = document.documentElement;
 const scheme = document.querySelector('meta[name="color-scheme"]');
 const schemes = {dark: 'dark', futuristic: 'dark', system: 'light dark'};
 const colours = document.querySelector('#theme-colors');
 form.querySelector('[data-without-script]').hidden = true;
 let saved = new FormData(form), pending = null;
 // The theme, the color-scheme and the browser's toolbar colour change together.
 function apply(theme) {
  root.dataset.theme = theme;
  if (scheme) scheme.content = schemes[theme] || 'light';
  document.querySelectorAll('head meta[name="theme-color"]').forEach(meta => meta.remove());
  for (const meta of colours.content.querySelectorAll('[data-for="' + theme + '"]')) {
   const copy = meta.cloneNode();
   copy.removeAttribute('data-for');
   document.head.append(copy);
  }
 }
 function restore(data) {
  for (const [name, value] of data) {
   const field = form.elements[name];
   if (field instanceof RadioNodeList) field.value = value;
   else if (field && field.type !== 'hidden') field.value = value;
  }
  apply(data.get('theme'));
 }
 async function save() {
  const data = new FormData(form), before = saved;
  apply(data.get('theme'));
  pending?.abort();
  const control = pending = new AbortController();
  try {
   const response = await fetch('/settings', {method: 'POST', body: new URLSearchParams(data), headers: {accept: 'application/json'}, signal: control.signal});
   const reply = await response.json().catch(() => ({}));
   if (!response.ok || !reply.ok) throw Error(reply.message || 'Cairn couldn’t save the theme. Try again.');
   saved = data;
   cairnToast({kind: 'success', text: reply.message});
  } catch (error) {
   if (error.name === 'AbortError') return;
   restore(before);
   cairnToast({kind: 'error', text: error.message || 'Cairn couldn’t save the theme. Try again.'});
  }
 }
 form.addEventListener('change', event => { if (event.target.matches('input[name=theme],select')) save(); });
 form.addEventListener('submit', event => { event.preventDefault(); save(); });
})();

// Keyboard, Writing & links and Send to Kindle save each field as it
// changes: a tick beside the field, the confirmation in the toast region,
// and any problem beside its field. Without JavaScript each form keeps its
// Save button.
(() => {
 const tick = document.querySelector('#save-tick');
 const field = (form, name) => form.elements[name];
 const mark = (input, text) => {
  const state = document.querySelector('.save-state[data-for="' + input.id + '"]');
  if (!state) return;
  state.innerHTML = text ? tick.innerHTML : '';
  state.classList.toggle('is-saved', !!text);
  clearTimeout(state.timer);
  if (text) state.timer = setTimeout(() => { state.classList.remove('is-saved'); }, 2400);
 };
 document.querySelectorAll('form[data-autosave]').forEach(form => {
  form.querySelector('[data-without-script]')?.setAttribute('hidden', '');
  const warning = form.querySelector('.is-warning');
  let pending = null;
  // The warning belongs to the saved address: it shows only once an address
  // is saved, and never beside an error, which means nothing was saved.
  const warn = text => {
   if (!warning) return;
   warning.hidden = !text;
   if (text) warning.querySelector('span').textContent = text;
   const input = document.getElementById(warning.id.replace(/-warning$/, ''));
   if (!input) return;
   const ids = (input.getAttribute('aria-describedby') || '').split(/\s+/).filter(id => id && id !== warning.id);
   if (text) ids.splice(ids.length - 1, 0, warning.id);
   input.setAttribute('aria-describedby', ids.join(' '));
  };
  const refuse = (input, problem) => {
   cairnFields.show(input, problem);
   mark(input, '');
   warn('');
  };
  const save = async input => {
   if (input.disabled) return;
   const problem = cairnFields.check(input);
   if (problem) {
    refuse(input, problem);
    cairnAnnounce(problem);
    return;
   }
   cairnFields.clear(input);
   pending?.abort();
   const control = pending = new AbortController();
   try {
    const response = await fetch('/settings', {method: 'POST', body: new URLSearchParams(new FormData(form)), headers: {accept: 'application/json'}, signal: control.signal});
    const reply = await response.json().catch(() => ({}));
    if (reply.errors && Object.keys(reply.errors).length) {
     for (const [name, message] of Object.entries(reply.errors)) {
      const target = field(form, name);
      if (target) refuse(target, message);
     }
     cairnAnnounce(Object.values(reply.errors)[0]);
     return;
    }
    if (!response.ok || !reply.ok) throw Error(reply.message || 'Cairn couldn’t save that. Try again.');
    for (const control of form.elements) if ('defaultValue' in control && control.type !== 'hidden') control.defaultValue = control.value;
    // A new default author is the name Connect an agent now refuses.
    const connect = document.querySelector('#connect-agent');
    if (connect && input.name === 'author') connect.dataset.owner = input.value.trim();
    mark(input, 'Saved');
    if (input.id === 'shortcuts') {
     if (input.checked) delete document.documentElement.dataset.shortcuts;
     else document.documentElement.dataset.shortcuts = 'off';
    }
    warn(reply.warning || '');
    // The warning shows once, under the field; screen readers hear it with the confirmation.
    cairnToast({kind: 'success', text: reply.message});
    if (reply.warning) cairnAnnounce(reply.message + ' ' + reply.warning);
   } catch (error) {
    if (error.name !== 'AbortError') cairnToast({kind: 'error', text: error.message});
   }
  };
  form.addEventListener('change', event => {
   const input = event.target;
   if (!input.matches('input, select') || input.type === 'hidden') return;
   // A field saves only what changed; typing the same value again is quiet.
   if (input.type !== 'checkbox' && input.value === input.defaultValue && input.getAttribute('aria-invalid') !== 'true') return;
   save(input);
  });
  form.addEventListener('submit', event => {
   event.preventDefault();
   const input = document.activeElement?.form === form ? document.activeElement : form.querySelector('input:not([type=hidden]), select');
   if (input) save(input);
  });
 });
})();

(() => {
 // A saved confirmation in the address would show again on reload; drop it.
 const url = new URL(location.href);
 if (url.searchParams.has('saved') || url.searchParams.has('kindle')) {
  url.searchParams.delete('saved');
  url.searchParams.delete('kindle');
  history.replaceState(null, '', url.pathname + url.search + url.hash);
 }
})();
