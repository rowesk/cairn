(() => {
 const button=document.querySelector('#copy-report-link'), tip=document.querySelector('#report-address'), status=document.querySelector('#copy-feedback'), compact=document.querySelector('.compact-copy');
 const url=new URL(button.dataset.url,location.href).href;
 tip.textContent=url;
 const positionTip=()=>{const rect=button.getBoundingClientRect();tip.style.left=Math.max(12,Math.min(rect.left,innerWidth-tip.offsetWidth-12))+'px';tip.style.top=Math.min(rect.bottom+8,innerHeight-tip.offsetHeight-12)+'px';};
 button.addEventListener('pointerenter',positionTip);button.addEventListener('focus',positionTip);window.addEventListener('resize',positionTip);

 async function copy(from){
  try {
   if(navigator.clipboard&&window.isSecureContext)await navigator.clipboard.writeText(url);
   else {
    const field=document.createElement('textarea');field.value=url;field.style.cssText='position:fixed;left:-9999px;top:0';document.body.append(field);
    let copied=false;try {field.select();copied=document.execCommand('copy');}finally{field.remove();from.focus({preventScroll:true});}
    if(!copied)throw Error('copy failed');
   }
   return true;
  }catch {return false;}
 }
 button.addEventListener('click',async()=>{
  if(await copy(button))status.textContent='Link copied';
  else {status.textContent='Could not copy. Select the address below.';tip.classList.add('copy-failed');}
 });
 let reset;
 compact?.addEventListener('click',async()=>{
  const copied=await copy(compact);
  status.textContent=copied?'Link copied':'Could not copy. Scroll to the top to select the address.';
  compact.classList.toggle('is-copied',copied);clearTimeout(reset);reset=setTimeout(()=>compact.classList.remove('is-copied'),1600);
 });
})();

(() => {
 const box=document.querySelector('.report-header .report-request');
 if(!box)return;
 const text=box.querySelector('p'),toggle=box.querySelector('.request-toggle');
 function check(){
  if(toggle.getAttribute('aria-expanded')==='true')return;
  box.classList.add('is-clamped');
  const over=text.scrollHeight>text.clientHeight+1;
  box.classList.toggle('is-clamped',over);toggle.hidden=!over;
 }
 toggle.addEventListener('click',()=>{
  const open=toggle.getAttribute('aria-expanded')!=='true';
  toggle.setAttribute('aria-expanded',String(open));toggle.textContent=open?'Show less':'Show full request';box.classList.toggle('is-clamped',!open);
 });
 check();window.addEventListener('resize',check);document.fonts?.ready.then(check);
})();

