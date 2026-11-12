// Every page script starts with this file: focus rings, report colours,
// local dates, the toast region and inline field errors.

// Focus rings show for the keyboard. A press of any key other than a lone
// modifier marks keyboard use on <html>; a pointer press marks pointer use,
// so a clicked button drops its ring (focus.css).
(() => {
 const root = document.documentElement;
 const modifiers = ['Shift', 'Control', 'Alt', 'Meta', 'CapsLock', 'Fn'];
 root.dataset.inputModality = 'pointer';
 document.addEventListener('pointerdown', () => { root.dataset.inputModality = 'pointer'; }, true);
 document.addEventListener('keydown', event => {
  if (!modifiers.includes(event.key)) root.dataset.inputModality = 'keyboard';
 }, true);
 // A frame that takes focus can't match :focus; mark it for focus.css.
 const frames = () => document.querySelectorAll('iframe.has-focus');
 addEventListener('blur', () => setTimeout(() => {
  const now = document.activeElement;
  for (const f of frames()) if (f !== now) f.classList.remove('has-focus');
  if (now?.tagName === 'IFRAME') now.classList.add('has-focus');
 }));
 addEventListener('focus', () => { for (const f of frames()) f.classList.remove('has-focus'); });
})();

// Reports Cairn rendered follow the theme: dark in Dark and Futuristic, and
// in System when the device is dark; black on white in Minimal.
// cairnScheme() is the cairn:scheme message for a report frame;
// cairnFollowScheme(frame) sends it whenever the frame loads or the device
// switches. Reports in their own design ignore it.
(() => {
 const root = document.documentElement, night = matchMedia('(prefers-color-scheme: dark)');
 const palettes = {futuristic: 'violet', minimal: 'mono'};
 window.cairnScheme = () => {
  const theme = root.dataset.theme;
  const dark = theme === 'dark' || theme === 'futuristic' || (theme === 'system' && night.matches);
  return {type: 'cairn:scheme', scheme: dark ? 'dark' : 'light', palette: palettes[theme] || ''};
 };
 window.cairnSchemeQuery = () => {
  const s = cairnScheme();
  if (s.scheme === 'light' && !s.palette) return '';
  return '?scheme=' + s.scheme + (s.palette ? '&palette=' + s.palette : '');
 };
 window.cairnFollowScheme = frame => {
  const send = () => { try { frame.contentWindow?.postMessage(cairnScheme(), '*'); } catch {} };
  frame.addEventListener('load', send);
  night.addEventListener?.('change', send);
  send();
 };
})();

// Dates follow the reader's own clock. The server writes them in its time
// zone so pages read right without JavaScript; here every <time data-local>
// is rewritten for this browser: "relative" as Just now, 5 minutes ago,
// Yesterday or 4 Nov ("relative-lower" mid-sentence), "day" as 4 Nov 2026.
// Times are 24-hour, as in "4 November 2026 at 13:57".
(() => {
 const months = ['January', 'February', 'March', 'April', 'May', 'June', 'July', 'August', 'September', 'October', 'November', 'December'];
 const pad = n => String(n).padStart(2, '0');
 window.cairnTime = t => pad(t.getHours()) + ':' + pad(t.getMinutes());
 window.cairnDate = (iso, now = new Date()) => {
  const t = new Date(iso);
  if (isNaN(t)) return null;
  const short = months[t.getMonth()].slice(0, 3);
  const full = t.getDate() + ' ' + months[t.getMonth()] + ' ' + t.getFullYear() + ' at ' + cairnTime(t);
  const age = now - t, midnight = d => new Date(d.getFullYear(), d.getMonth(), d.getDate());
  const days = Math.round((midnight(now) - midnight(t)) / 864e5);
  let label;
  if (age < 6e4) label = 'Just now';
  else if (age < 36e5) { const m = Math.floor(age / 6e4); label = m + (m === 1 ? ' minute ago' : ' minutes ago'); }
  else if (days === 0) { const h = Math.floor(age / 36e5); label = h + (h === 1 ? ' hour ago' : ' hours ago'); }
  else if (days === 1) label = 'Yesterday';
  else if (days < 7) label = days + ' days ago';
  else label = t.getDate() + ' ' + short + (t.getFullYear() === now.getFullYear() ? '' : ' ' + t.getFullYear());
  return {label, full, day: t.getDate() + ' ' + short + ' ' + t.getFullYear()};
 };
 function localize(root) {
  for (const el of root.querySelectorAll?.('time[data-local]') || []) {
   const d = cairnDate(el.getAttribute('datetime'));
   if (!d) continue;
   const kind = el.dataset.local;
   const text = kind === 'day' ? d.day : kind === 'relative-lower' ? d.label.charAt(0).toLowerCase() + d.label.slice(1) : d.label;
   const node = [...el.childNodes].find(n => n.nodeType === 3);
   if (node) node.textContent = text;
   if (el.title) el.title = d.full;
   const spoken = el.querySelector('.sr-only');
   if (spoken) spoken.textContent = ', ' + d.full;
   const row = el.closest('[data-date]');
   if (row) row.dataset.date = d.full;
  }
 }
 const start = () => {
  localize(document);
  new MutationObserver(changes => {
   for (const c of changes) for (const n of c.addedNodes) if (n.nodeType === 1) localize(n.matches?.('time[data-local]') ? n.parentElement : n);
  }).observe(document.body, {childList: true, subtree: true});
 };
 if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', start);
 else start();
})();

