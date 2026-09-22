(() => {
 const dialog = document.getElementById('preview');
 if (!dialog || typeof dialog.showModal !== 'function') return;
 const frame = document.getElementById('preview-frame');
 let trigger;
 document.addEventListener('click', event => {
  const link = event.target.closest('a[data-preview]');
  if (!link || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
  event.preventDefault();
  trigger = link;
  const path = new URL(link.href).pathname;
  document.getElementById('preview-title').textContent = link.dataset.title;
  document.getElementById('preview-byline').textContent = 'Prepared by ' + link.dataset.agent + ' · ' + link.dataset.date;
  document.getElementById('preview-request').textContent = link.dataset.request;
  document.getElementById('open-page').href = path;
  document.getElementById('manage-preview').href = '/manage' + path;
  dialog.querySelector('details').open = false;
  frame.src = '/_content' + path;
  dialog.showModal();
  document.body.style.overflow = 'hidden';
  document.getElementById('close-preview').focus();
 });
 document.getElementById('close-preview').addEventListener('click', () => dialog.close());
 dialog.addEventListener('click', event => {
  const box = dialog.getBoundingClientRect();
  if (event.target === dialog && (event.clientX < box.left || event.clientX > box.right || event.clientY < box.top || event.clientY > box.bottom)) dialog.close();
 });
 dialog.addEventListener('close', () => {
  frame.removeAttribute('src');
  document.body.style.overflow = '';
  if (trigger?.isConnected) trigger.focus();
 });
 document.addEventListener('keydown', event => {
  if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k' && !dialog.open) {
   event.preventDefault(); document.getElementById('search').focus();
  }
 });
})();

(() => {
 const menu=document.querySelector('.create-menu');
 if(!menu)return;
 document.addEventListener('click',event=>{if(!menu.contains(event.target))menu.open=false;});
 document.addEventListener('keydown',event=>{if(event.key==='Escape'&&menu.open){menu.open=false;menu.querySelector('summary').focus();}});
})();
