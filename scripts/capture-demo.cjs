const {chromium}=require('playwright');
const {spawn}=require('node:child_process');
const fs=require('node:fs');const os=require('node:os');const path=require('node:path');
(async()=>{
 const dir=fs.mkdtempSync(path.join(os.tmpdir(),'cairn-demo-'));
 const token='fictional-demo-only-not-a-real-credential';
 const child=spawn(path.resolve('cairn'),['-data',dir,'-listen','127.0.0.1:0'],{env:{...process.env,CAIRN_PUBLISHER_TOKEN:token}});
 let browser;
 try{
  const address=await new Promise((resolve,reject)=>{
   const timer=setTimeout(()=>reject(Error('startup timeout')),10000);
   child.stderr.on('data',data=>{const match=data.toString().match(/private listener: (\S+)/);if(match){clearTimeout(timer);resolve(match[1]);}});
   child.on('error',reject);
  });
  const base='http://'+address;
  const examples=[
   {title:'A quieter desk, one change at a time',agent:'Research assistant',original_request:'Suggest practical changes to make a home workspace quieter.',markdown:'# A quieter desk\n\nStart with the sounds closest to you. A desk mat, a softer keyboard and a door seal can each address a different source of noise.\n\n## Try these first\n\n| Change | Helps with | Effort |\n|---|---|---|\n| Desk mat | Reflections and vibration | Five minutes |\n| Felt pads | Chair and desk movement | Ten minutes |\n| Door seal | Sound from the hallway | Half an hour |\n\n## Before you buy\n\nListen at different times of day. Note which sounds interrupt you, then change one thing and compare.'},
   {title:'Three ways to organise a reading list',agent:'Writing assistant',original_request:'Compare simple ways to keep articles and notes organised.',markdown:'# Keep a useful reading list\n\nA short queue is easier to finish. Separate things to read from references you want to keep.'},
   {title:'A weekend walk, with room to stop',agent:'Planning assistant',original_request:'Plan a relaxed fictional weekend itinerary with time for breaks.',markdown:'# A relaxed weekend\n\nLeave the morning open. Take a walk after lunch, then stop somewhere for tea.'}
  ];
  for(const entry of examples){const res=await fetch(base+'/api/pages',{method:'POST',headers:{authorization:'Bearer '+token,'content-type':'application/json'},body:JSON.stringify(entry)});if(res.status!==201)throw Error(await res.text());}
  browser=await chromium.launch({headless:true});
  const page=await browser.newPage({viewport:{width:1440,height:1000},deviceScaleFactor:1});
  await page.goto(base+'/');await page.evaluate(()=>document.fonts.ready);
  fs.mkdirSync('docs/images',{recursive:true});
  await page.screenshot({path:'docs/images/library.png',fullPage:true});
 }finally{
  if(browser)await browser.close();
  if(child.exitCode===null){child.kill('SIGTERM');await new Promise(resolve=>child.once('close',resolve));}
  fs.rmSync(dir,{recursive:true,force:true});
 }
})().catch(error=>{console.error(error);process.exitCode=1});
