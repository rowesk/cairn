(() => {
 const form=document.querySelector('#editor-form'), text=document.querySelector('#markdown'), preview=document.querySelector('#preview-section');
 let dirty=false;
 preview.hidden=true;
 form.addEventListener('input',()=>{dirty=true;});
 form.addEventListener('submit',event=>{
  if(event.defaultPrevented)return;
  if(event.submitter?.id==='preview-button') { preview.hidden=false; preview.scrollIntoView({block:'start'}); }
  else { dirty=false; }
 });
 document.querySelector('#close-editor-preview').addEventListener('click',()=>{preview.hidden=true;text.focus();});
 window.addEventListener('beforeunload',event=>{if(dirty){event.preventDefault();event.returnValue='';}});
 document.addEventListener('keydown',event=>{if((event.metaKey||event.ctrlKey)&&event.key==='s'){event.preventDefault();form.requestSubmit(document.querySelector('#save-button'));}});
})();

(() => {
 const body=document.querySelector('#markdown'), field=document.querySelector('#pasted-images'), status=document.querySelector('#image-status');
 let images=JSON.parse(field.value||'{}'), pending=0;
 const panel=document.querySelector('#sharing-panel'), trigger=document.querySelector('#access-trigger');
 const choices=[...document.querySelectorAll('[name=access]')];
 const updateAccess=()=>{
  const shared=choices.some(el=>el.checked&&el.value==='public');
  document.querySelector('#save-access').textContent=shared?'Markdown · Public when saved':'Markdown · Private when saved';
  document.querySelector('#access-label').textContent=shared?'Public link':'Private';
  document.querySelector('#save-label').textContent=shared?'Save & share':'Save page';
  document.querySelector('#sharing-note').textContent=shared?'Your page becomes public when you save and share.':'Only your devices can open this page.';
  document.querySelector('#address-origin').textContent=(shared?new URL(panel.dataset.publicBase).host:location.host)+'/';
  trigger.classList.toggle('is-public',shared);
  document.querySelector('#access-private-icon').hidden=shared;
  document.querySelector('#access-public-icon').hidden=!shared;
 };
 choices.forEach(el=>el.addEventListener('change',updateAccess));updateAccess();
 const positionPanel=()=>{
  const rect=trigger.getBoundingClientRect();
  panel.style.left=Math.max(16,Math.min(rect.left,innerWidth-panel.offsetWidth-16))+'px';
  panel.style.top=Math.max(12,Math.min(rect.bottom+10,innerHeight-panel.offsetHeight-12))+'px';
 };
 panel.addEventListener('toggle',()=>{const open=panel.matches(':popover-open');trigger.setAttribute('aria-expanded',String(open));if(open)positionPanel();});
 window.addEventListener('resize',()=>{if(panel.matches(':popover-open'))positionPanel();});
 document.querySelector('#page-slug').addEventListener('invalid',()=>{panel.showPopover();positionPanel();});
 body.addEventListener('paste',async event=>{
  const files=[...(event.clipboardData?.items||[])].filter(i=>i.kind==='file'&&i.type.startsWith('image/')).map(i=>i.getAsFile());
  if(!files.length)return;
  event.preventDefault();pending++;
  try {
   for(const file of files){
    const ext={'image/png':'png','image/jpeg':'jpg','image/gif':'gif','image/webp':'webp'}[file.type];
    if(!ext)throw Error('Paste a PNG, JPEG, GIF or WebP image.');
    if(file.size>4*1024*1024)throw Error('Each image must fit 4 MiB.');
    const total=Object.values(images).reduce((sum,v)=>sum+v.length*3/4,0);
    if(total+file.size>12*1024*1024||Object.keys(images).length>=32)throw Error('Use up to 32 images, totalling no more than 12 MiB.');
    const data=await new Promise((resolve,reject)=>{const r=new FileReader();r.onload=()=>resolve(r.result.split(',')[1]);r.onerror=()=>reject(Error('Could not read that image. Try pasting it again.'));r.readAsDataURL(file);});
    const name='pasted-'+Date.now().toString(36)+'-'+Math.random().toString(36).slice(2,8)+'.'+ext;
    images[name]=data;field.value=JSON.stringify(images);
    body.setRangeText('\n![Pasted image](?asset='+name+')\n',body.selectionStart,body.selectionEnd,'end');body.dispatchEvent(new Event('input',{bubbles:true}));
    status.textContent=Object.keys(images).length+(Object.keys(images).length===1?' image attached. Preview to see it.':' images attached. Preview to see them.');
   }
  }catch(error){status.textContent=error.message;}finally{pending--;}
 });
 document.querySelector('#editor-form').addEventListener('submit',event=>{if(pending){event.preventDefault();status.textContent='Wait for the image to finish attaching, then try again.';}},true);
})();