(() => {
 const root=document.documentElement,body=document.body,frame=document.querySelector('.reading-shell>iframe'),bar=document.querySelector('.compact-reader'),nav=document.querySelector('.reading-nav'),toggle=bar.querySelector('.compact-title'),details=document.querySelector('#reader-details'),scrim=document.querySelector('.reader-scrim'),progress=bar.querySelector('.reading-progress'),reduced=matchMedia('(prefers-reduced-motion: reduce)');
 const toc=document.querySelector('.reader-toc'),contentsButton=bar.querySelector('.compact-contents'),section=bar.querySelector('.compact-section'),panel=toc?.querySelector('.toc-panel');
 const links=toc?[...toc.querySelectorAll('a[data-id]')]:[],controls=[...bar.querySelectorAll('a,button')];
 // Bar height plus breathing room, so a jumped-to heading is not hidden under the bar.
 const offset=64;
 let pending=false,shown=false,floating=false,sheet=null,openedAt=0,width=innerWidth,flowing=true,growth=[],restore=null,positions=new Map(),active=null;
 let wanted=null;try{wanted=decodeURIComponent(location.hash.slice(1));}catch{}
 if(!links.some(link=>link.dataset.id===wanted))wanted=null;
 // A deep link follows the report's late layout shifts (fonts, images) until the reader scrolls.
 let settle=0;
 for(const type of ['wheel','touchstart','keydown'])addEventListener(type,()=>{wanted=null;},{passive:true,once:true});

 // One sheet at a time drops below the bar: page details or, when the margin is too narrow, the contents.
 const isOpen=()=>sheet!==null;
 function openSheet(name){
  closeSheet(false);sheet=name;openedAt=scrollY;scrim.hidden=false;
  if(name==='details'){details.hidden=false;toggle.setAttribute('aria-expanded','true');return;}
  body.classList.add('contents-open');contentsButton.setAttribute('aria-expanded','true');
  if(root.dataset.inputModality==='keyboard')(active||links[0])?.focus({preventScroll:true});
  if(active)toc.scrollTop=Math.max(0,active.offsetTop-toc.clientHeight/3);
 }
 function closeSheet(refocus){
  if(!sheet)return;
  const was=sheet;sheet=null;scrim.hidden=true;details.hidden=true;toggle.setAttribute('aria-expanded','false');
  body.classList.remove('contents-open');contentsButton?.setAttribute('aria-expanded','false');
  if(refocus)(was==='details'?toggle:contentsButton).focus({preventScroll:true});
 }
 toggle.addEventListener('click',()=>sheet==='details'?closeSheet(false):openSheet('details'));
 contentsButton?.addEventListener('click',()=>sheet==='contents'?closeSheet(false):openSheet('contents'));
 scrim.addEventListener('click',()=>closeSheet(false));
 document.addEventListener('keydown',event=>{if(event.key==='Escape'&&isOpen())closeSheet(true);});

 function show(next){
  if(next===shown)return;
  shown=next;bar.classList.toggle('is-visible',next);bar.setAttribute('aria-hidden',String(!next));
  for(const control of controls)control.tabIndex=next?0:-1;
  if(!next)closeSheet(false);
 }
 function mark(link){
  if(link===active)return;
  active?.removeAttribute('aria-current');link?.setAttribute('aria-current','location');active=link;
  section.textContent=link?link.textContent:'';toggle.classList.toggle('has-section',!!link);
  // Keep the current entry in view inside a long contents list without scrolling the page.
  if(link&&floating){
   const top=link.offsetTop,bottom=top+link.offsetHeight;
   if(top<panel.scrollTop+48)panel.scrollTop=top-48;else if(bottom>panel.scrollTop+panel.clientHeight-24)panel.scrollTop=bottom-panel.clientHeight+24;
  }
 }
 function update(){
  pending=false;
  // The bar takes over from the cover's navigation and stays for the rest of the read.
  show(nav.getBoundingClientRect().bottom<=0);
  if(isOpen()&&Math.abs(scrollY-openedAt)>60)closeSheet(false);
  const rect=frame.getBoundingClientRect(),span=rect.height-innerHeight;
  progress.style.transform='scaleX('+(span>0?Math.min(1,Math.max(0,-rect.top/span)):rect.top<=0?1:0)+')';
  if(links.length&&flowing&&positions.size){
   const line=offset+8;let current=null;
   for(const link of links){const top=positions.get(link.dataset.id);if(top===undefined)continue;if(top+rect.top<=line)current=link;else break;}
   mark(current);
  }
 }
 const queue=()=>{if(!pending){pending=true;requestAnimationFrame(update);}};
 window.addEventListener('scroll',queue,{passive:true});

 // The report grows to its full height so the window does all the scrolling. A report
 // that keeps growing as it is resized (viewport-height layouts) keeps its own scroll.
 function stopFlowing(){flowing=false;restore=null;frame.style.height='';body.classList.remove('flowing-report');mark(null);}
 function resize(height){
  const now=performance.now();growth=growth.filter(time=>now-time<1500);
  if(height>frame.offsetHeight+1&&growth.push(now)>20)return stopFlowing();
  frame.style.height=Math.max(200,Math.ceil(height))+'px';body.classList.add('flowing-report');
  if(restore!==null){const top=frame.getBoundingClientRect().top+scrollY;scrollTo(0,top+restore*frame.offsetHeight);restore=null;}
  queue();
 }
 function jump(top,smooth){
  const target=frame.getBoundingClientRect().top+scrollY+top-offset;
  scrollTo({top:Math.max(0,target),behavior:smooth&&!reduced.matches?'smooth':'auto'});
 }
 function goTo(id,smooth){if(flowing&&positions.has(id))jump(positions.get(id),smooth);else frame.contentWindow?.postMessage({type:'cairn:go',id},'*');}
 function headings(items){
  if(!Array.isArray(items)||items.length>500)return;
  const next=new Map();
  for(const item of items){if(!Array.isArray(item)||typeof item[0]!=='string'||item[0].length>512||!Number.isFinite(item[1])||item[1]<0||item[1]>200000)return;next.set(item[0],item[1]);}
  positions=next;
  if(wanted&&positions.has(wanted)&&body.classList.contains('flowing-report')){
   settle||=performance.now()+2000;
   if(performance.now()<settle)goTo(wanted,false);else wanted=null;
  }
  queue();
 }
 window.addEventListener('message',event=>{
  if(event.source!==frame.contentWindow)return;
  const data=event.data;
  if(data?.type==='cairn:headings')return headings(data.items);
  if(data?.type==='cairn:layout')return place(data);
  const value=data?.type==='cairn:report-height'?data.height:data?.type==='cairn:scroll-to'?data.top:NaN;
  if(!Number.isFinite(value)||value<0||value>200000)return;
  if(data.type==='cairn:report-height'&&flowing&&value>=1)resize(value);
  else if(data.type==='cairn:scroll-to')jump(value,true);
 });
 toc?.addEventListener('click',event=>{
  const link=event.target.closest('a[data-id]');
  if(!link||event.button!==0||event.metaKey||event.ctrlKey||event.shiftKey||event.altKey)return;
  event.preventDefault();closeSheet(false);goTo(link.dataset.id,true);
  try{history.replaceState(null,'','#'+encodeURIComponent(link.dataset.id));}catch{}
 });
 // The contents sit in the report's own left margin, beside its text, when the margin has room.
 const gap=40,edge=20,narrowest=168,widest=260,colour=value=>typeof value==='string'&&value.length<64&&CSS.supports('color',value);
 function place(report){
  if(!toc||![report.left,report.lead].every(n=>Number.isFinite(n)&&n>=0&&n<=200000))return;
  const room=report.left-gap-edge,fits=room>=narrowest;
  if(fits){
   const width=Math.min(widest,room);
   toc.style.setProperty('--toc-left',report.left-gap-width+'px');toc.style.setProperty('--toc-width',width+'px');toc.style.setProperty('--toc-lead',report.lead+'px');
   if(colour(report.ink))toc.style.setProperty('--toc-ink',report.ink);
   if(colour(report.accent))toc.style.setProperty('--toc-accent',report.accent);
   if(colour(report.paper)&&!/^rgba\(.*,\s*0\)$/.test(report.paper)&&report.paper!=='transparent')toc.style.setProperty('--toc-paper',report.paper);
   if(typeof report.font==='string'&&report.font.length<300&&!/[;{}]/.test(report.font))toc.style.setProperty('--toc-font',report.font);
  }
  if(fits!==floating){floating=fits;body.classList.toggle('toc-floating',fits);if(fits&&sheet==='contents')closeSheet(false);}
 }
 function measure(){
  if(!flowing)return;
  const rect=frame.getBoundingClientRect();
  restore=rect.top<0&&rect.height>0?-rect.top/rect.height:null;
  frame.style.height='200px';frame.contentWindow?.postMessage({type:'cairn:measure'},'*');
 }
 frame.addEventListener('load',measure);
 window.addEventListener('resize',()=>{if(innerWidth!==width){width=innerWidth;measure();}queue();});
 measure();update();
})();
