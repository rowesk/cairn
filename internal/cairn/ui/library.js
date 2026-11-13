(() => {
 // The split create button's menu closes on an outside click or Escape.
 const menu = document.querySelector('.create-menu');
 if (!menu) return;
 document.addEventListener('click', event => { if (!menu.contains(event.target)) menu.open = false; });
 document.addEventListener('keydown', event => {
  if (event.key === 'Escape' && menu.open) { menu.open = false; menu.querySelector('summary').focus(); }
 });
})();

(() => {
 // Drop one-time confirmations from the address so a reload shows the plain list.
 const url = new URL(location.href), once = ['kindle', 'page', 'deleted', 'cleanup', 'done'];
 if (once.some(name => url.searchParams.has(name))) {
  once.forEach(name => url.searchParams.delete(name));
  history.replaceState(history.state, '', url.pathname + url.search + (url.hash === '#notice' ? '' : url.hash));
 }
})();

(() => {
 const sheet = document.getElementById('pages');
 if (!sheet) return;
 const $ = (selector, root = document) => root.querySelector(selector);
 const $$ = (selector, root = document) => [...root.querySelectorAll(selector)];
 const root = document.documentElement;
 const csrf = sheet.dataset.csrf, view = sheet.dataset.view;
 const form = $('#filters'), search = $('#search'), results = $('#results'), status = $('#list-status'), count = $('#list-count');
 const rowMenu = $('#row-menu'), dialog = $('#preview'), frame = $('#preview-frame'), sheetOfKeys = $('#shortcuts');
 if (frame) cairnFollowScheme(frame);
 const narrow = matchMedia('(max-width: 650px)');
 const keysOff = () => root.dataset.shortcuts === 'off';
 const pages = n => n + ' ' + (n === 1 ? sheet.dataset.noun : sheet.dataset.nouns);
 const rows = () => $$('#rows .report-row');
 const rowOf = el => el?.closest?.('.report-row');
 // A row on screen now, even if the list was redrawn since it was picked.
 const live = row => row?.isConnected ? row : rows().find(r => r.dataset.slug === row?.dataset.slug);
 const typing = el => el && (el.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(el.tagName));
 const keyboard = () => { root.dataset.inputModality = 'keyboard'; };
 $('.search-submit', form).hidden = true;
 const hint = $('#show-shortcuts');
 if (hint && !keysOff()) hint.hidden = false;
 if (keysOff()) {
  // The keys do nothing, so nothing should announce them.
  $$('[aria-keyshortcuts]').forEach(el => {
   if (el === search) el.setAttribute('aria-keyshortcuts', 'Meta+K Control+K');
   else el.removeAttribute('aria-keyshortcuts');
  });
 }
 if (!/Mac|iPhone|iPad/.test(navigator.platform)) $$('[data-mod]').forEach(key => { key.textContent = 'Ctrl'; });

 // Phones: a shorter search placeholder, and the author and order choices
 // behind one Filters control that shows a dot while either is in use.
 const longPlaceholder = search.placeholder;
 const fitPlaceholder = () => { search.placeholder = narrow.matches ? search.dataset.shortPlaceholder : longPlaceholder; };
 narrow.addEventListener('change', fitPlaceholder);
 fitPlaceholder();
 const toggle = $('#filter-toggle');
 const markFilters = () => {
  const on = [...$$('select', form)].some(select => select.value !== '');
  toggle.classList.toggle('is-filtered', on);
  toggle.setAttribute('aria-label', on ? 'Filters, in use' : 'Filters');
 };
 toggle.hidden = false;
 toggle.addEventListener('click', () => {
  const open = !form.classList.contains('show-filters');
  form.classList.toggle('show-filters', open);
  toggle.setAttribute('aria-expanded', String(open));
  if (open) $('select', form).focus();
 });
 markFilters();

 // Confirmations and errors go to the page's toast region.
 let lastUndo = null;
 const say = (text, undo, kind = 'success') => {
  lastUndo = undo || null;
  cairnToast({kind, text, actions: undo ? [{label: 'Undo', onClick: () => { lastUndo = null; undo(); }}] : []});
 };

 // The count under the list and the status a screen reader announces.
 const announce = () => {
  const body = $('.list-body', results);
  if (!body) return;
  const total = Number(body.dataset.total), shown = rows().length;
  let text = body.dataset.count;
  if (shown && shown < total && text.includes(' ·')) text = shown + ' of ' + pages(total) + text.slice(text.indexOf(' ·'));
  count.textContent = text;
  status.textContent = search.value.trim() ? (total ? pages(total) + ' match' : 'No ' + sheet.dataset.nouns + ' match') : (text || 'No ' + sheet.dataset.nouns + ' here');
 };

 // Row actions: the "…" link to Manage becomes a menu button.
 const upgrade = root => {
  $$('a.row-more', root).forEach(link => {
   const row = rowOf(link), button = document.createElement('button');
   button.type = 'button';
   button.className = 'row-more';
   button.innerHTML = link.innerHTML;
   button.setAttribute('aria-label', 'Actions for ' + row.dataset.title);
   button.setAttribute('aria-haspopup', 'menu');
   button.setAttribute('aria-expanded', 'false');
   button.setAttribute('popovertarget', 'row-menu');
   link.replaceWith(button);
  });
 };
 let menuRow = null, menuButton = null;
 const buildMenu = row => {
  menuRow = row;
  const slug = row.dataset.slug;
  const wanted = {edit: 'editable' in row.dataset, copy: true, archive: view !== 'archive', restore: view === 'archive', kindle: true, 'kindle-setup': true, manage: true};
  rowMenu.replaceChildren(...$('#row-menu-items').content.cloneNode(true).children);
  $$('[role=menuitem]', rowMenu).forEach(item => { item.tabIndex = -1; });
  $$('[data-act]', rowMenu).forEach(item => {
   const act = item.dataset.act;
   if (!wanted[act]) { item.remove(); return; }
   if (act === 'kindle') item.action = '/manage/' + slug;
   if (act === 'edit') item.href = '/edit/' + slug;
   if (act === 'manage') item.href = '/manage/' + slug;
  });
 };
 const placeMenu = () => {
  if (!menuButton) return;
  const r = menuButton.getBoundingClientRect(), w = rowMenu.offsetWidth || 248, h = rowMenu.offsetHeight;
  rowMenu.style.left = Math.max(12, Math.min(r.right - w, innerWidth - w - 12)) + 'px';
  rowMenu.style.top = Math.max(12, h && r.bottom + 6 + h > innerHeight - 12 ? r.top - h - 6 : r.bottom + 6) + 'px';
 };
 document.addEventListener('click', event => {
  const button = event.target.closest('button.row-more');
  if (!button || rowMenu.matches(':popover-open')) return;
  menuButton = button;
  buildMenu(rowOf(button));
 });
 rowMenu.addEventListener('toggle', event => {
  const open = event.newState === 'open';
  menuButton?.setAttribute('aria-expanded', String(open));
  if (open) { placeMenu(); $('[role=menuitem]', rowMenu)?.focus(); }
  else if (!document.activeElement || document.activeElement === document.body || rowMenu.contains(document.activeElement)) menuButton?.focus();
 });
 rowMenu.addEventListener('keydown', event => {
  const items = $$('[role=menuitem]', rowMenu), at = items.indexOf(document.activeElement);
  const go = i => { event.preventDefault(); items[(i + items.length) % items.length]?.focus(); };
  if (event.key === 'ArrowDown') go(at + 1);
  else if (event.key === 'ArrowUp') go(at - 1);
  else if (event.key === 'Home') go(0);
  else if (event.key === 'End') go(items.length - 1);
  else if (event.key === 'Tab') rowMenu.hidePopover();
 });
 rowMenu.addEventListener('click', event => {
  const item = event.target.closest('[data-act]');
  if (!item || !menuRow) return;
  const row = menuRow, act = item.dataset.act;
  if (act === 'copy') { event.preventDefault(); rowMenu.hidePopover(); copyLink(row); }
  else if (act === 'archive' || act === 'restore') { event.preventDefault(); rowMenu.hidePopover(); archive(row); }
 });

 const copyLink = async row => {
  const link = new URL(row.dataset.link, location.href).href;
  try {
   if (navigator.clipboard && isSecureContext) await navigator.clipboard.writeText(link);
   else {
    const field = document.createElement('textarea');
    field.value = link;
    field.className = 'sr-only';
    document.body.append(field);
    field.select();
    const copied = document.execCommand('copy');
    field.remove();
    if (!copied) throw Error();
   }
   say('Link copied: ' + link);
  } catch { say('Cairn couldn’t reach the clipboard. The link is ' + link, null, 'error'); }
 };

 // Archive or restore in place, with Undo.
 let countRequest;
 const counts = async () => {
  countRequest?.abort();
  const controller = new AbortController();
  countRequest = controller;
  try {
   const response = await fetch(location.href, {signal: controller.signal});
   if (!response.ok) return;
   const doc = new DOMParser().parseFromString(await response.text(), 'text/html');
   if (controller.signal.aborted) return;
   for (const selector of ['.topbar .topnav, .topbar .topbar-spacer', '.library-tabs']) {
    const current = $(selector), updated = $(selector, doc);
    if (current && updated) current.replaceWith(updated);
   }
  } catch { /* Keep the links already on screen if the refresh fails. */ }
 };
 const post = async (slug, archived) => {
  const body = new URLSearchParams();
  body.set('csrf', csrf);
  body.set('slug', slug);
  body.set('archived', archived ? '1' : '0');
  body.set('return', location.pathname + location.search);
  const response = await fetch('/_library/archive', {method: 'POST', body, headers: {Accept: 'application/json'}});
  const answer = await response.json().catch(() => ({}));
  if (!response.ok) throw Error(answer.message || 'Cairn couldn’t change that page. Try again.');
  return answer;
 };
 const archive = async row => {
  const toArchive = view !== 'archive', slug = row.dataset.slug, title = row.dataset.title;
  const list = rowOf(row) && row.parentNode, next = row.nextElementSibling;
  const neighbour = rows()[rows().indexOf(row) + 1] || rows()[rows().indexOf(row) - 1];
  const hadFocus = row.contains(document.activeElement) || dialog.open;
  try { await post(slug, toArchive); } catch (error) { say(error.message, null, 'error'); return; }
  row.remove();
  counts();
  adjustTotal(-1);
  if (hadFocus && neighbour) select(neighbour, true);
  const undo = async () => {
   try { await post(slug, !toArchive); } catch (error) { say(error.message, null, 'error'); return; }
   if (!list.isConnected) { load(location.href, 'none'); say((toArchive ? 'Restored “' : 'Archived “') + title + '”.'); return; }
   const before = next && next.isConnected ? next : null;
   list.insertBefore(row, before);
   counts();
   adjustTotal(1);
   select(row, true);
   say((toArchive ? 'Restored “' : 'Archived “') + title + (toArchive ? '” to your library.' : '” again.'));
  };
  say((toArchive ? 'Archived “' + title + '”. Its links still work.' : 'Restored “' + title + '” to your library.'), undo);
 };
 const adjustTotal = delta => {
  const body = $('.list-body', results);
  if (!body) return;
  const total = Number(body.dataset.total) + delta;
  body.dataset.total = total;
  body.dataset.count = body.dataset.count.replace(/^(\d+ of )?\d+ (?:pages?|files?|links?)/, pages(total));
  announce();
 };

 // Selection: J and K move focus between rows, so a screen reader follows.
 let selected = null;
 const select = (row, focus) => {
  if (!row) return;
  rows().forEach(r => r.classList.toggle('is-selected', r === row));
  selected = row;
  if (focus) {
   keyboard();
   $('.row-title', row).focus({preventScroll: true});
   row.scrollIntoView({block: 'nearest'});
  }
 };
 results.addEventListener('focusin', event => { const row = rowOf(event.target); if (row) select(row, false); });
 const move = step => {
  const all = rows();
  if (!all.length) return;
  const at = all.indexOf(rowOf(document.activeElement) || selected);
  select(all[at < 0 ? (step > 0 ? 0 : all.length - 1) : Math.max(0, Math.min(all.length - 1, at + step))], true);
 };

 // The preview drawer. Ordinary clicks preview; Enter, modified clicks and
 // phones open the full reader.
 let current = null, trigger = null;
 const show = async row => {
  if (!row) return;
  if (narrow.matches) { location.href = $('.row-title', row).href; return; }
  current = row;
  select(row, false);
  const slug = row.dataset.slug, all = rows(), at = all.indexOf(row);
  $('#preview-title').textContent = row.dataset.title;
  $('#preview-byline').textContent = 'Prepared by ' + row.dataset.agent + ' · ' + row.dataset.date;
  $('#open-page').href = '/' + slug;
  $('#manage-preview').href = '/manage/' + slug;
  $('#preview-prev').disabled = at <= 0;
  $('#preview-next').disabled = at >= all.length - 1;
  const requestToggle = $('#preview-request-toggle'), box = $('#preview-request');
  requestToggle.hidden = true;
  requestToggle.setAttribute('aria-expanded', 'false');
  box.hidden = true;
  frame.src = '/_content/' + slug + cairnSchemeQuery();
  row.classList.remove('is-unread');
  $('.unread-dot', row)?.remove();
  $('.row-title .sr-only', row)?.remove();
  if (!dialog.open) {
   dialog.showModal();
   document.body.style.overflow = 'hidden';
  }
  // Focus starts on the page's title, so a screen reader names the page
  // and the next Tab reaches the drawer's controls.
  $('#preview-title').focus({preventScroll: true});
  try {
   const response = await fetch('/_library/request/' + encodeURIComponent(slug), {headers: {'X-Cairn-Token': csrf}});
   const answer = response.ok ? await response.json() : {};
   const words = answer.summary || answer.request || '';
   if (current === row && words) { $('#preview-request-text').textContent = answer.request || words; requestToggle.hidden = false; }
  } catch {}
 };
 const step = n => {
  const all = rows(), at = all.indexOf(live(current));
  if (at < 0 || !all[at + n]) return;
  show(all[at + n]);
 };
 document.addEventListener('click', event => {
  const link = event.target.closest('a[data-preview]');
  if (!link || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey || event.detail === 0 || narrow.matches || typeof dialog.showModal !== 'function') return;
  event.preventDefault();
  trigger = link;
  show(rowOf(link));
 });
 $('#preview-prev').addEventListener('click', () => step(-1));
 $('#preview-next').addEventListener('click', () => step(1));
 $('#close-preview').addEventListener('click', () => dialog.close());
 $('#preview-request-toggle').addEventListener('click', event => {
  const open = event.currentTarget.getAttribute('aria-expanded') !== 'true';
  event.currentTarget.setAttribute('aria-expanded', String(open));
  $('#preview-request').hidden = !open;
 });
 dialog.addEventListener('click', event => {
  const box = dialog.getBoundingClientRect();
  if (event.target === dialog && (event.clientX < box.left || event.clientX > box.right || event.clientY < box.top || event.clientY > box.bottom)) dialog.close();
 });
 const archiveCurrent = () => {
  const row = live(current), all = rows(), next = all[all.indexOf(row) + 1] || all[all.indexOf(row) - 1];
  if (row) archive(row).then(() => next ? show(next) : dialog.close());
 };
 dialog.addEventListener('keydown', event => {
  if (typing(event.target) || event.metaKey || event.ctrlKey || event.altKey) return;
  const key = event.key.toLowerCase();
  if (key === 'arrowdown' || (key === 'j' && !keysOff())) { event.preventDefault(); step(1); }
  else if (key === 'arrowup' || (key === 'k' && !keysOff())) { event.preventDefault(); step(-1); }
  else if (key === 'e' && current && !keysOff()) { event.preventDefault(); archiveCurrent(); }
 });
 // Keys pressed inside the previewed page. The report forwards only the
 // keys asked for, and only after a real key press it did not use itself.
 frame.addEventListener('load', () => {
  try { frame.contentWindow?.postMessage({type: 'cairn:keys', keys: keysOff() ? ['Escape'] : ['Escape', 'j', 'k']}, '*'); } catch {}
 });
 window.addEventListener('message', event => {
  if (event.source !== frame.contentWindow || event.data?.type !== 'cairn:key') return;
  if (!dialog.open || document.activeElement !== frame) return;
  const key = String(event.data.key);
  if (key === 'Escape') { dialog.close(); return; }
  if (keysOff() || !navigator.userActivation?.isActive) return;
  if (key === 'j') step(1);
  else if (key === 'k') step(-1);
 });
 dialog.addEventListener('close', () => {
  frame.removeAttribute('src');
  document.body.style.overflow = '';
  const target = current ? $('.row-title', live(current) || current) : trigger;
  if (target?.isConnected) target.focus();
  current = null;
 });

 // Search as you type. The address follows the list, so a search can be
 // shared and Back returns to where you were.
 let controller = null, timer = 0, session = false;
 const address = () => {
  const params = new URLSearchParams();
  for (const [name, value] of new FormData(form)) if (value !== '') params.set(name, value);
  const query = params.toString();
  return '/' + (query ? '?' + query : '');
 };
 const load = async (url, mode) => {
  controller?.abort();
  controller = new AbortController();
  const fetchURL = new URL(url, location.href);
  fetchURL.searchParams.delete('n');
  fetchURL.searchParams.set('fragment', 'list');
  sheet.setAttribute('aria-busy', 'true');
  try {
   const response = await fetch(fetchURL, {signal: controller.signal, headers: {'X-Cairn-Token': csrf}});
   if (!response.ok) throw Error(String(response.status));
   results.innerHTML = await response.text();
   upgrade(results);
   selected = live(selected) || null;
   if (selected) select(selected, false);
   if (current) { const again = live(current); if (again) { current = again; again.classList.add('is-selected'); } }
   if (mode === 'push') history.pushState({list: true}, '', url);
   else if (mode === 'replace') history.replaceState({list: true}, '', url);
   announce();
  } catch (error) {
   if (error.name !== 'AbortError') location.assign(url);
  } finally {
   sheet.removeAttribute('aria-busy');
  }
 };
 // The first order is "Best match" while searching and "Newest first" otherwise.
 const order = form.elements.sort;
 const syncOrder = () => {
  const searching = search.value.trim() !== '', first = order.options[0];
  const newest = order.querySelector('option[value="newest"]');
  if (searching && first.textContent !== 'Best match') {
   first.textContent = 'Best match';
   if (!newest) order.add(new Option('Newest first', 'newest'), 1);
  } else if (!searching && first.textContent !== 'Newest first') {
   if (order.value === 'newest') order.value = '';
   newest?.remove();
   first.textContent = 'Newest first';
  }
 };
 const refresh = () => { syncOrder(); markFilters(); load(address(), session ? 'replace' : 'push'); session = true; };
 search.addEventListener('input', () => { clearTimeout(timer); timer = setTimeout(refresh, 140); });
 search.addEventListener('blur', () => { session = false; });
 search.addEventListener('keydown', event => {
  if (event.key === 'Escape') {
   event.preventDefault();
   if (search.value) { search.value = ''; clearTimeout(timer); refresh(); }
   else search.blur();
  } else if (event.key === 'ArrowDown' && rows().length) { event.preventDefault(); move(1); }
 });
 $$('select', form).forEach(select => select.addEventListener('change', () => { session = false; refresh(); session = false; }));
 form.addEventListener('submit', event => { event.preventDefault(); clearTimeout(timer); refresh(); });
 addEventListener('popstate', () => {
  const params = new URL(location.href).searchParams;
  search.value = params.get('q') || '';
  syncOrder();
  for (const select of $$('select', form)) select.value = params.get(select.name) || '';
  markFilters();
  session = false;
  load(location.href, 'none');
 });
 results.addEventListener('click', event => {
  const clear = event.target.closest('[data-clear-search]');
  if (!clear || event.metaKey || event.ctrlKey || event.shiftKey) return;
  event.preventDefault();
  search.value = '';
  $$('select', form).forEach(select => { select.value = ''; });
  markFilters();
  session = false;
  load(clear.href, 'push');
  search.focus();
 });

 // Show more pages in place; the address keeps how many are showing.
 results.addEventListener('click', async event => {
  const more = event.target.closest('.show-more');
  if (!more || event.metaKey || event.ctrlKey || event.shiftKey) return;
  event.preventDefault();
  const offset = Number(more.dataset.offset), url = new URL(location.href);
  url.searchParams.delete('n');
  url.searchParams.set('fragment', 'rows');
  url.searchParams.set('offset', offset);
  more.setAttribute('aria-disabled', 'true');
  more.textContent = 'Loading…';
  try {
   const response = await fetch(url, {headers: {'X-Cairn-Token': csrf}});
   if (!response.ok) throw Error();
   const holder = document.createElement('template');
   holder.innerHTML = await response.text();
   const next = holder.content.querySelector('.more');
   next?.remove();
   const added = [...holder.content.children];
   $('#rows').append(...added);
   upgrade($('#rows'));
   more.closest('.more').replaceWith(...(next ? [next] : []));
   const here = new URL(location.href);
   here.searchParams.set('n', rows().length);
   history.replaceState(history.state, '', here.pathname + here.search);
   announce();
   const first = added.find(el => el.classList.contains('report-row'));
   if (first) $('.row-title', first).focus({preventScroll: true});
  } catch {
   location.assign(more.href);
  }
 });

 // Keyboard shortcuts. Single keys act only while focus is in the list or
 // on the page itself, never in a field or another control, and not at all
 // when Settings turns them off. Tab order is unchanged.
 hint?.addEventListener('click', () => sheetOfKeys.showModal());
 $('#close-shortcuts').addEventListener('click', () => sheetOfKeys.close());
 sheetOfKeys.addEventListener('click', event => { if (event.target === sheetOfKeys) sheetOfKeys.close(); });
 document.addEventListener('keydown', event => {
  if (event.defaultPrevented) return;
  const key = event.key, active = document.activeElement;
  if ((event.metaKey || event.ctrlKey) && key.toLowerCase() === 'k' && !dialog.open) {
   event.preventDefault(); search.focus(); search.select(); return;
  }
  if ((event.metaKey || event.ctrlKey) && key.toLowerCase() === 'z' && lastUndo && !typing(event.target)) {
   event.preventDefault(); const undo = lastUndo; lastUndo = null; cairnDismissToast(); undo(); return;
  }
  if (event.metaKey || event.ctrlKey || event.altKey || typing(event.target) || dialog.open || sheetOfKeys.open || rowMenu.matches(':popover-open')) return;
  const onPage = !active || active === document.body || active.id === 'main';
  const inList = onPage || results.contains(active);
  const onRow = rowOf(active);
  if ((key === 'ArrowDown' || key === 'ArrowUp') && inList) { event.preventDefault(); move(key === 'ArrowDown' ? 1 : -1); return; }
  if (keysOff() || !inList) return;
  if (key === '/') { event.preventDefault(); search.focus(); search.select(); }
  else if (key === '?') { event.preventDefault(); sheetOfKeys.showModal(); }
  else if (key === 'j' || key === 'J') { event.preventDefault(); move(1); }
  else if (key === 'k' || key === 'K') { event.preventDefault(); move(-1); }
  else if ((key === ' ' || key === 'p' || key === 'P') && onRow && event.target.matches('.row-title')) { event.preventDefault(); trigger = event.target; show(onRow); }
  else if ((key === 'e' || key === 'E') && onRow) { event.preventDefault(); archive(onRow); }
 });

 upgrade(results);
 announce();
 status.textContent = '';
})();
