(() => {
 if (window.parent === window) return;
 let scheduled=false,last=0;
 function measure(){scheduled=false;const body=document.body;if(!body)return;const style=getComputedStyle(body);const height=Math.ceil(Math.max(document.documentElement.scrollHeight,body.scrollHeight+parseFloat(style.marginTop||0)+parseFloat(style.marginBottom||0)));if(height!==last){last=height;parent.postMessage({type:'cairn:report-height',height},'*');}}
 function queue(){if(!scheduled){scheduled=true;requestAnimationFrame(measure);}}
 function start(){new ResizeObserver(queue).observe(document.body);queue();document.fonts?.ready.then(queue);}
 if(document.readyState==='loading')document.addEventListener('DOMContentLoaded',start);else start();
 window.addEventListener('load',queue);window.addEventListener('resize',queue);
 window.addEventListener('message',event=>{if(event.source===parent&&event.data?.type==='cairn:measure'){last=0;queue();}});
})();