// Toasts. Each page has one region, fixed to the edge of the window, and in
// it one live region (#toast-live) that stays in the page from the start, so
// screen readers announce every message: only its text changes. One toast
// shows at a time. It leaves by itself after a while, unless it is an error;
// hovering it or focusing inside it holds it; Escape or its close button
// dismisses it.
//
//   cairnToast({kind: 'success'|'error'|'info', text,
//               actions: [{label, onClick} | {label, href}]})
//   cairnAnnounce(text)   speaks without showing anything
(() => {
 const region = document.getElementById('toasts');
 const live = document.getElementById('toast-live');
 let said = 0;
 window.cairnAnnounce = text => {
  if (!live) return;
  clearTimeout(said);
  live.textContent = '';
  said = setTimeout(() => { live.textContent = text; }, 120);
 };
 if (!region) { window.cairnToast = () => null; return; }
 const icon = kind => region.querySelector('#toast-icons')?.content.querySelector('[data-kind="' + kind + '"]')?.innerHTML || '';
 const reduced = matchMedia('(prefers-reduced-motion: reduce)');
 let current = null, timer = 0, left = 0, since = 0;

 const stop = () => { clearTimeout(timer); timer = 0; };
 const run = ms => { stop(); left = ms; since = Date.now(); timer = setTimeout(() => dismiss(current), ms); };
 const held = toast => toast.matches(':hover') || toast.contains(document.activeElement);

 function dismiss(toast) {
  if (!toast?.isConnected || toast.classList.contains('is-leaving')) return;
  stop();
  const hadFocus = toast.contains(document.activeElement);
  toast.classList.add('is-leaving');
  const gone = () => { toast.remove(); if (current === toast) current = null; };
  if (reduced.matches) gone();
  else { toast.addEventListener('animationend', gone, {once: true}); setTimeout(gone, 400); }
  if (hadFocus) {
   const main = document.getElementById('main');
   if (main) { main.tabIndex = -1; main.focus({preventScroll: true}); }
  }
 }

 function wire(toast) {
  current = toast;
  const close = toast.querySelector('.toast-close');
  close.hidden = false;
  close.addEventListener('click', () => dismiss(toast));
  // Errors stay until dismissed; anything with an action gets longer.
  const lasting = toast.classList.contains('is-error') ? 0 : toast.querySelector('.toast-actions') ? 10000 : 6000;
  const hold = () => { if (timer) { stop(); left -= Date.now() - since; } };
  const resume = () => setTimeout(() => { if (lasting && toast.isConnected && !held(toast) && !timer) run(Math.max(left, 2500)); });
  toast.addEventListener('pointerenter', hold);
  toast.addEventListener('focusin', hold);
  toast.addEventListener('pointerleave', resume);
  toast.addEventListener('focusout', resume);
  toast.addEventListener('keydown', event => { if (event.key === 'Escape') { event.stopPropagation(); dismiss(toast); } });
  if (lasting) run(lasting);
  else stop();
 }

 window.cairnToast = ({kind = 'success', text = '', actions = []} = {}) => {
  current?.remove();
  stop();
  const toast = document.createElement('div'), body = document.createElement('div'), words = document.createElement('p');
  toast.className = 'toast is-' + kind;
  toast.dataset.toast = '';
  toast.innerHTML = icon(kind);
  body.className = 'toast-body';
  words.className = 'toast-text';
  words.textContent = text;
  body.append(words);
  if (actions.length) {
   const row = document.createElement('div');
   row.className = 'toast-actions';
   for (const action of actions) {
    const control = document.createElement(action.href ? 'a' : 'button');
    control.textContent = action.label;
    if (action.href) control.href = action.href;
    else {
     control.type = 'button';
     control.addEventListener('click', event => { action.onClick?.(event); dismiss(toast); });
    }
    row.append(control);
   }
   body.append(row);
  }
  const close = document.createElement('button');
  close.type = 'button';
  close.className = 'toast-close';
  close.setAttribute('aria-label', 'Dismiss');
  close.innerHTML = icon('close');
  toast.append(body, close);
  region.append(toast);
  wire(toast);
  cairnAnnounce(text);
  return toast;
 };
 window.cairnDismissToast = () => dismiss(current);

 // A toast the server sent with the page: wire it, then say it once the
 // page has settled, so the live region already exists when its text lands.
 const first = region.querySelector('[data-toast]');
 if (first) {
  wire(first);
  setTimeout(() => cairnAnnounce(first.querySelector('.toast-text').textContent), 300);
 }
})();

