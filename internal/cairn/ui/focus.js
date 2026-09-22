(() => {
 const root=document.documentElement;
 const pointer=()=>{root.dataset.inputModality='pointer';};
 const keyboard=event=>{
  if(['Tab','ArrowUp','ArrowDown','ArrowLeft','ArrowRight','Home','End','PageUp','PageDown'].includes(event.key))root.dataset.inputModality='keyboard';
 };
 root.dataset.inputModality='pointer';
 document.addEventListener('pointerdown',pointer,true);
 document.addEventListener('keydown',keyboard,true);
})();
