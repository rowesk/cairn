(() => {
 // Copy link: every [data-copy-link] copies the page's link and says so in place,
 // on the button itself, with the same words for screen readers.
 const body=document.body,url=new URL(body.dataset.link,location.href).href,status=document.querySelector('#copy-status');
 const is=name=>body.classList.contains(name);
 const said=!is('is-owner')?'Link copied.':!is('is-shared')?'Link copied. Only you can open it.':is('is-protected')?'Link copied. Visitors also need the password.':'Link copied. Anyone with it can read this page.';
 async function copy(from,url){
  try {
   if(navigator.clipboard&&window.isSecureContext)await navigator.clipboard.writeText(url);
   else {
    const field=document.createElement('textarea');field.value=url;field.setAttribute('readonly','');field.style.position='fixed';field.style.left='-9999px';field.style.top='0';document.body.append(field);
    let copied=false;try {field.select();copied=document.execCommand('copy');}finally{field.remove();from.focus({preventScroll:true});}
    if(!copied)throw Error('copy failed');
   }
   return true;
  }catch {return false;}
 }
 const timers=new Map();
 function announce(text,failed,url){
  status.textContent='';status.classList.toggle('is-failed',failed);
  requestAnimationFrame(()=>{
   status.textContent=text;
   if(failed){const address=document.createElement('span');address.className='copy-address';address.textContent=url;status.append(' ',address);getSelection()?.selectAllChildren(address);}
  });
 }
 for(const button of document.querySelectorAll('[data-copy-link]')){
  button.hidden=false;
  button.addEventListener('click',async()=>{
   const link=button.dataset.copyLink?new URL(button.dataset.copyLink,location.href).href:url;
   const copied=await copy(button,link);
   clearTimeout(timers.get(button));
   button.classList.toggle('is-copied',copied);
   announce(copied?said:'Could not copy the link. It is selected here:',!copied,link);
   timers.set(button,setTimeout(()=>button.classList.remove('is-copied'),2000));
  });
 }
 document.addEventListener('pointerdown',event=>{if(status.classList.contains('is-failed')&&!status.contains(event.target)){status.classList.remove('is-failed');status.textContent='';}});
})();

