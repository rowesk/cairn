(() => {
 if (window.parent === window) return;
 let scheduled=false,last=0,lastMarks='',lastLayout='';
 // Quirks-mode body heights already include the margins; adding them again grows the frame forever.
 function measure(){scheduled=false;const body=document.body;if(!body)return;const style=getComputedStyle(body);const margins=document.compatMode==='BackCompat'?0:parseFloat(style.marginTop||0)+parseFloat(style.marginBottom||0);const height=Math.ceil(Math.max(document.documentElement.scrollHeight,body.scrollHeight+margins));if(height!==last){last=height;parent.postMessage({type:'cairn:report-height',height},'*');}marks();layout();}
 // Heading positions let the reader mark the current section in its contents.
 function marks(){const items=[...document.querySelectorAll('h1[id],h2[id],h3[id],h4[id],h5[id],h6[id]')].slice(0,500).map(h=>[h.id,Math.round(h.getBoundingClientRect().top+scrollY)]);const key=JSON.stringify(items);if(key!==lastMarks){lastMarks=key;parent.postMessage({type:'cairn:headings',items},'*');}}
 // Where the text column starts and how the report looks, so the reader can set its contents in the left margin.
 function layout(){
  const visible=element=>element.getClientRects().length>0;
  let blocks=[...document.querySelectorAll('p')].filter(p=>visible(p)&&p.offsetWidth>200).slice(0,40);
  if(!blocks.length)blocks=[...document.querySelectorAll('h1,h2,h3,li')].filter(visible).slice(0,20);
  if(!blocks.length)return;
  const lefts=blocks.map(block=>block.getBoundingClientRect().left).sort((a,b)=>a-b),first=document.querySelector('h1,h2,h3,p');
  const page=getComputedStyle(document.body),root=getComputedStyle(document.documentElement),heading=document.querySelector('h2,h3,h1'),link=document.querySelector('a[href]');
  const clear=colour=>colour==='transparent'||/rgba\(.*,\s*0\)$/.test(colour);
  const report={left:Math.round(lefts[lefts.length>>1]),lead:first?Math.max(0,Math.round(first.getBoundingClientRect().top+scrollY)):0,ink:page.color,paper:clear(page.backgroundColor)?root.backgroundColor:page.backgroundColor,accent:link?getComputedStyle(link).color:page.color,font:(heading?getComputedStyle(heading):page).fontFamily};
  const key=JSON.stringify(report);
  if(key!==lastLayout){lastLayout=key;parent.postMessage({type:'cairn:layout',...report},'*');}
 }
 function queue(){if(!scheduled){scheduled=true;requestAnimationFrame(measure);}}
 function start(){new ResizeObserver(queue).observe(document.body);queue();document.fonts?.ready.then(queue);}
 if(document.readyState==='loading')document.addEventListener('DOMContentLoaded',start);else start();
 window.addEventListener('load',queue);window.addEventListener('resize',queue);
 const selfScrolling=()=>document.documentElement.scrollHeight>innerHeight+1;
 const find=id=>document.getElementById(id)||document.getElementsByName(id)[0];
 // A report at its full height cannot scroll itself, so it asks the reader to scroll instead.
 function go(target,hash){
  try{history.replaceState(null,'',hash);}catch{}
  if(selfScrolling())target.scrollIntoView();
  else parent.postMessage({type:'cairn:scroll-to',top:Math.max(0,target.getBoundingClientRect().top+scrollY)},'*');
 }
 window.addEventListener('message',event=>{
  if(event.source!==parent)return;
  if(event.data?.type==='cairn:measure'){last=0;lastMarks='';lastLayout='';queue();}
  else if(event.data?.type==='cairn:go'&&typeof event.data.id==='string'){const target=find(event.data.id);if(target)go(target,'#'+encodeURIComponent(event.data.id));}
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