// Inline field errors. Forms that check themselves carry novalidate, so the
// browser shows no bubbles; every field with a rule names its message in
// data attributes, in the same words the server uses:
//
//   required        data-missing     minlength  data-too-short
//   pattern, email  data-mismatch    min, max   data-range
//
// On submit the first field to fix gets focus, and each message sits beside
// its field (#<id>-error, created when the page has none). Pages call
// cairnFields.show(field, message) and .clear(field) for their own rules.
(() => {
 const iconFor = () => document.querySelector('#toast-icons')?.content.querySelector('[data-kind="error"]')?.innerHTML || '';
 const groups = '.field-row, .address-input, .address-field, .secret-field, .dropzone, .length-field, .search, .markdown-field';
 const describe = (field, id, on) => {
  const ids = (field.getAttribute('aria-describedby') || '').split(/\s+/).filter(x => x && x !== id);
  if (on) ids.unshift(id);
  if (ids.length) field.setAttribute('aria-describedby', ids.join(' '));
  else field.removeAttribute('aria-describedby');
 };
 const slot = field => {
  const id = field.id + '-error';
  let error = document.getElementById(id);
  if (!error) {
   error = document.createElement(field.closest('label') ? 'span' : 'p');
   error.className = 'field-error';
   error.id = id;
   error.innerHTML = iconFor() + '<span></span>';
   const anchor = field.closest(groups) || field;
   anchor.after(error);
  }
  return error;
 };
 const clear = field => {
  const error = document.getElementById(field.id + '-error');
  if (error) error.hidden = true;
  field.removeAttribute('aria-invalid');
  describe(field, field.id + '-error', false);
 };
 const show = (field, message) => {
  const error = slot(field);
  (error.querySelector('span:last-child') || error).textContent = message;
  error.hidden = false;
  field.setAttribute('aria-invalid', 'true');
  describe(field, error.id, true);
  return error;
 };
 // The message a field's own rules give, or '' when it passes.
 const check = field => {
  if (field.disabled || !field.willValidate) return '';
  const v = field.validity, d = field.dataset;
  if (v.valueMissing || (field.required && field.type !== 'file' && !field.value.trim())) return d.missing || 'Fill this in.';
  if (v.tooShort || (field.minLength > 0 && field.value && [...field.value].length < field.minLength)) return d.tooShort || d.mismatch || 'This is too short.';
  if (v.typeMismatch || v.patternMismatch) return d.mismatch || 'Check this.';
  if (v.rangeUnderflow || v.rangeOverflow || v.badInput || v.stepMismatch) return d.range || 'Check this number.';
  return '';
 };
 window.cairnFields = {show, clear, check};
 const ruled = '[required], [pattern], [minlength], [type=email], [min], [max]';

 document.addEventListener('submit', event => {
  const form = event.target;
  if (!form.noValidate) return;
  // A button that skips the checks (Remove password, Preview) skips them here too.
  if (event.submitter?.formNoValidate) return;
  const fields = [...form.elements].filter(el => el.matches('input, select, textarea') && el.id);
  const wrong = [];
  for (const field of fields) {
   const message = check(field);
   if (message) { show(field, message); wrong.push(field); }
   else if (field.getAttribute('aria-invalid') === 'true' && field.matches(ruled)) clear(field);
  }
  if (!wrong.length) return;
  event.preventDefault();
  event.stopImmediatePropagation();
  document.documentElement.dataset.inputModality = 'keyboard';
  wrong[0].focus();
 }, true);
 // A field that has been put right loses its message as the person types.
 // Only fields with rules of their own: a page that checks a field itself
 // (the editor's link name) clears that error itself.
 document.addEventListener('input', event => {
  const field = event.target;
  if (field.getAttribute?.('aria-invalid') !== 'true' || !field.form?.noValidate || !field.matches(ruled)) return;
  if (!check(field)) clear(field);
 });
})();
