(() => {
 const $$ = (selector, root = document) => [...root.querySelectorAll(selector)];
 // What the address said before this script tidies it away.
 const done = new URL(location.href).searchParams.get('done') || '';

 // Show and hide a password. The buttons ship hidden, so without JavaScript
 // the field is a plain password field.
 $$('[data-reveal]').forEach(button => {
  const field = document.getElementById(button.dataset.reveal);
  if (!field) return;
  button.hidden = false;
  button.addEventListener('click', () => {
   const show = field.type === 'password';
   field.type = show ? 'text' : 'password';
   button.textContent = show ? 'Hide' : 'Show';
   button.setAttribute('aria-pressed', String(show));
   button.setAttribute('aria-label', (show ? 'Hide' : 'Show') + ' password');
   field.focus({preventScroll: true});
  });
 });

 // Copy text, confirming on the button for two seconds and to screen
 // readers; when the clipboard is out of reach, select it for the person.
 const copyText = async (button, text, field, message) => {
  const status = document.getElementById(button.dataset.status || 'copy-status');
  const label = button.querySelector('.copy-label'), rest = label?.dataset.rest || label?.textContent;
  if (label) label.dataset.rest = rest;
  let said = message;
  try {
   if (navigator.clipboard && isSecureContext) await navigator.clipboard.writeText(text);
   else {
    const scratch = document.createElement('textarea');
    scratch.value = text;
    scratch.className = 'sr-only';
    document.body.append(scratch);
    scratch.select();
    const copied = document.execCommand('copy');
    scratch.remove();
    if (!copied) throw Error();
   }
   button.classList.add('is-done');
   if (label) label.textContent = 'Copied';
  } catch {
   if (field) { field.focus(); field.select(); }
   said = 'Press Command or Control and C to copy the selected text.';
  }
  if (status) status.textContent = said;
  clearTimeout(button.copyTimer);
  button.copyTimer = setTimeout(() => { button.classList.remove('is-done'); if (label) label.textContent = rest; }, 2000);
 };
 $$('[data-copy], [data-copy-value]').forEach(button => {
  const field = document.getElementById(button.dataset.copy || '');
  if (!field && !button.dataset.copyValue) return;
  button.hidden = false;
  button.addEventListener('click', () => copyText(button, field ? field.value : button.dataset.copyValue, field, 'Link copied.'));
 });

 // A quiet Save turns primary once one of its fields changes, so the one
 // action that matters stands out without competing at rest.
 $$('[data-save-when-changed]').forEach(button => {
  const fields = button.dataset.saveWhenChanged.split(/\s+/).map(id => document.getElementById(id)).filter(Boolean);
  if (!fields.length) return;
  // Save title waits for an edit: with nothing changed it is off, so it
  // never looks like something to press. Without JavaScript it stays on.
  const update = () => {
   const same = fields.every(field => field.value === field.defaultValue);
   button.classList.toggle('quiet', same);
   if ('offUntilChanged' in button.dataset) button.disabled = same;
  };
  fields.forEach(field => field.addEventListener('input', update));
  update();
 });

 // A one-line text area: it grows with its words, and Enter submits.
 $$('textarea[data-single-line]').forEach(area => {
  const fit = () => {
   if (CSS.supports('field-sizing', 'content')) return;
   area.style.height = 'auto';
   area.style.height = area.scrollHeight + 2 + 'px';
  };
  area.addEventListener('keydown', event => {
   if (event.key === 'Enter' && !event.isComposing) {
    event.preventDefault();
    const save = area.form.querySelector('button[name=action]');
    if (save && !save.disabled) area.form.requestSubmit(save);
   }
  });
  area.addEventListener('input', () => {
   if (/[\r\n]/.test(area.value)) area.value = area.value.replace(/[\r\n]+/g, ' ');
   fit();
  });
  window.addEventListener('resize', fit);
  fit();
 });

 // A confirmation that opens in place takes focus, so the question is read
 // first; Escape or Keep page closes it and returns to its button.
 $$('details.delete-confirm').forEach(details => {
  const summary = details.querySelector('summary'), question = details.querySelector('.confirm-question');
  const close = () => { details.open = false; summary.focus(); };
  details.addEventListener('toggle', () => { if (details.open) question?.focus(); });
  details.addEventListener('keydown', event => {
   if (event.key === 'Escape' && details.open) { event.preventDefault(); close(); }
  });
  details.querySelectorAll('[data-close-details]').forEach(button => {
   button.hidden = false;
   button.addEventListener('click', close);
  });
  if (details.open) question?.focus();
 });

 // Sharing: after a new password is saved, Copy link and password hands
 // both over in one go, so the owner never types the password twice. The
 // password waits in this tab's session storage only across the save's
 // reload, and is removed as soon as the page reads it.
 const sharing = document.getElementById('sharing-form');
 if (sharing) {
  const key = 'cairn.share-password:' + sharing.dataset.page;
  const password = sharing.querySelector('#share-password');
  let known = '';
  try {
   known = sessionStorage.getItem(key) || '';
   sessionStorage.removeItem(key);
  } catch {}
  if (!/^(password-set|password-changed|relinked-password)$/.test(done)) known = '';
  sharing.addEventListener('submit', event => {
   if (event.defaultPrevented || event.submitter?.value !== 'sharing' || !password?.value) return;
   try { sessionStorage.setItem(key, password.value); } catch {}
  });
  const both = document.getElementById('copy-both');
  if (known && both) {
   both.hidden = false;
   both.addEventListener('click', () => copyText(both, sharing.dataset.publicUrl + '\nPassword: ' + known, null, 'Link and password copied.'));
  }
 }

 // Drop zones: the native file input covers the zone, so clicks, keys and
 // drops reach it directly; this shows what was chosen, and checks type
 // and size before anything is uploaded, in the server's own words.
 $$('.dropzone').forEach(zone => {
  const input = zone.querySelector('input[type=file]');
  const name = zone.querySelector('.dropzone-name');
  const detail = zone.querySelector('.dropzone-size');
  const error = document.getElementById(zone.dataset.error);
  const limit = Number(zone.dataset.limit);
  const types = input.accept.split(',').map(type => type.trim().toLowerCase());
  const size = bytes => bytes < 1024 ? bytes + ' bytes' : bytes < 1048576 ? Math.round(bytes / 1024) + ' KB' : (bytes / 1048576).toFixed(1) + ' MB';
  const fail = message => {
   cairnFields.show(input, message);
   zone.classList.remove('has-file');
   zone.classList.add('has-error');
   input.value = '';
  };
  const clear = () => {
   cairnFields.clear(input);
   error.hidden = true;
   zone.classList.remove('has-error');
  };
  zone.classList.add('is-enhanced');
  input.addEventListener('change', () => {
   const files = [...input.files];
   if (!files.length) { zone.classList.remove('has-file'); return; }
   for (const file of files) {
    const dot = file.name.lastIndexOf('.');
    const extension = dot > 0 ? file.name.slice(dot).toLowerCase() : '';
    if (!types.includes(extension)) return fail(zone.dataset.unsupported);
    if (file.size > limit) return fail(zone.dataset.tooLarge);
   }
   clear();
   const total = files.reduce((sum, file) => sum + file.size, 0);
   name.textContent = files.length === 1 ? files[0].name : files.length + ' images: ' + files.map(file => file.name).join(', ');
   detail.textContent = size(total) + ' · Choose again to replace ' + (files.length === 1 ? 'it' : 'them');
   zone.classList.add('has-file');
  });
  ['dragenter', 'dragover'].forEach(type => input.addEventListener(type, () => zone.classList.add('is-dragging')));
  input.addEventListener('dragleave', () => zone.classList.remove('is-dragging'));
  // Take dropped files ourselves, so a drop always lands the same way.
  input.addEventListener('drop', event => {
   zone.classList.remove('is-dragging');
   const files = event.dataTransfer && event.dataTransfer.files;
   if (!files || !files.length) return;
   event.preventDefault();
   const chosen = new DataTransfer();
   for (const file of input.multiple ? files : [files[0]]) chosen.items.add(file);
   input.files = chosen.files;
   input.dispatchEvent(new Event('change', {bubbles: true}));
  });
  if (zone.classList.contains('has-error')) input.value = '';
 });

 // The password gate. After repeated wrong passwords the next try waits a
 // moment. Unlock page pressed during the wait is kept and sent when it
 // ends, so what was typed stays in the field and never meets a refusal.
 const gate = document.querySelector('.gate-form[data-wait]');
 if (gate) {
  // A fifth of a second's margin, so the held try never arrives early.
  const until = Date.now() + Number(gate.dataset.wait) * 1000 + 200;
  let held = 0;
  gate.addEventListener('submit', event => {
   const left = until - Date.now();
   if (event.defaultPrevented || left <= 0) return;
   event.preventDefault();
   if (held) return;
   const seconds = Math.max(1, Math.ceil((left - 200) / 1000)), error = gate.querySelector('#gate-password-error');
   error.querySelector('span').textContent = 'Cairn will check it in ' + (seconds === 1 ? '1 second' : seconds + ' seconds') + '.';
   error.hidden = false;
   held = setTimeout(() => { held = 0; gate.requestSubmit(); }, left);
  });
 }

 // A confirmation in the address would show again on reload; drop it.
 const url = new URL(location.href);
 if (url.searchParams.has('done')) {
  url.searchParams.delete('done');
  history.replaceState(null, '', url.pathname + url.search + url.hash);
 }
})();
