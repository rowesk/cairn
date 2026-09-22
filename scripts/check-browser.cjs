const {chromium}=require('playwright');
const {spawn}=require('node:child_process');
const fs=require('node:fs');const os=require('node:os');const path=require('node:path');
(async()=>{
 const dir=fs.mkdtempSync(path.join(os.tmpdir(),'cairn-browser-'));
 const child=spawn(process.env.CAIRN_BINARY || path.resolve('cairn'),['-data',dir,'-listen','127.0.0.1:0','-public-listen','127.0.0.1:0','-public-base-url','https://reports.example.com'],{env:{...process.env,CAIRN_PUBLISHER_TOKEN:'browser-smoke-token-at-least-32-characters'}});
 let browser;let publicAddress;
 try{
  const address=await new Promise((resolve,reject)=>{
   let output='';
   const finish=(error,address)=>{
    clearTimeout(timer);child.stderr.off('data',onData);child.off('error',onError);child.off('exit',onExit);
    error?reject(error):resolve(address);
   };
   const onData=data=>{output+=data.toString();const publicMatch=output.match(/public listener: (\S+)/);if(publicMatch)publicAddress=publicMatch[1];const match=output.match(/private listener: (\S+)/);if(match)finish(null,match[1]);};
   const onError=error=>finish(error);
   const onExit=code=>finish(Error('Cairn exited before startup: '+code+' '+output));
   const timer=setTimeout(()=>finish(Error('Cairn startup timed out: '+output)),10000);
   child.stderr.on('data',onData);child.once('error',onError);child.once('exit',onExit);
  });
  const base='http://'+address;
  await fetch(base+'/api/pages',{method:'POST',headers:{'content-type':'application/json',authorization:'Bearer browser-smoke-token-at-least-32-characters'},body:JSON.stringify({title:'Other page',agent:'Test',original_request:'Other private request',slug:'other',html:'<p>Other page</p>',assets:{'secret.png':'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII='}})});
  const response=await fetch(base+'/api/pages',{method:'POST',headers:{'content-type':'application/json',authorization:'Bearer browser-smoke-token-at-least-32-characters'},body:JSON.stringify({title:'Browser report',agent:'ExampleAgent',original_request:'PRIVATE-BROWSER-MARKER',slug:'browser',html:'<h1>Sandboxed report</h1><img src="?asset=bulb.png"><img id="cross-page" src="/_assets/other/secret.png"><script>document.body.dataset.executed="yes"</script>',assets:{'bulb.png':'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII='}})});
  if(response.status!==201)throw Error(await response.text());
  browser=await chromium.launch({headless:true});
  const page=await browser.newPage({viewport:{width:390,height:844}});
  page.setDefaultTimeout(10000);
  await page.goto(base+'/browser');
  await page.frameLocator('iframe').locator('h1').waitFor();
  await page.getByRole('group',{name:'Prepared by ExampleAgent',exact:true}).waitFor();
  const frame=page.frames().find(f=>f!==page.mainFrame());
  const checks=await frame.evaluate(()=>({crossPage:document.querySelector("#cross-page").naturalWidth,scriptRan:document.body.dataset.executed,leaked:document.body.textContent.includes('PRIVATE-BROWSER-MARKER'),image:document.querySelector('img').naturalWidth,isolated:(()=>{try{return !parent.document.body}catch{return true}})()}));
  if(checks.crossPage || checks.scriptRan!=='yes' || checks.leaked || !checks.image || !checks.isolated)throw Error(JSON.stringify(checks));
  await page.goto(base+'/_content/browser');
  if(await page.locator('body').getAttribute('data-executed')!=='yes')throw Error('direct content script did not run');
  if((await page.content()).includes('PRIVATE-BROWSER-MARKER'))throw Error('direct content leaked request');
  const interactive=await fetch(base+'/api/pages',{method:'POST',headers:{'content-type':'application/json',authorization:'Bearer browser-smoke-token-at-least-32-characters'},body:JSON.stringify({title:'Interactive report',agent:'ExampleAgent',original_request:'PRIVATE-INTERACTIVE',slug:'interactive',markdown:'# Interactive report\n\n```mermaid\nflowchart LR\n  Chat --> Page\n```\n\n```echarts\n{"xAxis":{"type":"category","data":["A","B"]},"yAxis":{},"series":[{"type":"bar","data":[2,5]}]}\n```'})});
  if(interactive.status!==201)throw Error(await interactive.text());
  await page.goto(base+'/interactive');const interactiveFrame=page.frames().find(f=>f!==page.mainFrame());
  await interactiveFrame.locator('.mermaid svg').waitFor();await interactiveFrame.locator('canvas').waitFor();
  const denied=await interactiveFrame.evaluate(async()=>{try{await fetch('/api/pages',{method:'POST',body:'{}'});return false}catch{return true}});
  if(!denied)throw Error('report network write was not blocked');
  for(const layout of ['report','comparison','visual']){
   const result=await fetch(base+'/api/pages',{method:'POST',headers:{'content-type':'application/json',authorization:'Bearer browser-smoke-token-at-least-32-characters'},body:JSON.stringify({title:'Bulb comparison',agent:'ExampleAgent',original_request:'PRIVATE-TEMPLATE-MARKER',slug:layout,template:layout,markdown:'# Bulb comparison\n\nA readable comparison of lighting choices for the house.\n\n## Options\n\n| Name | Power | Brightness | Colour | Fitting | Warranty |\n|---|---|---|---|---|---|\n| Reading lamp | 6 W | 800 lm | Warm white | E27 | Two years |\n\n![Bulb](?asset=bulb.png)',assets:{'bulb.png':'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII='}})});
   if(result.status!==201)throw Error(await result.text());
   for(const width of [390,1280]){
    await page.setViewportSize({width,height:900});await page.goto(base+'/'+layout);
    const report=page.frames().find(f=>f!==page.mainFrame());await report.locator('main').waitFor();
    const metrics=await report.evaluate(()=>({wide:document.documentElement.scrollWidth>innerWidth+1,table:document.querySelector('.table-scroll').scrollWidth>document.querySelector('.table-scroll').clientWidth,layout:document.querySelector('main').dataset.template}));
    if(metrics.wide || metrics.layout!==layout || (width===390 && !metrics.table))throw Error(JSON.stringify(metrics));
   }
  }
  await page.goto(base+'/import');
  await page.locator('[name=document]').setInputFiles({name:'notes.md',mimeType:'text/markdown',buffer:Buffer.from('# Imported from a file\n\nA private report.')});
  await page.locator('[name=title]').fill('Imported notes');await page.locator('details summary').click();await page.locator('[name=agent]').fill('the owner');await page.locator('[name=original_request]').fill('Private import request');await page.locator('[name=slug]').fill('import-check');
  await page.getByRole('button',{name:'Save private page'}).click();await page.waitForURL('**/import-check',{timeout:5000}).catch(async()=>{throw Error('Import failed at '+page.url()+': '+await page.locator('body').innerText())});
  await page.frameLocator('iframe').getByRole('heading',{name:'Imported from a file'}).waitFor();
  await page.goto(base+'/import');
  await page.locator('[name=document]').setInputFiles({name:'custom.html',mimeType:'text/html',buffer:Buffer.from('<h1>Custom HTML import</h1>')});
  await page.locator('[name=title]').fill('Custom HTML');await page.locator('details summary').click();await page.locator('[name=agent]').fill('the owner');await page.locator('[name=original_request]').fill('Private HTML request');await page.locator('[name=slug]').fill('html-import');
  await page.getByRole('button',{name:'Save private page'}).click();await page.waitForURL('**/html-import');await page.frameLocator('iframe').getByRole('heading',{name:'Custom HTML import'}).waitFor();
  await page.goto(base+'/');await page.getByRole('searchbox').fill('Private HTML request');await page.getByRole('button',{name:'Search',exact:true}).click();
  await page.getByRole('heading',{name:'Custom HTML',exact:true}).waitFor();await page.getByRole('link',{name:'Manage Custom HTML',exact:true}).click();
  await page.getByLabel('Title',{exact:true}).fill('Renamed import');await page.getByRole('button',{name:'Rename',exact:true}).click();
  await page.goto(base+'/manage/html-import');await page.getByRole('button',{name:'Archive page',exact:true}).click();
  if(await page.getByRole('heading',{name:'Renamed import',exact:true}).count())throw Error('archived page visible');
  await page.getByRole('link',{name:/^Archive \d+$/}).click();await page.getByRole('heading',{name:'Renamed import',exact:true}).waitFor();
  await page.goto(base+'/manage/html-import');await page.getByRole('button',{name:'Restore to library',exact:true}).click();
  await page.goto(base+'/manage/html-import');await page.getByRole('link',{name:'Delete page',exact:true}).click();await page.getByRole('button',{name:'Delete permanently',exact:true}).click();
  if((await fetch(base+'/html-import')).status!==404)throw Error('deleted page readable');
  await page.goto(base+'/manage/report');if(await page.getByLabel('Public URL name').inputValue()!=='report')throw Error('sharing changed private path');await page.getByRole('button',{name:'Enable public link',exact:true}).click();
  const publicBase='http://'+publicAddress;
  await page.goto(publicBase+'/report');await page.frameLocator('iframe').getByRole('heading',{name:'Bulb comparison',exact:true}).waitFor();
  if((await page.content()).includes('PRIVATE-TEMPLATE-MARKER'))throw Error('public wrapper leaked context');
  if((await fetch(publicBase+'/')).status!==404 || (await fetch(publicBase+'/manage/report')).status!==404)throw Error('public management route exposed');
  await page.goto(base+'/manage/report');await page.getByLabel('Share password').fill('browser-secret');await page.getByRole('button',{name:'Set password',exact:true}).click();
  await page.goto(publicBase+'/report');await page.getByLabel('Password',{exact:true}).fill('browser-secret');await page.getByRole('button',{name:'Unlock report',exact:true}).click();
  await page.frameLocator('iframe').getByRole('heading',{name:'Bulb comparison',exact:true}).waitFor();
  const unlocked=page.frames().find(f=>f!==page.mainFrame());
  await unlocked.waitForFunction(()=>document.querySelector('img')?.naturalWidth>0);
  await page.goto(base+'/manage/report');await page.getByLabel('Share password').fill('changed-secret');await page.getByRole('button',{name:'Change password',exact:true}).click();
  await page.goto(publicBase+'/report');await page.getByRole('button',{name:'Unlock report',exact:true}).waitFor();
  await page.goto(base+'/manage/report');await page.getByRole('button',{name:'Turn sharing off',exact:true}).click();
  if((await fetch(publicBase+'/report')).status!==404)throw Error('revoked share readable');
  if((await fetch(base+'/report')).status!==200)throw Error('private route lost after revocation');
  // Exercise the newer editor and settings flows with real browser events.
  await page.goto(base+'/new');
  await page.getByLabel('Title',{exact:true}).fill('Editor smoke');
  await page.getByLabel('Markdown',{exact:true}).fill('# Editor smoke\n\nSaved from the editor.');
  await page.getByRole('button',{name:'Preview',exact:true}).click();
  await page.frameLocator('#editor-preview').getByRole('heading',{name:'Editor smoke'}).waitFor();
  await page.getByRole('button',{name:'Back to writing'}).click();
  await page.getByRole('button',{name:'Page access and address'}).click();
  await page.getByRole('radio',{name:/Anyone with the link/}).check();
  await page.getByRole('button',{name:'Close page access'}).click();
  await page.getByRole('button',{name:/Save & share/}).click();
  await page.waitForURL(url=>/^\/[A-Za-z0-9]{3}$/.test(url.pathname));
  const editorPath=new URL(page.url()).pathname;
  if((await fetch(publicBase+editorPath)).status!==200)throw Error('editor did not share at its private path');
  if(!(await fetch(base+editorPath+'?download=1')).ok)throw Error('editor source missing');
  await page.goto(base+'/manage'+editorPath);await page.getByRole('button',{name:'Turn sharing off',exact:true}).click();
  if((await fetch(publicBase+editorPath)).status!==404 || (await fetch(base+editorPath)).status!==200)throw Error('editor revocation lost private access');
  await page.goto(base+'/settings');
  if(await page.locator('[name=link_length]').inputValue()!=='3')throw Error('default link length changed');
  await page.locator('#new-person-name').fill('Browser agent');await page.locator('#new-person-key').click();
  await page.getByRole('button',{name:'Create key',exact:true}).click();
  await page.waitForFunction(()=>document.querySelector('#key-secret').value.startsWith('cairn_'));
  const key=await page.locator('#key-secret').inputValue();
  const snippet=await page.locator('#key-snippet').inputValue();
  if(!snippet.includes('238,328') || snippet.includes('{{OWNER}}') || !snippet.includes("Owner's private report library"))throw Error('agent setup has stale link policy or owner');
  const keyed=await fetch(base+'/api/pages',{method:'POST',headers:{'content-type':'application/json',authorization:'Bearer '+key},body:JSON.stringify({title:'Keyed report',original_request:'Private keyed request',markdown:'Hello from a key'})});
  if(keyed.status!==201)throw Error('new agent key failed');
  await Promise.all([page.waitForNavigation({waitUntil:'load'}),page.locator('#key-close').click()]);
  await page.waitForFunction(()=>document.querySelector('#key-secret')?.value==='');
  await page.locator('[data-key-name="Browser agent"]').click();
  await page.locator('#key-revoke').click();await page.locator('#key-revoke').click();
  await page.waitForFunction(()=>document.querySelector('#key-primary').textContent==='Create key');
  if((await fetch(base+'/api/pages',{method:'POST',headers:{'content-type':'application/json',authorization:'Bearer '+key},body:'{}'})).status!==401)throw Error('revoked key accepted');
  await Promise.all([page.waitForNavigation({waitUntil:'load'}),page.locator('#key-close').click()]);
  for(const width of [320,390,1440]){
   await page.setViewportSize({width,height:900});
   for(const route of ['/','/settings','/new','/report']){
    await page.goto(base+route);
    if(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth+1))throw Error('horizontal overflow at '+width+' '+route);
   }
  }
  await page.setViewportSize({width:1280,height:900});await page.goto(base+'/report');
  if(process.env.CAIRN_SCREENSHOT)await page.screenshot({path:process.env.CAIRN_SCREENSHOT,fullPage:true});
  console.log('Headless Chromium passed: responsive readers, sandbox isolation, images, charts, imports, library lifecycle, matching share paths, passwords, revocation, editor preview/save, agent keys and settings.');
 }finally{
  if(browser)await browser.close();
  if(child.pid && child.exitCode===null && child.signalCode===null){
   await new Promise(resolve=>{
    const done=()=>{clearTimeout(timer);resolve()};
    const timer=setTimeout(()=>{child.kill('SIGKILL');resolve()},15000);
    child.once('close',done);child.once('error',done);child.kill('SIGTERM');
   });
  }
  fs.rmSync(dir,{recursive:true,force:true});
 }
})().catch(e=>{console.error(e);process.exitCode=1});
