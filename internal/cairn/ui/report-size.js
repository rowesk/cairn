(() => {
 const root=document.documentElement,framed=window.parent!==window;
 // Opened on its own, a report shows the opening title the reader's cover would otherwise show.
 if(!framed)root.dataset.standalone='';
 // Tables, code and diagrams Cairn drew scroll sideways. They take keyboard focus only while there is more to see, and
 // browsers without scroll-driven animation get the table edge fade from here.
 const timeline=window.CSS?.supports?.('animation-timeline','scroll()');
 function edges(el){const start=el.scrollLeft>1,end=el.scrollLeft+el.clientWidth<el.scrollWidth-1;el.parentElement.dataset.more=(start?'start ':'')+(end?'end':'');}
 function regions(){for(const el of document.querySelectorAll('[data-scroll]')){if(el.scrollWidth>el.clientWidth+1)el.tabIndex=0;else el.removeAttribute('tabindex');if(!timeline&&el.classList.contains('table-scroll'))edges(el);}}
 if(!timeline)document.addEventListener('scroll',event=>{if(event.target.classList?.contains('table-scroll'))edges(event.target);},true);
 // The header row of a long table stays in view while its rows scroll past. The reader says which part of the report
 // is visible below its bar; a report that scrolls itself uses its own scroll position.
 let view=null;
 function pin(){
  for(const table of document.querySelectorAll('.table-scroll>table')){
   const head=table.tHead,rows=table.tBodies[0]?.rows.length||0;
   let offset=0;
   if(view&&head&&rows>=8){const top=table.getBoundingClientRect().top+scrollY;offset=Math.round(Math.max(0,Math.min(view.top-top,table.offsetHeight-head.offsetHeight*3)));}
   if(offset>0){table.dataset.pinned='';table.style.setProperty('--pin',offset+'px');}
   else if('pinned' in table.dataset){delete table.dataset.pinned;table.style.removeProperty('--pin');}
  }
 }
 const selfScrolling=()=>root.scrollHeight>innerHeight+1;
 addEventListener('scroll',()=>{if(selfScrolling()){view={top:scrollY,height:innerHeight};pin();}},{passive:true});
 if(!framed){
  const ready=()=>{regions();new ResizeObserver(regions).observe(document.body);};
  if(document.readyState==='loading')document.addEventListener('DOMContentLoaded',ready);else ready();
  return;
 }
 let scheduled=false,last=0,lastMarks='',lastLayout='';
 // Quirks-mode body heights already include the margins; adding them again grows the frame forever.
 function measure(){scheduled=false;const body=document.body;if(!body)return;const style=getComputedStyle(body);const margins=document.compatMode==='BackCompat'?0:parseFloat(style.marginTop||0)+parseFloat(style.marginBottom||0);const height=Math.ceil(Math.max(root.scrollHeight,body.scrollHeight+margins));if(height!==last){last=height;parent.postMessage({type:'cairn:report-height',height},'*');}marks();layout();regions();pin();}
 // Heading positions let the reader mark the current section in its contents. Each heading also says
 // how much space sits above it (to the block before it, or its own top margin), because a section
 // starts where that space starts: once the last line before it has gone, the heading is what is read.
 function gapAbove(h,top){let prev=h.previousElementSibling;while(prev&&!prev.getClientRects().length)prev=prev.previousElementSibling;const gap=prev?top-prev.getBoundingClientRect().bottom:parseFloat(getComputedStyle(h).marginTop)||0;return Math.round(Math.min(96,Math.max(0,gap)));}
 function marks(){const items=[...document.querySelectorAll('h1[id],h2[id],h3[id],h4[id],h5[id],h6[id]')].slice(0,500).map(h=>{const top=h.getBoundingClientRect().top;return [h.id,Math.round(top+scrollY),gapAbove(h,top)];});const key=JSON.stringify(items);if(key!==lastMarks){lastMarks=key;parent.postMessage({type:'cairn:headings',items},'*');}}
 // Where the report's first block starts and which blocks a Cairn template lets run wider than the text
 // column, so the reader can keep its contents beside the text and out of their way. The reader takes
 // the column and the colours from its own grid and theme, never from here.
 function layout(){
  const visible=element=>element.getClientRects().length>0;
  let blocks=[...document.querySelectorAll('p')].filter(p=>visible(p)&&p.offsetWidth>200).slice(0,40);
  if(!blocks.length)blocks=[...document.querySelectorAll('h1,h2,h3,li')].filter(visible).slice(0,20);
  if(!blocks.length)return;
  const lefts=blocks.map(block=>block.getBoundingClientRect().left).sort((a,b)=>a-b),first=document.querySelector('main>:not(.title-echo),h1,h2,h3,p'),column=lefts[lefts.length>>1];
  // [top, bottom, left] of each block that reaches left of the text column.
  const wide=[...document.querySelectorAll('main[data-template]>*')].filter(visible).map(element=>{const r=element.getBoundingClientRect();return [Math.round(r.top+scrollY),Math.round(r.bottom+scrollY),Math.max(0,Math.round(r.left))];}).filter(block=>block[2]<column-1).slice(0,60);
  const report={wide,lead:first?Math.max(0,Math.round(first.getBoundingClientRect().top+scrollY)):0};
  const key=JSON.stringify(report);
  if(key!==lastLayout){lastLayout=key;parent.postMessage({type:'cairn:layout',...report},'*');}
 }
 function queue(){if(!scheduled){scheduled=true;requestAnimationFrame(measure);}}
 function start(){new ResizeObserver(queue).observe(document.body);queue();document.fonts?.ready.then(queue);}
 if(document.readyState==='loading')document.addEventListener('DOMContentLoaded',start);else start();
 window.addEventListener('load',queue);window.addEventListener('resize',queue);
 const find=id=>document.getElementById(id)||document.getElementsByName(id)[0];
 // A report at its full height cannot scroll itself, so it asks the reader to scroll instead.
 function go(target,hash){
  try{history.replaceState(null,'',hash);}catch{}
  if(selfScrolling())target.scrollIntoView();
  else parent.postMessage({type:'cairn:scroll-to',top:Math.max(0,target.getBoundingClientRect().top+scrollY)},'*');
 }
 const number=value=>Number.isFinite(value)&&value>=-200000&&value<=200000;
 window.addEventListener('message',event=>{
  if(event.source!==parent)return;
  const data=event.data;
  if(data?.type==='cairn:measure'){last=0;lastMarks='';lastLayout='';queue();}
  else if(data?.type==='cairn:go'&&typeof data.id==='string'){const target=find(data.id);if(target)go(target,'#'+encodeURIComponent(data.id));}
  // {type:'cairn:viewport',top,height}: top is how far the report's top sits above the visible area, in report pixels.
  else if(data?.type==='cairn:viewport'&&number(data.top)&&number(data.height)){view={top:data.top,height:data.height};pin();}
  // {type:'cairn:scheme',scheme:'dark'|'light'|'auto',palette:'violet'|'mono'|''} switches a Cairn template's
  // colours; pages in their own design ignore it.
  else if(data?.type==='cairn:scheme'&&['dark','light','auto'].includes(data.scheme)){
   if(data.scheme==='light')delete root.dataset.reportScheme;else root.dataset.reportScheme=data.scheme;
   // Violet tints a dark report (Futuristic); mono keeps a light one to ink and grey (Minimal).
   const palette=data.scheme==='dark'||data.scheme==='auto'?(data.palette==='violet'?'violet':''):(data.palette==='mono'?'mono':'');
   if(palette)root.dataset.reportPalette=palette;else delete root.dataset.reportPalette;
   lastLayout='';queue();
  }
  // {type:'cairn:keys',keys}: the keys a host wants back from inside the report (the library's drawer
  // asks for Escape, J and K). The reader asks for none, so its own keys never take one the report received.
  else if(data?.type==='cairn:keys'&&Array.isArray(data.keys))hostKeys=new Set(data.keys.filter(key=>['Escape','j','k'].includes(key)));
 });
 // The reader shows focus rings the way the person is working: by pointer or by keyboard.
 document.addEventListener('pointerdown',()=>parent.postMessage({type:'cairn:input',kind:'pointer'},'*'),true);
 document.addEventListener('keydown',event=>{if(event.key==='Tab')parent.postMessage({type:'cairn:input',kind:'keyboard'},'*');},true);
 // A key the host asked for goes back to it only after a real key press that the report itself did not
 // use: the report's own handlers have run and left it alone, and no field or editable text took it.
 let hostKeys=new Set();
 window.addEventListener('keydown',event=>{
  if(!hostKeys.size||!event.isTrusted||event.metaKey||event.ctrlKey||event.altKey||event.isComposing)return;
  const key=event.key.length===1?event.key.toLowerCase():event.key,target=event.target;
  if(!hostKeys.has(key)||target.isContentEditable||/^(INPUT|TEXTAREA|SELECT)$/.test(target.tagName||'')||target.closest?.('[role=application],[role=textbox],[role=slider],[role=spinbutton],[role=combobox],[role=listbox],[role=menu],[role=grid]'))return;
  setTimeout(()=>{if(!event.defaultPrevented)parent.postMessage({type:'cairn:key',key},'*');});
 });
 document.addEventListener('click',event=>{
  if(event.defaultPrevented||event.button!==0||event.metaKey||event.ctrlKey||event.shiftKey||event.altKey)return;
  const link=event.target.closest?.('a[href]'),href=link?.getAttribute('href');
  if(!href||href[0]!=='#'||href.length<2||selfScrolling())return;
  let id=href.slice(1);try{id=decodeURIComponent(id);}catch{}
  const target=find(id);
  if(!target)return;
  event.preventDefault();
  go(target,href);
 });
})();