(() => {
 // The request starts at three lines with a toggle; without JavaScript it shows in full.
 const box=document.querySelector('.reader-cover .report-request');
 if(!box)return;
 const text=box.querySelector('.request-text'),toggle=box.querySelector('.request-toggle');
 if(!toggle)return;
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
 const root=document.documentElement,body=document.body,cover=document.querySelector('.reader-cover'),frame=document.querySelector('.report-frame');
 // The owner's cover and strip have a toolbar row the bar replaces; a visitor's strip is replaced whole.
 const nav=cover.querySelector('.reading-nav')||cover;
 const bar=document.querySelector('.compact-reader'),title=bar.querySelector('.compact-title'),hint=document.querySelector('#compact-title-hint'),details=document.querySelector('#reader-details'),scrim=document.querySelector('.reader-scrim'),progress=bar.querySelector('.reading-progress');
 const toc=document.querySelector('.reader-toc'),contentsButton=bar.querySelector('.compact-contents'),section=bar.querySelector('.compact-section'),page=bar.querySelector('.compact-page'),panel=toc?.querySelector('.toc-panel');
 const menu=document.querySelector('#reader-menu'),shortcuts=document.querySelector('#reader-shortcuts'),grid=document.querySelector('.reader-grid'),view=frame?.parentElement||document.querySelector('.file-page');
 const reduced=matchMedia('(prefers-reduced-motion: reduce)'),phone=matchMedia('(max-width: 760px)'),fine=matchMedia('(hover: hover) and (pointer: fine)');
 const links=toc?[...toc.querySelectorAll('a[data-id]')]:[],ids=new Set(links.map(link=>link.dataset.id)),controls=[...bar.querySelectorAll('a,button')];
 // The rail may float only on Cairn's own templates that keep their text in the column.
 const railAllowed=body.classList.contains('rail-layout');
 // Bar height plus breathing room, so a jumped-to heading is not hidden under the bar.
 const offset=64;
 let pending=false,shown=false,floating=false,sheet=null,opener=null,openedAt=0,menuFrom='cover',menuAt=0,width=innerWidth,flowing=true,growth=[],restore=null,positions=new Map(),starts=new Map(),active=null,lastView='',layout=null,returnTo=null;
 let wanted=null;try{wanted=decodeURIComponent(location.hash.slice(1));}catch{}
 if(!ids.has(wanted))wanted=null;
 // A deep link follows the report's late layout shifts (fonts, images) until the reader scrolls.
 let settle=0;
 for(const type of ['wheel','touchstart','keydown'])addEventListener(type,()=>{wanted=null;},{passive:true,once:true});

 // The title in the bar opens page details; on a phone it shows the current section and opens the contents.
 const titleSheet=()=>phone.matches&&toc?'contents':'details';
 function syncTitle(){
  const name=titleSheet();
  title.setAttribute('aria-controls',name==='contents'?'reader-contents':'reader-details');
  title.setAttribute('aria-expanded',String(sheet===name));
  hint.textContent=name==='contents'?'Shows the contents':'Shows page details';
 }
 // When the page title and the section do not both fit, the section alone shows: never two truncations.
 // Whole pixels hide a cut of less than one: the title can lose its last letters to an ellipsis while
 // scrollWidth still equals clientWidth plus one. The text's own layout box says exactly where it ends.
 const cut=el=>{const range=document.createRange();range.selectNodeContents(el);return range.getBoundingClientRect().right>el.getBoundingClientRect().right+0.5;};
 function fitTitle(){
  title.classList.remove('section-only');
  if(!section.textContent||getComputedStyle(page).display==='none')return;
  if(cut(page)||cut(section))title.classList.add('section-only');
 }
 // Any later change of room refits it: the bar's fonts arriving, the contents button coming or going.
 document.fonts?.addEventListener?.('loadingdone',()=>fitTitle());
 if(window.ResizeObserver){let fitting=0;const refit=new ResizeObserver(()=>{cancelAnimationFrame(fitting);fitting=requestAnimationFrame(fitTitle);});refit.observe(title);refit.observe(bar);}

 // One sheet at a time drops below the bar: page details or, when the rail does not float, the contents.
 // Opening one moves focus into it; Escape, the backdrop, focus leaving it or scrolling away closes it.
 function openSheet(name,from){
  closeSheet(false);sheet=name;opener=from||null;openedAt=scrollY;scrim.hidden=false;
  show(true);
  if(name==='details'){details.hidden=false;syncTitle();details.focus({preventScroll:true});return;}
  body.classList.add('contents-open');contentsButton?.setAttribute('aria-expanded','true');syncTitle();
  if(active)toc.scrollTop=Math.max(0,active.offsetTop-toc.clientHeight/3);
  (active||links[0])?.focus({preventScroll:true});
 }
 function closeSheet(refocus){
  if(!sheet)return;
  sheet=null;scrim.hidden=true;details.hidden=true;
  body.classList.remove('contents-open');contentsButton?.setAttribute('aria-expanded','false');syncTitle();
  pending=false;update();
  if(refocus){
   // An opener in the cover sits under the bar once the page has scrolled: hand focus to the bar's own control.
   let to=opener;
   if(to&&(!to.isConnected||to.closest('[inert]')||to.offsetParent===null))to=to.classList.contains('more-button')?bar.querySelector('.compact-more'):title;
   if(to&&!to.closest('[inert]')&&to.offsetParent!==null)to.focus({preventScroll:true});
  }
  opener=null;
 }
 // Focus leaving an open sheet closes it: to another control, or into the report frame (which the
 // window only reports as its own blur). When the whole window loses focus the active element stays.
 function leftSheet(){
  if(!sheet)return;
  const now=document.activeElement,box=sheet==='details'?details:toc;
  if(now===frame||(now!==document.body&&!box.contains(now)&&now!==title&&now!==contentsButton))closeSheet(false);
 }
 for(const box of [details,toc].filter(Boolean))box.addEventListener('focusout',event=>{
  const to=event.relatedTarget;
  if(!to)return setTimeout(leftSheet);
  if(sheet&&!box.contains(to)&&to!==title&&to!==contentsButton)closeSheet(false);
 });
 addEventListener('blur',()=>{leftSheet();setTimeout(leftSheet);});
 title.addEventListener('click',()=>{const name=titleSheet();sheet===name?closeSheet(true):openSheet(name,title);});
 contentsButton?.addEventListener('click',()=>sheet==='contents'?closeSheet(true):openSheet('contents',contentsButton));
 scrim.addEventListener('click',()=>closeSheet(false));
 document.addEventListener('keydown',event=>{if(event.key==='Escape'&&sheet){event.preventDefault();closeSheet(true);}});

 // The bar takes over from the toolbar and stays for the rest of the read. It also stays while it
 // holds focus, an open sheet or its menu, so focus never drops to the page.
 const holding=()=>sheet!==null||bar.contains(document.activeElement)||(menuFrom==='bar'&&menu?.matches(':popover-open'));
 function show(next){
  if(!next&&holding())next=true;
  if(next===shown)return;
  shown=next;bar.classList.toggle('is-visible',next);bar.setAttribute('aria-hidden',String(!next));
  for(const control of controls)control.tabIndex=next?0:-1;
  // The cover's own controls sit under the bar or above the screen: they leave the tab order,
  // so Tab from the middle of a page never jumps back to the top.
  nav.inert=next;
  if(next)fitTitle();
 }
 bar.addEventListener('focusout',()=>setTimeout(queue));
 function mark(link){
  if(link===active)return;
  active?.removeAttribute('aria-current');link?.setAttribute('aria-current','location');active=link;
  section.textContent=link?link.textContent:'';title.classList.toggle('has-section',!!link);
  fitTitle();
  // Keep the current entry in view inside a long contents list without scrolling the page.
  if(link&&floating){
   const top=link.offsetTop,bottom=top+link.offsetHeight;
   if(top<panel.scrollTop+48)panel.scrollTop=top-48;else if(bottom>panel.scrollTop+panel.clientHeight-24)panel.scrollTop=bottom-panel.clientHeight+24;
  }
 }
 // The report pins long tables' header rows below the bar; it needs to know which part of it is in view.
 function viewport(rect){
  if(!flowing||!frame?.contentWindow)return;
  const barBottom=shown?bar.offsetHeight:0,top=Math.max(0,Math.round(barBottom-rect.top)),height=Math.max(0,Math.round(innerHeight-Math.max(barBottom,rect.top)));
  const key=top+' '+height;
  if(key===lastView)return;
  lastView=key;frame?.contentWindow.postMessage({type:'cairn:viewport',top,height},'*');
 }
 // The section being read is the last one whose start has reached the bar's bottom edge. A section
 // starts where the space above its heading starts, so the moment the last line of the section before
 // it goes under the bar, and the heading is the first thing under the bar, the bar names it.
 // Sections in the last screen can never reach that edge, so the final stretch of scrolling is shared
 // between them in order: each is named while it is on screen, and the last one only at the very end.
 function reading(rect){
  const base=bar.offsetHeight||48,max=Math.max(0,document.documentElement.scrollHeight-innerHeight);
  const marks=[];
  for(const link of links){const top=starts.get(link.dataset.id);if(top!==undefined)marks.push([link,scrollY+rect.top+top-base]);}
  const late=marks.findIndex(([,at])=>at>max-2);
  if(late>=0){
   const count=marks.length-late,from=Math.max(late?marks[late-1][1]:0,max-Math.min(innerHeight/2,max));
   for(let i=late;i<marks.length;i++)marks[i][1]=Math.min(marks[i][1],from+(max-2-from)*(i-late+1)/count);
  }
  let now=null;
  for(const [link,at] of marks){if(at<=scrollY+1)now=link;else break;}
  return now;
 }
 function update(){
  pending=false;
  show(nav.getBoundingClientRect().bottom<=0);
  cover.inert=shown&&cover.getBoundingClientRect().bottom<=bar.offsetHeight;
  if(sheet&&Math.abs(scrollY-openedAt)>60)closeSheet(false);
  if(menu?.matches(':popover-open')&&Math.abs(scrollY-menuAt)>60)menu.hidePopover();
  const rect=(frame||view).getBoundingClientRect(),span=rect.height-innerHeight;
  progress.style.transform='scaleX('+(span>0?Math.min(1,Math.max(0,-rect.top/span)):rect.top<=0?1:0)+')';
  viewport(rect);
  if(links.length&&flowing&&positions.size)mark(reading(rect));
  railRoom(rect);
 }
 // The rail keeps its place and width beside the text, and always ends on a whole entry. A table,
 // figure or diagram wider than the text never runs under it: the rail ends above one coming up,
 // steps aside as a whole while one passes its top, and comes back once it has gone.
 function railRoom(rect){
  if(!floating){body.classList.remove('toc-yield');return;}
  let away=false,room=Math.round(rect.bottom-offset-24);
  const box=panel.getBoundingClientRect();
  if(layout?.wide?.length&&!toc.contains(document.activeElement)){
   for(const [top,bottom,left] of layout.wide){
    if(rect.left+left>=box.right+8||rect.top+bottom<=box.top)continue;
    if(rect.top+top<=box.top+48){away=true;break;}
    room=Math.min(room,Math.round(rect.top+top-box.top-24));
   }
  }
  room=Math.min(room,Math.round(innerHeight-box.top-48));
  if(room<120)away=true;
  body.classList.toggle('toc-yield',away);
  if(away)return;
  // The tallest height that ends between two entries.
  const list=panel.scrollHeight,scrolled=panel.scrollTop;let end=0;
  if(list-scrolled<=room)end=list-scrolled;
  else for(const link of links){const bottom=link.offsetTop+link.offsetHeight-scrolled;if(bottom<=room)end=Math.max(end,bottom);else if(link.offsetTop-scrolled>room)break;}
  const height=Math.max(end,Math.min(room,120));
  if(panel.dataset.height!==String(height)){panel.dataset.height=String(height);toc.style.setProperty('--toc-height',height+'px');}
  panel.classList.toggle('is-cut',list-scrolled>height+1);
 }
 const queue=()=>{if(!pending){pending=true;requestAnimationFrame(update);}};
 window.addEventListener('scroll',queue,{passive:true});
 panel?.addEventListener('scroll',queue,{passive:true});

 // The report grows to its full height so the window does all the scrolling. A report that keeps
 // growing as it is resized (viewport-height layouts), or claims a height its page could not need,
 // keeps its own scroll instead.
 const limit=Math.min(200000,Number(body.dataset.reportLimit)||200000);
 function settled(){body.classList.add('report-settled');}
 setTimeout(settled,4000);
 function stopFlowing(){flowing=false;restore=null;frame.style.height='';view.style.minHeight='';body.classList.remove('flowing-report');settled();mark(null);}
 function resize(height){
  if(height>limit)return stopFlowing();
  const now=performance.now();growth=growth.filter(time=>now-time<1500);
  if(height>frame.offsetHeight+1&&growth.push(now)>20)return stopFlowing();
  frame.style.height=Math.max(200,Math.ceil(height))+'px';view.style.minHeight='';body.classList.add('flowing-report');settled();
  if(restore!==null){const top=(frame||view).getBoundingClientRect().top+scrollY;scrollTo(0,top+restore*frame.offsetHeight);restore=null;}
  lastView='';queue();
 }
 function jump(top,smooth){
  const target=(frame||view).getBoundingClientRect().top+scrollY+top-offset;
  scrollTo({top:Math.max(0,target),behavior:smooth&&!reduced.matches?'smooth':'auto'});
 }
 function goTo(id,smooth){if(flowing&&positions.has(id))jump(positions.get(id),smooth);else frame?.contentWindow?.postMessage({type:'cairn:go',id},'*');}
 // Heading positions must name the page's own contents, in their order, inside the report. Each may
 // carry the space above its heading (0 to 96px), which is where its section starts.
 function headings(items){
  if(!Array.isArray(items)||items.length>500)return;
  const next=new Map(),from=new Map(),most=flowing?frame.offsetHeight+1:limit;let last=-1;
  for(const item of items){
   if(!Array.isArray(item)||(item.length!==2&&item.length!==3)||typeof item[0]!=='string'||!Number.isFinite(item[1])||item[1]<0||item[1]>most)return;
   const gap=item.length===3?item[2]:0;
   if(!Number.isFinite(gap)||gap<0||gap>96)return;
   if(!ids.has(item[0]))continue;
   if(item[1]<last)return;
   last=item[1];next.set(item[0],item[1]);
   // A jump lands a heading 16px under the bar, so a section never starts later than that.
   from.set(item[0],Math.max(0,item[1]-Math.max(gap,offset-48)));
  }
  positions=next;starts=from;
  if(wanted&&positions.has(wanted)&&body.classList.contains('flowing-report')){
   settle||=performance.now()+2000;
   if(performance.now()<settle)goTo(wanted,false);else wanted=null;
  }
  queue();
 }
 // What the report may say, and how often. Messages are coalesced to one of each kind a frame, so a
 // report that floods the reader only ever has its latest word taken. A scroll request must follow a
 // real click or key press inside the report, which activates the reader too.
 const latest=new Map();let draining=false,lastScroll=0;
 const activated=()=>navigator.userActivation?navigator.userActivation.isActive:true;
 function take(){
  draining=false;
  for(const [type,data] of latest){
   latest.delete(type);
   if(type==='cairn:headings')headings(data.items);
   else if(type==='cairn:layout')place(data);
   else if(type==='cairn:report-height'&&flowing&&Number.isFinite(data.height)&&data.height>=1)resize(data.height);
  }
 }
 window.addEventListener('message',event=>{
  if(event.source!==frame?.contentWindow)return;
  const data=event.data,type=data?.type;
  if(typeof type!=='string')return;
  // Clicks and Tab presses inside the report decide whether focus rings show, as they do in the reader.
  if(type==='cairn:input'){if(data.kind==='pointer'||data.kind==='keyboard')root.dataset.inputModality=data.kind;return;}
  if(type==='cairn:scroll-to'){
   const now=performance.now();
   if(!Number.isFinite(data.top)||data.top<0||data.top>frame.offsetHeight||!activated()||now-lastScroll<400)return;
   lastScroll=now;jump(data.top,true);return;
  }
  if(type!=='cairn:headings'&&type!=='cairn:layout'&&type!=='cairn:report-height')return;
  latest.set(type,data);
  if(!draining){draining=true;requestAnimationFrame(take);}
 });
 toc?.addEventListener('click',event=>{
  const link=event.target.closest('a[data-id]');
  if(!link||event.button!==0||event.metaKey||event.ctrlKey||event.shiftKey||event.altKey)return;
  event.preventDefault();closeSheet(false);goTo(link.dataset.id,true);
  try{history.replaceState(null,'',location.pathname+location.search+'#'+encodeURIComponent(link.dataset.id));}catch{}
 });

 // Contents float in the left margin when it has room for them beside the text, on template pages
 // that allow it. The text column is the grid's own, so the rail never depends on what the report
 // says about itself; the report only says where its first block starts and which blocks run wide.
 const gap=40,edge=16,narrowest=168,widest=260;
 const within=(value,most)=>Number.isFinite(value)&&value>=0&&value<=most;
 function place(report){
  if(report){
   const tall=flowing?frame.offsetHeight+1:limit,broad=frame.offsetWidth+1;
   if(!within(report.lead,600))return;
   // Wide blocks: at most 60 [top, bottom, left] triples inside the report.
   const wide=Array.isArray(report.wide)&&report.wide.length<=60&&report.wide.every(block=>Array.isArray(block)&&block.length===3&&within(block[0],tall)&&within(block[1],tall)&&block[1]>=block[0]&&within(block[2],broad))?report.wide:[];
   layout={lead:report.lead,wide};
  }
  if(!toc||!layout)return;
  // Template pages put their text column on the reader's grid at every width.
  const column=parseFloat(getComputedStyle(grid).paddingLeft)||0;
  const size=Math.min(widest,column-gap-edge),fits=railAllowed&&flowing&&size>=narrowest&&innerWidth>1000;
  if(fits){
   toc.style.setProperty('--toc-left',Math.round(column-gap-size)+'px');toc.style.setProperty('--toc-width',Math.round(size)+'px');toc.style.setProperty('--toc-lead',layout.lead+'px');
  }
  if(fits!==floating){
   floating=fits;body.classList.toggle('toc-floating',fits);
   if(fits&&sheet==='contents')closeSheet(false);
   const item=menu?.querySelector('.menu-contents');if(item)item.hidden=fits;
  }
  queue();
 }
 function measure(){
 if(!frame){settled();return;}
  if(!flowing)return;
  const rect=(frame||view).getBoundingClientRect();
  restore=rect.top<0&&rect.height>0?-rect.top/rect.height:null;
  // The report measures itself at a small height. Its place keeps its size meanwhile, so nothing
  // below it moves unless the report really changed.
  if(frame.offsetHeight>200)view.style.minHeight=view.offsetHeight+'px';
  frame.style.height='200px';frame?.contentWindow?.postMessage({type:'cairn:measure'},'*');
 }
 frame?.addEventListener('load',measure);
 if(frame)cairnFollowScheme(frame);
 window.addEventListener('resize',()=>{if(innerWidth!==width){width=innerWidth;measure();place();}syncTitle();fitTitle();lastView='';queue();});
 phone.addEventListener?.('change',()=>{closeSheet(false);syncTitle();});

 // The overflow menu belongs to whichever More button opened it, in the toolbar or the bar.
 const keysOn=()=>root.dataset.shortcuts!=='off';
 if(menu){
  for(const item of menu.querySelectorAll('.menu-details,.menu-contents'))item.hidden=false;
  menu.querySelector('.menu-shortcuts').hidden=!fine.matches||!keysOn();
  for(const trigger of document.querySelectorAll('.more-button'))trigger.addEventListener('click',()=>{menuFrom=trigger.closest('.compact-reader')?'bar':'cover';menu.dataset.from=menuFrom;});
  const trigger=()=>document.querySelector(menuFrom==='bar'?'.compact-more':'.reader-cover .more-button');
  const placeMenu=()=>{
   const r=trigger().getBoundingClientRect(),w=menu.offsetWidth||240,h=menu.offsetHeight;
   menu.style.inset=Math.max(12,h?Math.min(r.bottom+6,innerHeight-h-12):r.bottom+6)+'px auto auto '+Math.max(12,Math.min(r.right-w,innerWidth-w-12))+'px';
  };
  menu.addEventListener('beforetoggle',event=>{if(event.newState==='open'){menuAt=scrollY;menu.dataset.from=menuFrom;placeMenu();}});
  menu.addEventListener('toggle',()=>{
   const open=menu.matches(':popover-open');
   for(const button of document.querySelectorAll('.more-button'))button.setAttribute('aria-expanded',String(open&&(button.closest('.compact-reader')?'bar':'cover')===menuFrom));
   if(open)placeMenu();else queue();
  });
  menu.querySelector('.menu-details').addEventListener('click',()=>{const from=trigger();menu.hidePopover();openSheet('details',from);});
  menu.querySelector('.menu-contents')?.addEventListener('click',()=>{const from=trigger();menu.hidePopover();openSheet('contents',from);});
  menu.querySelector('.menu-shortcuts').addEventListener('click',()=>{const from=trigger();menu.hidePopover();showShortcuts(from);});
 }
 // The shortcut list hands focus back to whatever opened it.
 const listing=()=>shortcuts.matches(':popover-open');
 function showShortcuts(from){returnTo=from||document.activeElement;shortcuts.showPopover();shortcuts.querySelector('.close').focus();}
 shortcuts.addEventListener('toggle',()=>{
  if(listing())return;
  if(returnTo?.isConnected&&!returnTo.closest('[inert]')&&(returnTo===frame||returnTo.offsetParent!==null))returnTo.focus({preventScroll:true});
  returnTo=null;
 });

 // Keys: C for the contents, [ and ] for the neighbouring pages in the library, ? for the list. They act
 // only while focus is in the reader itself, never on a key the report received, and not at all when
 // the owner turned them off in Settings. Focus a key moves shows its ring, as Tab's would.
 const keys=new Set(['c','C','[',']','?']);
 function shortcut(key){
  if(!keysOn())return false;
  root.dataset.inputModality='keyboard';
  if(key==='?'){listing()?shortcuts.hidePopover():showShortcuts(document.activeElement);return true;}
  if(listing())return false;
  if(key==='c'||key==='C'){
   if(!toc)return false;
   if(floating){(active||links[0]).focus({preventScroll:true});return true;}
   sheet==='contents'?closeSheet(true):openSheet('contents',phone.matches?title:contentsButton);
   return true;
  }
  const neighbour=document.querySelector(key==='['?'.end-page.is-previous':key===']'?'.end-page.is-next':null);
  if(neighbour){location.href=neighbour.href;return true;}
  return false;
 }
 document.addEventListener('keydown',event=>{
  if(event.defaultPrevented||event.metaKey||event.ctrlKey||event.altKey||event.isComposing||!keys.has(event.key))return;
  const target=event.target;
  if(target.isContentEditable||/^(INPUT|TEXTAREA|SELECT)$/.test(target.tagName)||target===frame)return;
  if(shortcut(event.key))event.preventDefault();
 });

 // An iframe never matches :focus while its document holds focus, so the reader marks it.
 const frameFocus=()=>setTimeout(()=>body.classList.toggle('frame-focused',document.activeElement===frame));
 addEventListener('blur',frameFocus);addEventListener('focus',frameFocus);document.addEventListener('focusin',frameFocus);

 syncTitle();measure();update();
})();

(() => {
 // Drop one-time notices from the address so a reload shows the plain page.
 const url=new URL(location.href);
 let changed=false;
 for(const name of ['kindle','saved'])if(url.searchParams.has(name)){url.searchParams.delete(name);changed=true;}
 if(changed)history.replaceState(null,'',url.pathname+url.search+url.hash);
})();
