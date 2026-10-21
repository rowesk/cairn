(() => {
 if (window.parent === window) return;
 let scheduled=false,last=0,lastMarks='';
 // Quirks-mode body heights already include the margins; adding them again grows the frame forever.
 function measure(){scheduled=false;const body=document.body;if(!body)return;const style=getComputedStyle(body);const margins=document.compatMode==='BackCompat'?0:parseFloat(style.marginTop||0)+parseFloat(style.marginBottom||0);const height=Math.ceil(Math.max(document.documentElement.scrollHeight,body.scrollHeight+margins));if(height!==last){last=height;parent.postMessage({type:'cairn:report-height',height},'*');}marks();}
 // Heading positions let the reader mark the current section in its contents.
 function marks(){const items=[...document.querySelectorAll('h1[id],h2[id],h3[id],h4[id],h5[id],h6[id]')].slice(0,500).map(h=>[h.id,Math.round(h.getBoundingClientRect().top+scrollY)]);const key=JSON.stringify(items);if(key!==lastMarks){lastMarks=key;parent.postMessage({type:'cairn:headings',items},'*');}}
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
  if(event.data?.type==='cairn:measure'){last=0;lastMarks='';queue();}
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
