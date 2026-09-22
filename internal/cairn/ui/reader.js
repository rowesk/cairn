(() => {
 const button=document.querySelector('#copy-report-link'), tip=document.querySelector('#report-address'), status=document.querySelector('#copy-feedback');
 const url=new URL(button.dataset.url,location.href).href;
 tip.textContent=url;
 const positionTip=()=>{const rect=button.getBoundingClientRect();tip.style.left=Math.max(12,Math.min(rect.left,innerWidth-tip.offsetWidth-12))+'px';tip.style.top=Math.min(rect.bottom+8,innerHeight-tip.offsetHeight-12)+'px';};
 button.addEventListener('pointerenter',positionTip);button.addEventListener('focus',positionTip);window.addEventListener('resize',positionTip);

 button.addEventListener('click',async()=>{
  try {
   if(navigator.clipboard&&window.isSecureContext)await navigator.clipboard.writeText(url);
   else {
    const field=document.createElement('textarea');field.value=url;field.style.cssText='position:fixed;left:-9999px;top:0';document.body.append(field);
    let copied=false;try {field.select();copied=document.execCommand('copy');}finally{field.remove();button.focus({preventScroll:true});}
    if(!copied)throw Error('copy failed');
   }
   status.textContent='Link copied';
  }catch {status.textContent='Could not copy. Select the address below.';tip.classList.add('copy-failed');}
 });
})();

(() => {
 const frame=document.querySelector('.reading-shell>iframe'),bar=document.querySelector('.compact-reader'),nav=document.querySelector('.reading-nav'),title=document.querySelector('.report-heading h1'),mobile=matchMedia('(max-width:700px)');
 let pending=false, width=innerWidth;
 function update(){pending=false;const visible=mobile.matches&&nav.getBoundingClientRect().bottom<=0;bar.classList.toggle('is-visible',visible);bar.classList.toggle('has-title',title.getBoundingClientRect().bottom<=56);bar.setAttribute('aria-hidden',String(!visible));const link=bar.querySelector('a');if(link)link.tabIndex=visible?0:-1;}
 window.addEventListener('scroll',()=>{if(!pending){pending=true;requestAnimationFrame(update);}},{passive:true});
 window.addEventListener('message',event=>{if(event.source!==frame.contentWindow||event.data?.type!=='cairn:report-height')return;const height=event.data.height;if(!mobile.matches||!Number.isFinite(height)||height<1||height>200000)return;frame.style.height=Math.max(200,Math.ceil(height))+'px';document.body.classList.add('flowing-report');});
 function reset(){if(!mobile.matches){frame.style.height='';document.body.classList.remove('flowing-report');}else{frame.style.height='200px';frame.contentWindow?.postMessage({type:'cairn:measure'},'*');}update();}
 frame.addEventListener('load',reset);mobile.addEventListener('change',reset);window.addEventListener('resize',()=>{if(innerWidth!==width){width=innerWidth;reset();}});reset();
})();
