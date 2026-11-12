const {chromium}=require('playwright');
const {spawn}=require('node:child_process');
const fs=require('node:fs');const os=require('node:os');const path=require('node:path');const net=require('node:net');
(async()=>{
 // A loopback relay stands in for the owner's mail account so Send to Kindle runs end to end.
 // relayMode makes it refuse the sign-in ('auth') or the message for now ('busy').
 const mailbox=[];let relayMode='ok';
 const relay=net.createServer(socket=>{
  let buffer='',data=null;const say=line=>socket.write(line+'\r\n');say('220 relay');
  socket.on('data',chunk=>{
   buffer+=chunk.toString('latin1');let end;
   while((end=buffer.indexOf('\r\n'))!==-1){
    const line=buffer.slice(0,end);buffer=buffer.slice(end+2);
    if(data!==null){if(line==='.'){mailbox.push(data);data=null;say('250 queued');}else data+=line+'\n';continue;}
    const verb=line.split(' ')[0].toUpperCase();
    if(verb==='EHLO')say('250-relay\r\n250 AUTH PLAIN');else if(verb==='AUTH')say(relayMode==='auth'?'535 5.7.8 bad credentials':'235 ok');else if(verb==='RCPT')say(relayMode==='busy'?'451 4.3.0 try later':'250 ok');else if(verb==='DATA'){data='';say('354 go');}else if(verb==='QUIT'){say('221 bye');socket.end();}else say('250 ok');
   }
  });
 });
 await new Promise(resolve=>relay.listen(0,'127.0.0.1',resolve));
 const dir=fs.mkdtempSync(path.join(os.tmpdir(),'cairn-browser-'));
 const child=spawn(process.env.CAIRN_BINARY || path.resolve('cairn'),['-data',dir,'-listen','127.0.0.1:0','-public-listen','127.0.0.1:0','-public-base-url','https://reports.example.com'],{env:{...process.env,CAIRN_PUBLISHER_TOKEN:'browser-smoke-token-at-least-32-characters',CAIRN_MAIL_FROM:'owner@example.com',CAIRN_SMTP_SERVER:'127.0.0.1:'+relay.address().port,CAIRN_SMTP_PASSWORD:'browser-relay-password'}});
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
  browser=await chromium.launch({headless:true,executablePath:process.env.CAIRN_CHROMIUM_EXECUTABLE || undefined});
  {
   // First run: an empty library offers three real ways in, and the agent one opens Settings at its section.
   const first=await browser.newPage({viewport:{width:1280,height:900}});
   await first.goto(base+'/');
   for(const name of [/^Connect an agent/,/^Write a page/,/^Import a file/])await first.getByRole('link',{name}).waitFor();
   if(await first.locator('#search').count()||await first.getByText('Return to your library').count())throw Error('first run shows an empty search or a dead end');
   await first.getByRole('link',{name:/^Connect an agent/}).click();await first.waitForURL('**/settings#agents');
   if(!await first.locator('#agents').isVisible())throw Error('settings has no agents anchor');
   await first.close();
  }
  await fetch(base+'/api/pages',{method:'POST',headers:{'content-type':'application/json',authorization:'Bearer browser-smoke-token-at-least-32-characters'},body:JSON.stringify({title:'Other page',agent:'Test',original_request:'Other private request',slug:'other',html:'<p>Other page</p>',assets:{'secret.png':'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII='}})});
  const response=await fetch(base+'/api/pages',{method:'POST',headers:{'content-type':'application/json',authorization:'Bearer browser-smoke-token-at-least-32-characters'},body:JSON.stringify({title:'Browser report',agent:'ExampleAgent',original_request:'PRIVATE-BROWSER-MARKER',slug:'browser',html:'<h1>Sandboxed report</h1><img src="?asset=bulb.png"><img id="cross-page" src="/_assets/other/secret.png"><script>document.body.dataset.executed="yes"</script>',assets:{'bulb.png':'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII='}})});
  if(response.status!==201)throw Error(await response.text());
  const page=await browser.newPage({viewport:{width:390,height:844}});
  page.setDefaultTimeout(10000);
  await page.goto(base+'/browser');
  await page.frameLocator('iframe').locator('h1').waitFor();
  await page.getByText('Prepared by ExampleAgent').first().waitFor();
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
  // Markdown reports on a phone: footnotes, scroll regions, diagrams and charts in the report's type, no text under 12px.
  const fence='```',bulbRows=Array.from({length:10},(_,i)=>`| Bulb ${i+1} | E27 | Warm white | Yes | 25,000 h | Two years | Needs the maker's app and a hub to dim |`);
  const richMarkdown=['# Rich report','','Opening line with a note.[^1]','','## Prices','','| Bulb | Power | Price |','|---|---:|---:|','| Warm LED | 6 W | £4.50 |','','## Every bulb','','| Bulb | Fitting | Colour | Dimmable | Lifetime | Warranty | Notes |','|---|---|---|---|---|---|---|',...bulbRows,'','## The order to do it in','',fence+'mermaid','flowchart LR','  A[Measure the fitting] --> B[Choose the colour] --> C[Check the dimmer] --> D[Replace the dimmer] --> E[Fit every bulb]',fence,'','## Cost over ten years','',fence+'echarts',JSON.stringify({xAxis:{type:'category',data:['Warm LED','Halogen','Smart LED','Filament LED','Compact fluorescent']},yAxis:{type:'value',axisLabel:{formatter:'{value} yrs',fontSize:9}},grid:{left:150,right:30},series:[{type:'bar',data:[3,33,5,4,26]}]}),fence,'','## A broken drawing','',fence+'mermaid','this is not a diagram',fence,'',fence+'sh','curl -s https://example.com/a/very/long/path/that/keeps/going/past/the/edge --header "Accept: application/json"',fence,'','Water is H<sub>2</sub>O.<script>document.body.dataset.ran="yes"</script>','','[^1]: The note at the end.'].join('\n');
  const rich=await fetch(base+'/api/pages',{method:'POST',headers:{'content-type':'application/json',authorization:'Bearer browser-smoke-token-at-least-32-characters'},body:JSON.stringify({title:'Rich report',agent:'ExampleAgent',original_request:'PRIVATE-RICH',slug:'rich',markdown:richMarkdown})});
  if(rich.status!==201)throw Error(await rich.text());
  const badChart=await fetch(base+'/api/pages',{method:'POST',headers:{'content-type':'application/json',authorization:'Bearer browser-smoke-token-at-least-32-characters'},body:JSON.stringify({title:'Bad chart',agent:'ExampleAgent',original_request:'PRIVATE-BAD',markdown:fence+'echarts\n{"series": [1,]}\n'+fence})});
  if(badChart.status!==400||!(await badChart.text()).includes('echarts block 1 on line 2 is not valid chart JSON'))throw Error('invalid chart JSON was not refused');
  await page.setViewportSize({width:390,height:844});await page.goto(base+'/rich');
  const richFrame=page.frames().find(f=>f!==page.mainFrame());
  // Diagrams draw as the reader comes near them, so read down the page first.
  await page.waitForTimeout(400);for(let y=0,end=await page.evaluate(()=>document.documentElement.scrollHeight);y<end;y+=400){await page.evaluate(y=>scrollTo(0,y),y);await page.waitForTimeout(60);}
  await richFrame.locator('.diagram svg').waitFor();await richFrame.locator('.chart-canvas canvas').waitFor();await richFrame.locator('.diagram-failed').waitFor();await page.waitForTimeout(600);
  const richChecks=await richFrame.evaluate(()=>{
   let smallest=Infinity,where='';
   for(const el of document.querySelectorAll('main *')){
    const box=el.getBoundingClientRect();
    if(!box.width||!box.height||el.closest('.title-echo,.sr-only')||![...el.childNodes].some(n=>n.nodeType===3&&n.data.trim()))continue;
    let size=parseFloat(getComputedStyle(el).fontSize);const svg=el.closest('svg');
    if(svg?.viewBox?.baseVal?.width)size*=svg.getBoundingClientRect().width/svg.viewBox.baseVal.width;
    if(size<smallest){smallest=size;where=el.tagName+' '+el.textContent.trim().slice(0,20);}
   }
   const option=echarts.getInstanceByDom(document.querySelector('.chart-canvas')).getOption();let chartFont=Infinity;
   const walk=v=>{if(v&&typeof v==='object')for(const k in v){if(k==='fontSize'&&typeof v[k]==='number')chartFont=Math.min(chartFont,v[k]);else walk(v[k]);}};walk(option);
   const [narrow,wide]=document.querySelectorAll('.table-scroll'),diagram=document.querySelector('.diagram:not(.diagram-failed)'),label=diagram.querySelector('.nodeLabel, text');
   return {smallest,where,chartFont,chartGrid:option.grid[0].left,chartFamily:getComputedStyle(document.documentElement).getPropertyValue('--report-sans'),chartName:document.querySelector('.chart-canvas').getAttribute('aria-label'),
    diagramFamily:getComputedStyle(label).fontFamily,overflow:document.documentElement.scrollWidth>innerWidth+1,
    narrow:[narrow.scrollWidth>narrow.clientWidth+1,narrow.hasAttribute('tabindex')],wide:[wide.scrollWidth>wide.clientWidth+1,wide.tabIndex,wide.getAttribute('aria-label')],
    diagram:[diagram.hasAttribute('data-overflow'),diagram.querySelector('.diagram-hint').offsetHeight>0,diagram.querySelector('[data-scroll]').tabIndex],
    failed:document.querySelector('.diagram-failed pre').textContent,echo:document.querySelector('.title-echo').getBoundingClientRect().height,ran:document.body.dataset.ran||'',note:document.querySelector('.footnote-ref').getAttribute('href')};
  });
  if(richChecks.smallest<11.9||richChecks.chartFont<12||richChecks.chartGrid>60||!richChecks.chartFamily.includes('system-ui')||!richChecks.diagramFamily.includes('system-ui')||richChecks.chartName!=='Cost over ten years'||richChecks.overflow||richChecks.narrow.join()!=='false,false'||richChecks.wide.join()!=='true,0,Every bulb, table'||richChecks.diagram.join()!=='true,true,0'||!richChecks.failed.includes('this is not a diagram')||richChecks.echo>1||richChecks.ran||richChecks.note!=='#fn-1')throw Error('phone report: '+JSON.stringify(richChecks));
  // The reader tells the report which part is visible, and a long table keeps its header row in view.
  const tableTop=await richFrame.evaluate(()=>Math.round(document.querySelectorAll('.table-scroll table')[1].getBoundingClientRect().top+scrollY));
  await page.evaluate(top=>document.querySelector('.report-frame').contentWindow.postMessage({type:'cairn:viewport',top,height:700},'*'),tableTop+160);
  await richFrame.waitForFunction(()=>document.querySelectorAll('.table-scroll table')[1].style.getPropertyValue('--pin')==='160px');
  await page.evaluate(()=>document.querySelector('.report-frame').contentWindow.postMessage({type:'cairn:viewport',top:0,height:700},'*'));
  await richFrame.waitForFunction(()=>!document.querySelector('table[data-pinned]'));
  // Footnote links scroll the reader, not the frame.
  await page.frameLocator('.report-frame').locator('.footnote-ref').click();await page.waitForFunction(()=>scrollY>300);await page.waitForTimeout(900);
  const noteAt=await page.evaluate(()=>document.querySelector('.report-frame').getBoundingClientRect().top)+await richFrame.evaluate(()=>document.getElementById('fn-1').getBoundingClientRect().top);
  if(noteAt<30||noteAt>844-40)throw Error('footnote link landed at '+noteAt);
  // The dark scheme redraws diagrams and charts without growing the frame.
  const lightHeight=await page.evaluate(()=>document.querySelector('.report-frame').offsetHeight);
  // The reader sends the theme's scheme when the frame loads; post after that, as a device switching would.
  await richFrame.waitForLoadState('load');await page.waitForTimeout(300);
  await page.evaluate(()=>document.querySelector('.report-frame').contentWindow.postMessage({type:'cairn:scheme',scheme:'dark'},'*'));
  await richFrame.waitForFunction(()=>document.documentElement.dataset.reportScheme==='dark'&&getComputedStyle(document.body).backgroundColor==='rgb(17, 27, 34)');await page.waitForTimeout(900);
  const dark=await richFrame.evaluate(()=>({diagrams:document.querySelectorAll('.diagram svg').length,stroke:getComputedStyle(document.querySelector('.diagram .node rect, .diagram .node polygon')).stroke,charts:document.querySelectorAll('.chart-canvas canvas').length}));
  if(dark.diagrams!==1||dark.charts<1||dark.stroke!=='rgb(157, 177, 190)'||await page.evaluate(()=>document.querySelector('.report-frame').offsetHeight)!==lightHeight)throw Error('dark scheme: '+JSON.stringify(dark));
  // Custom HTML and pages saved before the current templates have no report stylesheet; the runtime still keeps them inside the screen.
  const custom=await fetch(base+'/api/pages',{method:'POST',headers:{'content-type':'application/json',authorization:'Bearer browser-smoke-token-at-least-32-characters'},body:JSON.stringify({title:'Custom drawing',agent:'ExampleAgent',original_request:'PRIVATE-CUSTOM',slug:'custom-drawing',capabilities:['mermaid','echarts'],html:'<h1>Custom</h1><pre><code class="language-mermaid">flowchart LR\n  A[Measure the fitting] --> B[Choose the colour] --> C[Check the dimmer] --> D[Fit every bulb]</code></pre><pre><code class="language-echarts">{"xAxis":{"type":"category","data":["A","B"]},"yAxis":{},"series":[{"type":"bar","data":[2,5]}]}</code></pre>'})});
  if(custom.status!==201)throw Error(await custom.text());
  await page.goto(base+'/custom-drawing');const customFrame=page.frames().find(f=>f!==page.mainFrame());
  await customFrame.locator('.diagram svg').waitFor();await customFrame.locator('canvas').waitFor();
  const customChecks=await customFrame.evaluate(()=>({wide:document.documentElement.scrollWidth>innerWidth+1,colours:echarts.getInstanceByDom(document.querySelector('.chart-canvas')).getOption().color?.length||0}));
  if(customChecks.wide||!customChecks.colours)throw Error('custom HTML drawing: '+JSON.stringify(customChecks));
  // Opened on its own, the report shows its opening title again.
  await page.goto(base+'/_content/rich');
  if(await page.locator('.title-echo').evaluate(h=>h.getBoundingClientRect().height)<20)throw Error('standalone report hid its title');
  let sections='<style>body{max-width:40rem;margin:2rem auto;font:18px/1.6 Georgia,serif}</style><p><a id="jump" href="#s4">Section 4</a></p>';for(let i=1;i<=8;i++)sections+=`<h2 id="s${i}">Section ${i}</h2><p>${'Reading text. '.repeat(120)}</p>`;
  const flowing=await fetch(base+'/api/pages',{method:'POST',headers:{'content-type':'application/json',authorization:'Bearer browser-smoke-token-at-least-32-characters'},body:JSON.stringify({title:'Flowing reader',agent:'ExampleAgent',original_request:'PRIVATE-FLOW',slug:'flowing',html:sections})});
  if(flowing.status!==201)throw Error(await flowing.text());
  await page.setViewportSize({width:1280,height:800});await page.goto(base+'/flowing');
  const flowFrame=page.frameLocator('iframe');await flowFrame.locator('#jump').waitFor();await page.waitForTimeout(600);
  const sized=await page.evaluate(()=>({flowing:document.body.classList.contains('flowing-report'),height:document.querySelector('iframe').offsetHeight}));
  if(!sized.flowing||sized.height<1200||sized.height>20000)throw Error('desktop report did not flow at its own height: '+JSON.stringify(sized));
  await flowFrame.locator('#jump').click();await page.waitForFunction(()=>scrollY>500);await page.waitForTimeout(900);
  const landed=await page.evaluate(()=>document.querySelector('iframe').getBoundingClientRect().top)+await page.frames().find(f=>f!==page.mainFrame()).evaluate(()=>document.getElementById('s4').getBoundingClientRect().top);
  if(landed<30||landed>120)throw Error('in-report link landed at '+landed);
  const barShown=()=>page.evaluate(()=>document.querySelector('.compact-reader').classList.contains('is-visible'));
  await page.evaluate(()=>scrollTo(0,400));await page.waitForTimeout(300);await page.mouse.move(640,700);await page.mouse.wheel(0,200);await page.waitForTimeout(400);
  if(!await barShown())throw Error('compact bar hid while scrolling down');
    await page.locator('.compact-title').click();await page.getByRole('region',{name:'Page details'}).getByText('PRIVATE-FLOW').waitFor();
  await page.keyboard.press('Escape');if(await page.locator('#reader-details').isVisible())throw Error('Escape did not close page details');
  // A page in its own design never has contents floating over it: they open from the bar or the More menu.
  if(await page.evaluate(()=>document.body.classList.contains('toc-floating')||getComputedStyle(document.querySelector('.reader-toc')).display!=='none'))throw Error('contents floated over a custom page');
  await page.evaluate(()=>scrollTo(0,0));await page.waitForTimeout(300);
  await page.locator('.compact-contents').click();
  await page.getByRole('navigation',{name:'Contents'}).getByRole('link',{name:'Section 6'}).click();await page.waitForFunction(()=>document.querySelector('.reader-toc a[aria-current]')?.dataset.id==='s6');
  const railLanded=await page.evaluate(()=>document.querySelector('iframe').getBoundingClientRect().top)+await page.frames().find(f=>f!==page.mainFrame()).evaluate(()=>document.getElementById('s6').getBoundingClientRect().top);
  if(railLanded<30||railLanded>120||await page.evaluate(()=>location.hash)!=='#s6')throw Error('contents link landed at '+railLanded);
  await page.setViewportSize({width:390,height:844});await page.goto(base+'/flowing');await flowFrame.locator('#jump').waitFor();await page.waitForTimeout(600);
  await page.mouse.move(195,700);await page.mouse.wheel(0,1500);await page.waitForTimeout(300);await page.mouse.wheel(0,-120);await page.waitForTimeout(400);
  // On a phone the current section's name in the bar opens the contents.
  await page.locator('.compact-title').click();await page.getByRole('navigation',{name:'Contents'}).getByRole('link',{name:'Section 2'}).click();
  await page.waitForFunction(()=>document.querySelector('.compact-section').textContent==='Section 2'&&!document.body.classList.contains('contents-open'));
  await page.goto(base+'/import');
  await page.locator('[name=document]').setInputFiles({name:'notes.md',mimeType:'text/markdown',buffer:Buffer.from('# Imported from a file\n\nA private report.')});
  await page.locator('[name=title]').fill('Imported notes');await page.locator('details summary').click();await page.locator('[name=agent]').fill('the owner');await page.locator('[name=original_request]').fill('Private import request');await page.locator('[name=slug]').fill('import-check');
  await page.getByRole('button',{name:'Save private page'}).click();await page.waitForURL('**/import-check',{timeout:5000}).catch(async()=>{throw Error('Import failed at '+page.url()+': '+await page.locator('body').innerText())});
  await page.frameLocator('iframe').getByRole('heading',{name:'Imported from a file'}).waitFor();
  await page.goto(base+'/import');
  await page.locator('[name=document]').setInputFiles({name:'Holiday photos.zip',mimeType:'application/zip',buffer:Buffer.alloc(3<<20,7)});
  await page.locator('details summary').click();await page.locator('[name=slug]').fill('file-check');
  await page.getByRole('button',{name:'Save private page'}).click();await page.waitForURL('**/file-check');
  for(const width of [390,1280]){
   await page.setViewportSize({width,height:844});await page.goto(base+'/file-check');await page.locator('.file-page .file-icon-archive').waitFor();
   const card=await page.evaluate(()=>({frame:!!document.querySelector('iframe'),wide:document.documentElement.scrollWidth>innerWidth+1,name:document.querySelector('.file-name').textContent,facts:document.querySelector('.file-facts').textContent,icon:document.querySelector('.file-page .file-icon').getBoundingClientRect().width}));
   if(card.frame||card.wide||card.name!=='Holiday photos.zip'||card.facts!=='ZIP archive · 3 MB'||card.icon<40)throw Error('file page: '+JSON.stringify(card));
  }
  for(const theme of ['system','cairn','dark','minimal','futuristic']){
   for(const width of [320,390,1280]){
    await page.setViewportSize({width,height:844});await page.evaluate(t=>document.documentElement.dataset.theme=t,theme);
    const state=await page.evaluate(()=>{const card=document.querySelector('.file-card'),icon=card.querySelector('.file-icon'),button=card.querySelector('.file-download'),a=card.getBoundingClientRect(),b=button.getBoundingClientRect();return {wide:document.documentElement.scrollWidth>innerWidth+1,inside:b.left>=a.left&&b.right<=a.right+1,ink:getComputedStyle(icon).color,paper:getComputedStyle(card).backgroundColor};});
    if(state.wide||!state.inside||state.ink===state.paper)throw Error('file theme '+theme+' at '+width+': '+JSON.stringify(state));
   }
  }
  await page.goto(base+'/file-check');
  const [saved]=await Promise.all([page.waitForEvent('download'),page.getByRole('link',{name:'Download',exact:true}).click()]);
  if(saved.suggestedFilename()!=='Holiday photos.zip')throw Error('file download named '+saved.suggestedFilename());
  await page.goto(base+'/');await page.locator('.report-row .file-mark .file-icon-archive').waitFor();
  await page.goto(base+'/import');
  await page.locator('[name=document]').setInputFiles({name:'custom.html',mimeType:'text/html',buffer:Buffer.from('<h1>Custom HTML import</h1>')});
  await page.locator('[name=title]').fill('Custom HTML');await page.locator('details summary').click();await page.locator('[name=agent]').fill('the owner');await page.locator('[name=original_request]').fill('Private HTML request');await page.locator('[name=slug]').fill('html-import');
  await page.getByRole('button',{name:'Save private page'}).click();await page.waitForURL('**/html-import');await page.frameLocator('iframe').getByRole('heading',{name:'Custom HTML import'}).waitFor();
  await page.goto(base+'/');await page.getByRole('searchbox').fill('Private HTML request');
  await page.getByRole('heading',{name:'Custom HTML',exact:true}).waitFor();await page.getByRole('button',{name:'Actions for Custom HTML',exact:true}).click();await page.locator('.row-menu:popover-open').getByRole('menuitem',{name:/^Manage page/}).click();
  await page.getByLabel('Title',{exact:true}).fill('Renamed import');await page.getByRole('button',{name:'Save title',exact:true}).click();
  await page.getByText('Title saved.').waitFor();
  await page.getByRole('button',{name:'Archive page',exact:true}).click();await page.getByText('Archived. The page is hidden').waitFor();
  await page.goto(base+'/');if(await page.getByRole('heading',{name:'Renamed import',exact:true}).count())throw Error('archived page visible');
  await page.getByRole('link',{name:/^Archive \d+$/}).click();await page.getByRole('heading',{name:'Renamed import',exact:true}).waitFor();
  await page.goto(base+'/manage/html-import');await page.getByRole('button',{name:'Restore to library',exact:true}).click();await page.getByText('Restored.').waitFor();
  // The confirmation is a toast over the page; put it away like a person would before reaching under it.
  await page.locator('.toast-close').click();await page.locator('.toast').waitFor({state:'detached'});
  await page.locator('summary',{hasText:'Delete page'}).click();await page.getByRole('button',{name:'Keep page',exact:true}).click();
  if(await page.locator('.delete-confirm').evaluate(d=>d.open))throw Error('Keep page did not close the confirmation');
  await page.locator('summary',{hasText:'Delete page'}).click();await page.getByRole('button',{name:'Delete permanently',exact:true}).click();
  await page.getByText('Deleted “Renamed import”.').waitFor();
  if((await fetch(base+'/html-import')).status!==404)throw Error('deleted page readable');
  await page.goto(base+'/manage/report');if(!(await page.locator('#sharing-state').innerText()).includes('reports.example.com/report'))throw Error('sharing changed private path');await page.getByRole('switch',{name:'Sharing'}).click();await page.getByText('Shared. Anyone with the link can read this page.').waitFor();
  const publicBase='http://'+publicAddress;
  await page.goto(publicBase+'/report');await page.frameLocator('iframe').getByRole('heading',{name:'Bulb comparison',exact:true}).waitFor();
  if((await page.content()).includes('PRIVATE-TEMPLATE-MARKER'))throw Error('public wrapper leaked context');
  if((await fetch(publicBase+'/')).status!==404 || (await fetch(publicBase+'/manage/report')).status!==404)throw Error('public management route exposed');
  await page.goto(base+'/manage/report');await page.locator('#share-password').fill('browser-secret');await page.getByRole('button',{name:'Save sharing',exact:true}).click();await page.getByText('Password set.').waitFor();
  await page.goto(publicBase+'/report');await page.getByLabel('Password',{exact:true}).fill('browser-secret');await page.getByRole('button',{name:'Unlock page',exact:true}).click();
  await page.frameLocator('iframe').getByRole('heading',{name:'Bulb comparison',exact:true}).waitFor();
  const unlocked=page.frames().find(f=>f!==page.mainFrame());
  await unlocked.waitForFunction(()=>document.querySelector('img')?.naturalWidth>0);
  await page.goto(base+'/manage/report');await page.locator('#share-password').fill('changed-secret');await page.getByRole('button',{name:'Save sharing',exact:true}).click();await page.getByText('Password changed.').waitFor();
  await page.goto(publicBase+'/report');await page.getByRole('button',{name:'Unlock page',exact:true}).waitFor();
  await page.goto(base+'/manage/report');await page.getByRole('switch',{name:'Sharing'}).click();await page.getByText('Sharing is off.').waitFor();
  if((await fetch(publicBase+'/report')).status!==404)throw Error('revoked share readable');
  if((await fetch(base+'/report')).status!==200)throw Error('private route lost after revocation');
  // Exercise the newer editor and settings flows with real browser events.
  await page.goto(base+'/new');
  await page.getByLabel('Title',{exact:true}).fill('Editor smoke');
  await page.getByLabel('Markdown',{exact:true}).fill('# Editor smoke\n\nSaved from the editor.');
  await page.getByRole('button',{name:'Preview',exact:true}).click();
  await page.frameLocator('#editor-preview').getByRole('heading',{name:'Editor smoke'}).waitFor();
  await page.getByRole('button',{name:'Write',exact:true}).click();
  await page.getByRole('button',{name:'Page access and link name'}).click();
  await page.getByRole('radio',{name:/Anyone with the link/}).check();
  await page.getByRole('button',{name:'Close page access'}).click();
  await page.getByRole('button',{name:/Save & share/}).click();
  await page.waitForURL(url=>/^\/[A-Za-z0-9]{3}$/.test(url.pathname));
  const editorPath=new URL(page.url()).pathname;
  if((await fetch(publicBase+editorPath)).status!==200)throw Error('editor did not share at its private path');
  if(!(await fetch(base+editorPath+'?download=1')).ok)throw Error('editor source missing');
  await page.goto(base+'/manage'+editorPath);await page.getByRole('switch',{name:'Sharing'}).click();await page.getByText('Sharing is off.').waitFor();
  if((await fetch(publicBase+editorPath)).status!==404 || (await fetch(base+editorPath)).status!==200)throw Error('editor revocation lost private access');
  // Editing reopens the saved writing and replaces the page at the same address.
  await page.goto(base+'/edit'+editorPath);
  if(await page.getByLabel('Title',{exact:true}).inputValue()!=='Editor smoke'||!(await page.getByLabel('Markdown',{exact:true}).inputValue()).includes('Saved from the editor.'))throw Error('edit did not reopen the writing');
  await page.getByLabel('Markdown',{exact:true}).fill('# Editor smoke\n\nEdited in place.');
  await page.keyboard.press(process.platform==='darwin'?'Meta+p':'Control+p');await page.frameLocator('#editor-preview').getByText('Edited in place.').waitFor();
  if(await page.locator('#editor-form').getAttribute('data-mode')!=='preview'||await page.locator('#page-title').isVisible())throw Error('preview did not replace the writing');
  await page.getByRole('button',{name:'Save changes'}).click();await page.waitForURL(url=>url.pathname===editorPath);
  await page.frameLocator('iframe').getByText('Edited in place.').waitFor();
  if(!(await (await fetch(base+editorPath+'?download=1')).text()).includes('Edited in place.'))throw Error('edit did not keep the Markdown');
  await page.goto(base+'/edit'+editorPath);if(await page.locator('#draft-note').isVisible())throw Error('a saved edit came back as a draft');
  // Errors sit beside the field in Cairn's words, never as a browser bubble.
  await page.getByLabel('Title',{exact:true}).fill('');await page.getByRole('button',{name:'Save changes'}).click();
  await page.locator('#page-title-error').getByText('Give the page a title.').waitFor();
  if(new URL(page.url()).pathname!=='/edit'+editorPath||await page.evaluate(()=>document.activeElement.id)!=='page-title')throw Error('empty title was not caught in place');
  page.once('dialog',dialog=>dialog.accept());await page.goto(base+'/');
  await page.goto(base+'/settings');
  if(await page.locator('[name=link_length]').inputValue()!=='3')throw Error('default link length changed');
  {
   // People & agents: connecting an agent leads the section; the key dialog runs create, copy,
   // instructions, test; focus stays inside and returns to the opener; changes are announced.
   const keys=await browser.newContext({viewport:{width:1280,height:900},permissions:['clipboard-read','clipboard-write']});
   const k=await keys.newPage();k.setDefaultTimeout(10000);
   try{
    await k.goto(base+'/settings#agents');
    if(await k.evaluate(()=>document.querySelector('.settings-section').id)!=='people'||!await k.locator('#connect-agent').isVisible())throw Error('settings does not lead with connecting an agent');
    await k.locator('#new-person-key').click();
    await k.locator('#new-person-name-error').getByText('Enter a name.').waitFor();
    if(await k.evaluate(()=>document.activeElement.id)!=='new-person-name'||await k.locator('#new-person-name').getAttribute('aria-invalid')!=='true'||await k.locator('#key-dialog').evaluate(d=>d.open))throw Error('an empty agent name was not caught in place');
    // One click on Create key makes the key: the dialog opens with it, never with a second Create key.
    await k.locator('#new-person-name').fill('Browser agent');await k.locator('#new-person-key').click();
    await k.locator('#key-dialog[open]').waitFor();
    await k.waitForFunction(()=>document.querySelector('#key-secret').value.startsWith('cairn_'));
    if(await k.locator('#step-create').isVisible())throw Error('the key dialog asked for a second Create key');
    // Tab stays inside the dialog.
    for(let i=0;i<6;i++){await k.keyboard.press('Tab');if(!await k.evaluate(()=>!!document.activeElement.closest('#key-dialog')))throw Error('Tab left the key dialog');}
    await k.locator('#key-copy').focus();
    const key=await k.locator('#key-secret').inputValue();
    if(await k.locator('#key-secret').evaluate(el=>el.tagName+el.readOnly)!=='INPUTtrue'||await k.evaluate(()=>document.activeElement.id)!=='key-copy')throw Error('the key is not a read-only one-line field with Copy focused');
    await k.waitForFunction(()=>document.querySelector('#key-status').textContent.includes('Copy it now'));
    const order=await k.evaluate(()=>[...document.querySelectorAll('.key-step')].filter(el=>!el.hidden).map(el=>el.id).join(' '));
    if(order!=='step-copy step-setup step-test')throw Error('key steps out of order: '+order);
    // Leaving with an uncopied key asks first; Escape does not lose it.
    await k.keyboard.press('Escape');await k.locator('#key-confirm:not([hidden])').getByText('You haven’t copied the key.').waitFor();
    if(!await k.locator('#key-dialog').evaluate(d=>d.open)||await k.evaluate(()=>document.activeElement.id)!=='key-confirm-yes')throw Error('closing an uncopied key did not confirm in place');
    await k.locator('#key-confirm-yes').click();await k.locator('#key-copy.is-done').waitFor();
    if(await k.evaluate(()=>navigator.clipboard.readText())!==key)throw Error('Copy key copied something else');
    if(await k.locator('#key-confirm').isVisible())throw Error('the dialog still says the key was not copied');
    // The dialog's title and close button stay in view while it scrolls.
    await k.locator('#step-test').scrollIntoViewIfNeeded();
    const head=await k.locator('#key-dialog .photo-dialog-head').boundingBox(),box=await k.locator('#key-dialog').boundingBox();
    if(!head||head.y>box.y+2)throw Error('the key dialog header scrolled away');
    await k.waitForFunction(()=>document.querySelector('#key-status').textContent.startsWith('Key copied'));
    await k.locator('#setup-copy').click();
    const setup=await k.evaluate(()=>navigator.clipboard.readText());
    if(setup.includes(key)||setup.includes('cairn_')||/Maintainer|contract\.md|<!--|\{\{/.test(setup)||!setup.includes('$CAIRN_API_KEY')||!setup.includes('Library: '+base)||!setup.includes('Your byline: Browser agent'))throw Error('setup text leaks a secret or maintainer text, or lacks the origin and byline');
    await k.locator('#command-copy').click();
    if(await k.evaluate(()=>navigator.clipboard.readText())!==`CAIRN_URL=${base} cairn publish page.md -request "the owner’s words"`)throw Error('publish command');
    if(await k.locator('#test-command').textContent()!==`curl -sS -H "Authorization: Bearer $CAIRN_API_KEY" "${base}/api/pages?limit=1"`)throw Error('test command');
    // The test command does what the dialog says.
    const tested=await fetch(base+'/api/pages?limit=1',{headers:{authorization:'Bearer '+key}});
    if(tested.status!==200||!(await tested.text()).startsWith('{"pages":'))throw Error('the test command does not succeed as described');
    const keyed=await fetch(base+'/api/pages',{method:'POST',headers:{'content-type':'application/json',authorization:'Bearer '+key},body:JSON.stringify({title:'Keyed report',original_request:'Private keyed request',markdown:'Hello from a key'})});
    if(keyed.status!==201)throw Error('new agent key failed');
    await k.locator('#key-close').click();await k.waitForFunction(()=>!document.querySelector('#key-dialog').open);
    await k.waitForFunction(()=>document.activeElement.id==='new-person-key');
    await k.locator('.person-row',{hasText:'Browser agent'}).getByText('Key active').waitFor();
    if(await k.locator('#key-secret').inputValue()!==''||(await k.content()).includes(key))throw Error('the key stayed in the page');
    // Revoking confirms in the dialog, and focus returns to the row's Manage key.
    const manage=k.getByRole('button',{name:'Manage key for Browser agent'});
    await manage.click();await k.locator('#key-dialog[open]').waitFor();
    if(!(await k.locator('#key-state').textContent()).startsWith('Key active.')||await k.locator('#step-create').isVisible()||await k.locator('#step-copy').isVisible())throw Error('reopening an active key shows the wrong steps');
    // Rotating confirms, shows the new key once and stops the old one.
    await k.locator('#key-rotate').click();await k.locator('#key-confirm').getByText('Rotate the key for Browser agent?').waitFor();
    await k.locator('#key-confirm-yes').click();
    await k.waitForFunction(old=>document.querySelector('#key-secret').value.startsWith('cairn_')&&document.querySelector('#key-secret').value!==old,key);
    const rotated=await k.locator('#key-secret').inputValue();
    if((await fetch(base+'/api/pages',{headers:{authorization:'Bearer '+key}})).status!==401||(await fetch(base+'/api/pages',{headers:{authorization:'Bearer '+rotated}})).status!==200)throw Error('rotation did not replace the key');
    await k.locator('#key-copy').click();await k.locator('#key-copy.is-done').waitFor();
    await k.locator('#key-revoke').click();await k.locator('#key-confirm').getByText('Revoke the key for Browser agent?').waitFor();
    await k.locator('#key-confirm-no').click();
    if(await k.locator('#key-confirm').isVisible()||await k.evaluate(()=>document.activeElement.id)!=='key-revoke')throw Error('Keep key did not return to Revoke key');
    await k.locator('#key-revoke').click();await k.locator('#key-confirm-yes').click();
    await k.locator('#step-create:not([hidden])').waitFor();
    await k.waitForFunction(()=>document.querySelector('#key-status').textContent==='Key revoked.');
    if((await fetch(base+'/api/pages',{headers:{authorization:'Bearer '+rotated}})).status!==401)throw Error('revoked key accepted');
    await k.keyboard.press('Escape');await k.waitForFunction(()=>!document.querySelector('#key-dialog').open);
    await k.waitForFunction(()=>document.activeElement.getAttribute('aria-label')==='Create key for Browser agent');
    await k.locator('.person-row',{hasText:'Browser agent'}).getByText('No key').waitFor();
   }finally{await keys.close();}
  }
  // Themes. One click on a card applies a theme to the page at once and saves it for every view; a
  // shared-pages theme of its own holds until it is set back. Without JavaScript the cards are radio
  // buttons with a Save button.
  const themeNotice=name=>name+' is on everywhere, shared pages included.';
  const setThemes=async(theme,reader='',shared='')=>{
   await page.goto(base+'/settings');
   if(reader||shared){
    await page.evaluate(()=>{document.querySelector('.theme-views').open=true;});
    await page.getByLabel('Reading your pages',{exact:true}).selectOption(reader===theme?'':reader);
    await page.getByLabel('Shared pages',{exact:true}).selectOption(shared===theme?'':shared);
   }
   const card=page.locator('.theme-choice[data-choice="'+theme+'"]');
   if(await card.locator('input').isChecked()){
    // Already chosen: choosing it again changes nothing, so save the per-view choices directly.
    await page.evaluate(()=>document.querySelector('#theme-form').requestSubmit());
   }else await card.click();
   await page.locator('.toast.is-success').last().waitFor();
   await page.waitForFunction(theme=>document.documentElement.dataset.theme===theme,theme);
  };
  await page.goto(base+'/manage/report');await page.getByRole('switch',{name:'Sharing'}).click();await page.getByText('Shared. Anyone with the link can read this page.').waitFor();
  await page.getByRole('button',{name:'Remove password',exact:true}).click();
  {
   // One click: the page changes before the reply, the notice confirms, nothing else is needed.
   await page.goto(base+'/settings');
   if(await page.getByRole('button',{name:'Save theme',exact:true}).isVisible())throw Error('Save theme shows although the cards save themselves');
   await page.locator('#appearance').scrollIntoViewIfNeeded();
   await page.locator('.theme-choice[data-choice="dark"]').click();
   if(await page.locator('html').getAttribute('data-theme')!=='dark')throw Error('theme did not apply on click');
   if(await page.locator('meta[name="color-scheme"]').getAttribute('content')!=='dark')throw Error('color-scheme meta not updated');
   await page.locator('.toast').getByText(themeNotice('Dark')).waitFor();
   if(await page.locator('meta[name="theme-color"]').first().getAttribute('content')!=='#111b22')throw Error('theme-color meta not updated');
   if(await page.locator('body').evaluate(el=>getComputedStyle(el).backgroundColor)!=='rgb(17, 27, 34)')throw Error('dark not painted');
   await page.reload();
   if(await page.locator('html').getAttribute('data-theme')!=='dark'||!await page.locator('.theme-choice[data-choice="dark"] input').isChecked())throw Error('one-click theme did not persist');
   for(const url of [base+'/',base+'/report',publicBase+'/report']){await page.goto(url);if(await page.locator('html').getAttribute('data-theme')!=='dark')throw Error('one click missed '+url);}
  }
  await setThemes('minimal','futuristic','cairn');
  for(const [url,theme] of [[base+'/','minimal'],[base+'/settings','minimal'],[base+'/new','minimal'],[base+'/import','minimal'],[base+'/manage/report','minimal'],[base+'/report','futuristic'],[publicBase+'/report','cairn']]){
   await page.goto(url);
   if(await page.locator('html').getAttribute('data-theme')!==theme)throw Error('wrong theme at '+url);
  }
  // A card click keeps shared pages on their own theme.
  await setThemes('dark');
  await page.goto(publicBase+'/report');if(await page.locator('html').getAttribute('data-theme')!=='cairn')throw Error('shared pages lost their own theme');
  const backgrounds={cairn:'rgb(238, 242, 245)',dark:'rgb(17, 27, 34)',minimal:'rgb(255, 255, 255)',futuristic:'rgb(26, 27, 38)'};
  for(const theme of ['cairn','dark','minimal','futuristic']){
   await setThemes(theme,theme,theme);
   await page.reload();
   if(!await page.locator('.theme-choice[data-choice="'+theme+'"] input').isChecked()||await page.locator('#public-theme').inputValue()!=='')throw Error('theme selection did not persist');
   for(const width of [320,390,1440]){
    await page.setViewportSize({width,height:900});
    for(const route of ['/','/settings','/new','/import','/manage/report','/report']){
     await page.goto(base+route);
     if(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth+1))throw Error('theme overflow '+theme+' '+width+' '+route);
     if(await page.locator('html').getAttribute('data-theme')!==theme)throw Error('missing theme '+route);
     const background=await page.locator('body').evaluate(el=>getComputedStyle(el).backgroundColor);
     if(background!==backgrounds[theme])throw Error('incorrect background '+theme+' '+route+' '+background);
     if(process.env.CAIRN_THEME_SCREENSHOTS && width!==320 && ['/','/settings','/report'].includes(route)){
      fs.mkdirSync(process.env.CAIRN_THEME_SCREENSHOTS,{recursive:true});
      await page.screenshot({path:path.join(process.env.CAIRN_THEME_SCREENSHOTS,theme+'-'+(route.slice(1)||'library')+'-'+width+'.png'),fullPage:true});
     }
    }
    await page.goto(publicBase+'/report');
    if(await page.locator('html').getAttribute('data-theme')!==theme)throw Error('public theme not applied');
    if((await page.content()).includes('PRIVATE-TEMPLATE-MARKER'))throw Error('theme exposed private context');
   }
  }
  await page.setViewportSize({width:1280,height:900});
  {
   // Every surface takes the theme: its background is dark in the dark themes and light in the light
   // ones (the cover and bar stay navy in Cairn), and its text reads on it at 4.5:1 or better.
   const lum=c=>{const p=c.match(/[\d.]+/g).map(Number);const f=v=>{v/=255;return v<=.03928?v/12.92:((v+.055)/1.055)**2.4;};return .2126*f(p[0])+.7152*f(p[1])+.0722*f(p[2]);};
   const ratio=(a,b)=>{const x=lum(a),y=lum(b);return (Math.max(x,y)+.05)/(Math.min(x,y)+.05);};
   const look=selector=>`(()=>{const el=document.querySelector(${JSON.stringify(selector)});if(!el)return null;const bgOf=n=>{for(;n;n=n.parentElement){const c=getComputedStyle(n).backgroundColor;if(!/rgba\\(.*,\\s*0\\)$/.test(c)&&c!=='transparent')return c;}return getComputedStyle(document.documentElement).backgroundColor;};return {bg:bgOf(el),fg:getComputedStyle(el).color};})()`;
   const open=async(p,url,act)=>{await p.goto(url);await p.waitForTimeout(250);if(act)await act(p);};
   const surfaces=[
    ['library',base+'/',null,'.row-title'],
    ['search highlight',base+'/?q=bulb',null,'mark',{mark:true}],
    ['unread dot',base+'/',null,'.report-row.is-unread .row-main',{dot:true}],
    ['row menu',base+'/',async p=>{await p.locator('.row-more').first().click();},'.row-menu [role=menuitem]'],
    ['shortcut sheet',base+'/',async p=>{await p.keyboard.press('?');},'#shortcuts dd'],
    ['drawer',base+'/',async p=>{await p.locator('.row-title').first().click();await p.locator('#preview[open]').waitFor();},'#preview-title'],
    ['editor write',base+'/new',null,'.editor-paper .editor-foot'],
    ['editor preview',base+'/new',async p=>{await p.fill('#markdown','# Preview\n\nText.');await p.click('#preview-button');await p.waitForTimeout(600);},'.preview-title',{cover:true}],
    ['access popover',base+'/new',async p=>{await p.click('#access-trigger');},'.access-choice strong'],
    ['import drop zone',base+'/import',null,'.dropzone'],
    ['manage switch',base+'/manage/report',null,'.switch-text'],
    ['danger zone',base+'/manage/report/delete',null,'#delete .button.danger-quiet, #delete summary'],
    ['key dialog',base+'/settings',async p=>{await p.evaluate(()=>document.querySelector('#key-dialog').showModal());},'#key-dialog h2'],
    ['settings notice',base+'/settings?saved=writing#writing',null,'.toast-text'],
    ['inline error',base+'/manage/report',async p=>{await p.fill('#link-name','settings');await p.locator('button[value=sharing]').click();await p.waitForTimeout(400);},'#link-name-error span'],
    ['owner error page',base+'/no-such-page',null,'main h1'],
    ['visitor error page',publicBase+'/no-such-page',null,'main h1'],
    ['reader cover',base+'/rich',null,'.reader-cover .report-title',{cover:true}],
    ['reader bar',base+'/rich',async p=>{await p.evaluate(()=>scrollTo(0,1600));await p.waitForTimeout(500);},'.compact-page',{cover:true}],
    ['reader menu',base+'/rich',async p=>{await p.evaluate(()=>scrollTo(0,1600));await p.waitForTimeout(500);await p.locator('.compact-more').click();},'#reader-menu .menu-status span'],
    ['reader details',base+'/rich',async p=>{await p.evaluate(()=>scrollTo(0,1600));await p.waitForTimeout(500);await p.locator('.compact-title').click();},'.details-title',{cover:true}],
    ['reader end',base+'/rich',async p=>{await p.evaluate(()=>scrollTo(0,document.documentElement.scrollHeight));await p.waitForTimeout(700);},'.end-back'],
    ['public strip',publicBase+'/report',null,'.reader-strip .byline-who'],
    ['settings',base+'/settings',null,'.settings-heading h1'],
   ];
   const failures=[];
   for(const theme of ['cairn','dark','minimal','futuristic','system']){
    await setThemes(theme);
    // A page nobody has opened yet, for the unread dot.
    await fetch(base+'/api/pages',{method:'POST',headers:{'content-type':'application/json',authorization:'Bearer browser-smoke-token-at-least-32-characters'},body:JSON.stringify({title:'Unread in '+theme,agent:'ExampleAgent',original_request:'Unread check',slug:'unread-'+theme,markdown:'# Unread\n\nNot opened yet.'})});
    const ctx=await browser.newContext({viewport:{width:1280,height:900},colorScheme:theme==='system'?'dark':'light'});
    const dark=['dark','futuristic','system'].includes(theme);
    try{
     for(const [name,url,act,selector,opts={}] of surfaces){
      const p=await ctx.newPage();
      try{
       await open(p,url,act);
       const got=await p.evaluate(look(selector));
       if(!got){failures.push(theme+' '+name+': '+selector+' missing');continue;}
       const bgLum=lum(got.bg),navy=opts.cover&&theme==='cairn';
       if(!opts.mark&&(dark?bgLum>.06:(bgLum<.4&&!navy)))failures.push(theme+' '+name+': background '+got.bg);
       if(ratio(got.fg,got.bg)<4.5)failures.push(theme+' '+name+': text '+got.fg+' on '+got.bg);
       if(opts.dot&&ratio(await p.evaluate(()=>getComputedStyle(document.querySelector('.unread-dot')).backgroundColor),got.bg)<3)failures.push(theme+' '+name+': dot');
      }finally{await p.close();}
     }
     // The report itself: a Markdown page goes dark with the theme, from the reader's message when the
     // theme follows the device; custom HTML keeps its own paper.
     const p=await ctx.newPage();
     try{
      await p.goto(base+'/rich');const frame=p.frames().find(f=>f!==p.mainFrame());await frame.waitForLoadState('load');
      await frame.waitForFunction(dark=>(document.documentElement.dataset.reportScheme==='dark')===dark,dark,{timeout:5000}).catch(()=>failures.push(theme+' report scheme'));
      const paper=await frame.evaluate(()=>getComputedStyle(document.body).backgroundColor);
      if(dark?lum(paper)>.06:lum(paper)<.9)failures.push(theme+' report paper '+paper);
      if(theme==='futuristic'&&await frame.evaluate(()=>document.documentElement.dataset.reportPalette)!=='violet')failures.push('futuristic report palette');
      await p.goto(base+'/browser');
      if(!await p.locator('body.report-fixed').count())failures.push(theme+' custom HTML not framed as its own design');
     }finally{await p.close();}
    }finally{await ctx.close();}
   }
   if(failures.length)throw Error('themes miss surfaces: '+failures.join('; '));
  }
  // Appearance works without JavaScript: radio buttons and a Save button.
  const noJS=await browser.newContext({javaScriptEnabled:false});
  try{
   const plain=await noJS.newPage();await plain.goto(base+'/settings');
   await plain.locator('.theme-choice[data-choice="minimal"]').click();
   await plain.getByRole('button',{name:'Save theme',exact:true}).click();await plain.waitForURL(/\/settings\?saved=appearance#appearance$/);await plain.getByText(themeNotice('Minimal')).waitFor();
   if(await plain.locator('html').getAttribute('data-theme')!=='minimal')throw Error('no-JavaScript theme save failed');
  }finally{await noJS.close();}
  await setThemes('cairn');
  for(const width of [320,390,1440]){
   await page.setViewportSize({width,height:900});
   for(const route of ['/','/settings','/new','/report']){
    await page.goto(base+route);
    if(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth+1))throw Error('horizontal overflow at '+width+' '+route);
   }
  }
  await page.setViewportSize({width:1280,height:900});await page.goto(base+'/report');
  if(process.env.CAIRN_SCREENSHOT)await page.screenshot({path:process.env.CAIRN_SCREENSHOT,fullPage:true});
  // Real form posts from the reader and the library menu must pass the owner check.
  await page.goto(base+'/settings#kindle');if(await page.getByRole('button',{name:'Save Kindle address'}).isVisible())throw Error('Kindle shows a Save button although the field saves itself');await page.getByLabel('Kindle email address').fill('reader@kindle.com');await page.keyboard.press('Tab');await page.locator('.toast').getByText('Kindle address saved.').waitFor();await page.locator('.save-state[data-for="kindle-address"].is-saved').waitFor();
  // In the reader, Send to Kindle lives in the More menu; the result is a toast, so the cover never moves.
  await page.goto(base+'/report');const coverHeight=await page.evaluate(()=>document.querySelector('#cover').offsetHeight);
  await page.locator('.reader-cover .more-button').click();await page.locator('#reader-menu .menu-kindle').click();await page.locator('.toast').getByText('Sent to your Kindle').waitFor();
  if(await page.evaluate(()=>document.querySelector('#cover').offsetHeight)!==coverHeight||await page.locator('#cover .notice').count())throw Error('the Kindle result moved the cover');
  if(new URL(page.url()).search)throw Error('send notice stayed in the address');
  await page.goto(base+'/');await page.locator('.report-row').first().getByRole('button',{name:/^Actions for /}).click();
  await page.locator('.row-menu:popover-open').getByRole('menuitem',{name:'Send to Kindle',exact:true}).click();await page.getByText('Sent to your Kindle').waitFor();
  if(!await page.locator('.toast.is-success').isVisible()||await page.locator('.row-notice, .list-notice').count())throw Error('library Kindle notice is not a toast');
  if(mailbox.length!==2||!mailbox.every(m=>m.includes('To: reader@kindle.com')&&m.includes('application/epub+zip')))throw Error('relay did not receive two EPUB emails');
  if((await (await fetch(publicBase+'/report')).text()).includes('Send to Kindle'))throw Error('public reader offers Send to Kindle');
  // Finding: words that appear only in a page's body match while typing, the address follows, Back undoes it.
  const worktops=await fetch(base+'/api/pages',{method:'POST',headers:{'content-type':'application/json',authorization:'Bearer browser-smoke-token-at-least-32-characters'},body:JSON.stringify({title:'Kitchen worktops',agent:'ExampleAgent',original_request:'PRIVATE-WORKTOP',slug:'worktops',markdown:'# Kitchen worktops\n\nOak is the warmest choice for this kitchen.\n\n## Care\n\nThe quartzite sample stains less than marble.'})});
  if(worktops.status!==201)throw Error(await worktops.text());
  await page.setViewportSize({width:1280,height:900});await page.goto(base+'/');
  const before=await page.locator('.report-row').count();
  await page.locator('#search').pressSequentially('quartzi',{delay:20});
  await page.locator('.report-row[data-slug="worktops"] .snippet mark').waitFor();
  if(await page.locator('.report-row').count()!==1||!(await page.locator('.report-row .snippet').innerText()).startsWith('In the page:')||new URL(page.url()).searchParams.get('q')!=='quartzi')throw Error('search as you type: '+page.url());
  await page.goBack();await page.waitForFunction(n=>!location.search.includes('q=')&&document.querySelector('#search').value===''&&document.querySelectorAll('.report-row').length===n,before);
  await page.goForward();await page.waitForFunction(()=>document.querySelector('#search').value==='quartzi'&&document.querySelectorAll('.report-row').length===1);
  await page.locator('#search').press('Escape');await page.waitForFunction(n=>document.querySelector('#search').value===''&&document.querySelectorAll('.report-row').length===n,before);
  await page.locator('#search').fill('no such words anywhere');await page.getByRole('heading',{name:'No pages match “no such words anywhere”'}).waitFor();
  await page.getByRole('link',{name:'Clear search'}).click();await page.waitForFunction(n=>document.querySelectorAll('.report-row').length===n,before);
  // Keyboard: J and K move a visible selection, typing never triggers them, Space previews, J steps on, Escape returns.
  await page.locator('#search').focus();await page.keyboard.type('jk');
  if(await page.locator('#search').inputValue()!=='jk'||await page.locator('.report-row.is-selected').count())throw Error('shortcuts fired while typing');
  await page.locator('#search').fill('');await page.keyboard.press('Escape');await page.keyboard.press('Escape');
  await page.keyboard.press('j');await page.keyboard.press('j');await page.keyboard.press('k');
  const titles=await page.locator('.report-row').evaluateAll(rows=>rows.map(r=>r.dataset.title));
  // From the keyboard the selected row carries the ring, around the whole row rather than its title alone.
  const focused=()=>page.evaluate(()=>({title:document.activeElement.closest('.report-row')?.dataset.title,ring:getComputedStyle(document.activeElement.closest('.report-row')||document.activeElement).outlineStyle,selected:!!document.activeElement.closest('.report-row.is-selected')}));
  let at=await focused();if(at.title!==titles[0]||at.ring==='none'||!at.selected)throw Error('J and K selection: '+JSON.stringify(at));
  await page.keyboard.press('Space');await page.locator('#preview[open]').waitFor();
  if(await page.locator('#preview-title').innerText()!==titles[0]||!await page.locator('#preview-prev').isDisabled())throw Error('Space did not preview the selection');
  await page.keyboard.press('j');await page.waitForFunction(t=>document.querySelector('#preview-title').textContent===t,titles[1]);
  await page.getByRole('button',{name:'Previous page'}).click();await page.waitForFunction(t=>document.querySelector('#preview-title').textContent===t,titles[0]);
  await page.getByRole('button',{name:'Next page'}).click();await page.waitForFunction(t=>document.querySelector('#preview-title').textContent===t,titles[1]);
  await page.keyboard.press('Escape');await page.waitForFunction(()=>!document.querySelector('#preview').open&&document.activeElement.closest('.report-row'));
  at=await focused();if(at.title!==titles[1])throw Error('closing the preview lost the place: '+JSON.stringify(at));
  await page.keyboard.press('Enter');await page.waitForURL(url=>url.pathname!=='/');await page.goBack();await page.locator('#search').waitFor();
  await page.keyboard.press('?');await page.getByRole('heading',{name:'Keyboard shortcuts'}).waitFor();await page.keyboard.press('Escape');
  if(await page.locator('#shortcuts').evaluate(d=>d.open))throw Error('shortcut sheet did not close');
  // E archives the selected page with Undo; the row comes back in place.
  await page.locator('.report-row[data-slug="worktops"] .row-title').focus();await page.keyboard.press('e');
  const scrolled=await page.evaluate(()=>scrollY);
  await page.locator('.toast').getByText('Archived “Kitchen worktops”. Its links still work.').waitFor();
  if(await page.evaluate(()=>scrollY)!==scrolled)throw Error('the archive toast moved the page');
  if(await page.locator('.report-row[data-slug="worktops"]').count()||!(await (await fetch(base+'/?archived=1')).text()).includes('data-slug="worktops"'))throw Error('E did not archive');
  await page.locator('.toast').getByRole('button',{name:'Undo'}).click();await page.locator('.report-row[data-slug="worktops"]').waitFor();
  if((await (await fetch(base+'/?archived=1')).text()).includes('data-slug="worktops"'))throw Error('Undo did not restore');
  // The row menu: the actions a page allows, opened from its button and closed with Escape.
  await page.getByRole('button',{name:'Actions for Kitchen worktops',exact:true}).click();
  const items=await page.locator('.row-menu:popover-open [role=menuitem]').allInnerTexts();
  // Markdown sent through the API keeps its source, so the page can be edited.
  // Five actions in two groups: what to do with the page, then where it lives. Sharing lives under Manage page.
  if(items.map(t=>t.split('\n')[0]).join('|')!=='Edit|Copy link|Send to Kindle|Archive|Manage page'||await page.locator('.row-menu:popover-open .menu-rule').count()!==1)throw Error('row menu: '+items.join('|'));
  await page.keyboard.press('Escape');if(await page.evaluate(()=>document.activeElement.getAttribute('aria-label'))!=='Actions for Kitchen worktops')throw Error('menu did not return focus');
  await page.goto(base+'/?q=Editor+smoke');await page.getByRole('button',{name:'Actions for Editor smoke',exact:true}).click();
  await page.locator('.row-menu:popover-open').getByRole('menuitem',{name:'Edit'}).click();await page.waitForURL('**/edit'+editorPath);
  // Phones skip the drawer and open the reader.
  await page.setViewportSize({width:390,height:844});await page.goto(base+'/');
  await page.locator('.report-row .row-title').first().click();await page.waitForURL(url=>url.pathname!=='/');
  if(await page.locator('#preview[open]').count())throw Error('phone opened the drawer');
  await page.setViewportSize({width:1280,height:900});
  // Fifty at a time: Show more loads in place and the address keeps the count; without JavaScript it is a link.
  for(let i=0;i<50;i++){const r=await fetch(base+'/api/pages',{method:'POST',headers:{'content-type':'application/json',authorization:'Bearer browser-smoke-token-at-least-32-characters'},body:JSON.stringify({title:'Filler page '+i,agent:'Test',original_request:'Filler',html:'<p>Filler body.</p>'})});if(r.status!==201)throw Error(await r.text());}
  await page.goto(base+'/');if(await page.locator('.report-row').count()!==50)throw Error('first page is not fifty rows');
  await page.getByRole('link',{name:'Show more pages'}).click();await page.waitForFunction(()=>document.querySelectorAll('.report-row').length>50);
  if(!new URL(page.url()).searchParams.get('n')||!await page.evaluate(()=>document.activeElement.closest('#page-row-51')))throw Error('Show more did not load in place');
  const plainList=await browser.newContext({javaScriptEnabled:false});
  try{
   const plain=await plainList.newPage();await plain.goto(base+'/');
   await plain.getByRole('link',{name:'Show more pages'}).click();await plain.waitForURL(/n=100#page-row-51$/);
   if(await plain.locator('.report-row').count()<=50)throw Error('no-JavaScript Show more');
   await plain.locator('#search').fill('quartzite');await plain.getByRole('button',{name:'Search',exact:true}).click();await plain.waitForURL(/q=quartzite/);
   if(await plain.locator('.report-row').count()!==1)throw Error('no-JavaScript search');
   await plain.locator('.report-row a.row-more').click();await plain.waitForURL('**/manage/worktops');
  }finally{await plainList.close();}
  // Shared shell: a skip link comes first, focus stays visible, and assets come from cached URLs.
  await page.setViewportSize({width:1280,height:900});await page.goto(base+'/');
  await page.keyboard.press('Tab');await page.waitForTimeout(300);
  const skip=await page.evaluate(()=>{const a=document.activeElement,r=a.getBoundingClientRect();return {cls:a.className,top:r.top,outline:getComputedStyle(a).outlineStyle}});
  if(skip.cls!=='skip-link'||skip.top<0||skip.outline==='none')throw Error('skip link is not the first visible stop: '+JSON.stringify(skip));
  await page.keyboard.press('Enter');if(await page.evaluate(()=>location.hash)!=='#main')throw Error('skip link did not reach main');
  await page.locator('#search').click();
  if(await page.locator('#search').evaluate(el=>getComputedStyle(el).outlineStyle)==='none')throw Error('a clicked text field lost its focus ring');
  const sheet=await page.evaluate(()=>document.querySelector('link[rel=stylesheet]').href);
  const sheetResponse=await fetch(sheet);const sheetText=await sheetResponse.text();
  if(sheetResponse.headers.get('cache-control')!=='public, max-age=31536000, immutable'||!/url\(dm-sans-400\.[0-9a-f]{12}\.woff2\)/.test(sheetText))throw Error('stylesheet is not immutable or lacks hashed fonts');
  for(const route of ['/new','/import','/manage/report']){
   await page.goto(base+route);await page.keyboard.press('Tab');
   if(await page.evaluate(()=>document.activeElement.className)!=='skip-link')throw Error('no skip link on '+route);
   if(await page.getByRole('banner').getByRole('link',{name:'cairn'}).count()!==1||await page.getByRole('main').count()!==1)throw Error('shared shell missing on '+route);
  }
  await page.goto(base+'/new');await page.locator('#page-title').focus();
  if(await page.locator('#page-title').evaluate(el=>getComputedStyle(el).borderLeftColor)==='rgba(0, 0, 0, 0)')throw Error('editor title shows no focus');
  await page.keyboard.press('Tab');
  if(await page.locator('#markdown').evaluate(el=>document.activeElement===el&&getComputedStyle(el).borderLeftColor!=='rgba(0, 0, 0, 0)')!==true)throw Error('editor body shows no focus');
  // Tablets keep the access column as a named icon.
  await page.setViewportSize({width:820,height:1000});await page.goto(base+'/');
  // Only what is unusual gets a badge: Shared, in words and an icon. Private, the default, is not announced at all.
  const badges=await page.evaluate(()=>({columns:document.querySelectorAll('.access-cell, .badge').length,shared:[...document.querySelectorAll('.report-row[data-shared] .row-badge svg')].filter(el=>el.getBoundingClientRect().width).length,plain:[...document.querySelectorAll('.report-row:not([data-shared])')].filter(row=>row.querySelector('.row-badge')||/Private/.test(row.querySelector('.row-head').textContent)).length,rows:document.querySelectorAll('.report-row[data-shared]').length}));
  if(badges.columns||badges.plain||(badges.rows&&!badges.shared))throw Error('access badges are not only on shared rows: '+JSON.stringify(badges));
  // Deferred runtime: a Markdown report paints before its libraries load, and Mermaid comes only when
  // boot.js fetches it for a diagram near the screen.
  await page.goto(base+'/interactive');
  const runtimeFrame=page.frames().find(f=>f!==page.mainFrame());await runtimeFrame.locator('.diagram svg').waitFor();
  const runtimeTags=await runtimeFrame.evaluate(()=>[...document.querySelectorAll('script[src^="/_runtime/"]')].map(s=>({src:s.getAttribute('src'),parsed:!s.hasAttribute('data-lazy'),defer:s.defer})));
  if(!runtimeTags.length||runtimeTags.some(t=>!/\?v=[0-9a-f]{12}$/.test(t.src)||(t.parsed&&!t.defer))||runtimeTags.some(t=>t.parsed&&t.src.includes('mermaid'))||!runtimeTags.some(t=>!t.parsed&&t.src.includes('mermaid')))throw Error('runtime scripts are not versioned and deferred, with Mermaid fetched on demand: '+JSON.stringify(runtimeTags));
  // Touch: 44px targets for controls and standalone links.
  const touch=await browser.newContext({viewport:{width:390,height:844},hasTouch:true,isMobile:true});
  try{
   const phone=await touch.newPage();
   for(const [route,selectors] of [['/',['.topnav a','.row-more','.create-menu summary','.split-create > .button','#search','#filter-toggle']],['/manage/report',['.back-link','.topbar-settings','.title-actions button','.quiet-link','.switch','.copy-button','#link-name','.field-row button','.reveal','.danger-quiet']],['/import',['.dropzone','#import-title','.more-fields summary','.form-submit button']],['/report',['.reader-cover .brand','.reader-cover .more-button']]]){
    await phone.goto(base+route);
    for(const selector of selectors){
     const box=await phone.locator(selector).first().boundingBox();
     if(!box||box.height<44)throw Error('touch target under 44px: '+route+' '+selector+' '+JSON.stringify(box));
    }
   }
  }finally{await touch.close();}
  // Sharing as one control: share, copy, rename the link, set a password, turn off.
  const sharing=await browser.newContext({viewport:{width:1280,height:900},permissions:['clipboard-read','clipboard-write']});
  try{
   const owner=await sharing.newPage();
   await owner.goto(base+'/manage/flowing');
   if(await owner.getByRole('switch',{name:'Sharing'}).getAttribute('aria-checked')!=='false'||await owner.locator('#sharing-form').count())throw Error('private page shows sharing as on');
   await owner.getByRole('switch',{name:'Sharing'}).click();await owner.getByRole('status').getByText('Shared. Anyone with the link can read this page.').waitFor();
   if(await owner.getByRole('switch',{name:'Sharing'}).getAttribute('aria-checked')!=='true'||new URL(owner.url()).search)throw Error('sharing did not switch on in place');
   await owner.getByRole('button',{name:'Copy link'}).click();await owner.getByRole('button',{name:'Copied'}).waitFor();
   if(await owner.evaluate(()=>navigator.clipboard.readText())!=='https://reports.example.com/flowing')throw Error('copy link copied the wrong address');
   await owner.locator('#link-name').fill('flow-link');await owner.getByRole('button',{name:'Save sharing',exact:true}).click();await owner.locator('.toast').getByText('Link name saved.').waitFor();
   if(await owner.locator('#link-name').inputValue()!=='flow-link'||await owner.locator('.copy-button').getAttribute('data-copy-value')!=='https://reports.example.com/flow-link'||(await fetch(publicBase+'/flow-link')).status!==200)throw Error('link name did not change the public address');
   await owner.locator('#share-password').fill('flow-password');await owner.getByRole('button',{name:'Show password'}).click();
   if(await owner.locator('#share-password').getAttribute('type')!=='text'||await owner.getByRole('button',{name:'Hide password'}).getAttribute('aria-pressed')!=='true')throw Error('password show toggle failed');
   await owner.getByRole('button',{name:'Save sharing',exact:true}).click();await owner.locator('.toast').getByText('Password set. Visitors need it to read this page.').waitFor();
   // The status says the page has a password, and the password just set can go out with the link.
   if(!(await owner.locator('#sharing-state').innerText()).startsWith('Shared with a password.'))throw Error('status line does not say Shared with a password');
   await owner.getByRole('button',{name:'Copy link and password'}).click();await owner.locator('#copy-both.is-done').waitFor();
   if(await owner.evaluate(()=>navigator.clipboard.readText())!=='https://reports.example.com/flow-link\nPassword: flow-password')throw Error('Copy link and password copied the wrong thing');
   await owner.reload();if(await owner.getByRole('button',{name:'Copy link and password'}).isVisible()||await owner.evaluate(()=>Object.keys(sessionStorage).length))throw Error('the password outlived the save');
   if((await fetch(publicBase+'/flow-link')).status!==401)throw Error('password did not protect the link');
   // The gate: whose page it is, the error on the field, focus kept, and the pause after a wrong try.
   const visitor=await sharing.newPage();await visitor.goto(publicBase+'/flow-link');
   if(await visitor.getByRole('heading',{level:1}).innerText()!=='A page from ExampleAgent'||(await visitor.content()).includes('Flowing reader'))throw Error('gate names the page or not its author');
   if(await visitor.evaluate(()=>document.activeElement.id)!=='gate-password')throw Error('gate does not focus the password field');
   await visitor.getByLabel('Password',{exact:true}).fill('wrong guess');await visitor.getByRole('button',{name:'Unlock page'}).click();
   await visitor.getByRole('alert').getByText('That password didn’t match.').waitFor();
   const gate=await visitor.evaluate(()=>{const field=document.querySelector('#gate-password'),error=document.querySelector('#gate-password-error');return {focus:document.activeElement===field,invalid:field.getAttribute('aria-invalid'),described:field.getAttribute('aria-describedby'),error:getComputedStyle(error).color,border:getComputedStyle(field.parentElement).borderTopColor,ink:getComputedStyle(document.body).color}});
   if(!gate.focus||gate.invalid!=='true'||gate.described!=='gate-password-error'||gate.error!==gate.border||gate.error===gate.ink)throw Error('gate error is not on the field: '+JSON.stringify(gate));
   await visitor.getByLabel('Password',{exact:true}).fill('another guess');await visitor.getByRole('button',{name:'Unlock page'}).click();
   await visitor.getByRole('alert').getByText(/Wait a second, then try again|didn’t match/).waitFor();
   // Waits double after each counted wrong password: 1s, then 2s.
   await visitor.waitForTimeout(2100);await visitor.getByLabel('Password',{exact:true}).fill('flow-password');await visitor.getByRole('button',{name:'Unlock page'}).click();
   await visitor.frameLocator('iframe').locator('#jump').waitFor();
   await owner.getByRole('switch',{name:'Sharing'}).click();await owner.getByText('Sharing is off. The public link no longer works.').waitFor();
   if((await fetch(publicBase+'/flow-link')).status!==404)throw Error('turning sharing off left the link working');
   // Keyboard: the manage page reads top to bottom. Save title waits for an edit, so it is no stop until then.
   await owner.goto(base+'/manage/flowing');await owner.keyboard.press('Tab');
   const order=[];for(let i=0;i<8;i++){await owner.keyboard.press('Tab');order.push(await owner.evaluate(()=>{const a=document.activeElement;return a.id||a.getAttribute('role')||a.textContent.trim()}));}
   if(order.join(' > ')!=='cairn > Library > Settings > page-title > Open page > switch > Archive page > Delete page')throw Error('manage tab order: '+order.join(' > '));
   // Delete page opens its confirmation with focus on the question; Escape closes it and returns.
   await owner.keyboard.press('Enter');await owner.waitForFunction(()=>document.activeElement.classList.contains('confirm-question'));
   await owner.keyboard.press('Escape');await owner.waitForFunction(()=>!document.querySelector('.delete-confirm').open&&document.activeElement.matches('.delete-confirm summary'));
  }finally{await sharing.close();}
  // Import: the drop zone takes a file by click, keyboard or drop, and refuses what the server would.
  await page.setViewportSize({width:1280,height:900});await page.goto(base+'/import');
  if(!await page.locator('.dropzone.is-enhanced').count())throw Error('drop zone not enhanced');
  await page.locator('[name=document]').setInputFiles({name:'tool.exe',mimeType:'application/octet-stream',buffer:Buffer.from('MZ')});
  if(await page.locator('[name=document]').evaluate(el=>el.files.length)!==1)throw Error('inert file refused');
  await page.locator('[name=document]').setInputFiles({name:'notes.md',mimeType:'text/markdown',buffer:Buffer.from('# Dropped notes\n\nFrom the drop zone.')});
  if(await page.locator('.dropzone:has(#document) .dropzone-name').innerText()!=='notes.md'||!/bytes|KB/.test(await page.locator('.dropzone:has(#document) .dropzone-size').innerText())||await page.locator('#document-error').isVisible())throw Error('chosen file not shown');
  const dropped=await page.evaluateHandle(()=>{const d=new DataTransfer();d.items.add(new File(['# Dropped page\n\nIt arrived by drag and drop.'],'dropped.md',{type:'text/markdown'}));return d;});
  await page.locator('[name=document]').dispatchEvent('drop',{dataTransfer:dropped});
  if(await page.locator('.dropzone:has(#document) .dropzone-name').innerText()!=='dropped.md')throw Error('drop did not choose the file');
  await page.keyboard.press('Tab');await page.locator('#import-title').focus();await page.keyboard.down('Shift');await page.keyboard.press('Tab');await page.keyboard.up('Shift');
  if(await page.evaluate(()=>document.activeElement.name)!=='document'||await page.locator('.dropzone:has(#document)').evaluate(el=>getComputedStyle(el).outlineStyle)==='none')throw Error('drop zone shows no keyboard focus');
  await page.locator('.more-fields summary').click();await page.locator('#import-slug').fill('dropped');
  await page.getByRole('button',{name:'Save private page'}).click();await page.waitForURL('**/dropped');await page.frameLocator('iframe').getByRole('heading',{name:'Dropped page'}).waitFor();
  // A server-side error comes back on the form, beside its field, with input kept.
  await page.goto(base+'/import');await page.locator('[name=document]').setInputFiles({name:'again.md',mimeType:'text/markdown',buffer:Buffer.from('# Again')});
  await page.locator('#import-title').fill('Kept title');await page.locator('.more-fields summary').click();await page.locator('#import-slug').fill('dropped');
  await page.getByRole('button',{name:'Save private page'}).click();await page.locator('#import-slug-error').getByText('Another page already uses this link.').waitFor();
  if(await page.locator('#import-title').inputValue()!=='Kept title'||await page.locator('#import-slug').getAttribute('aria-invalid')!=='true'||!await page.locator('.more-fields').evaluate(d=>d.open))throw Error('import error lost input');
  // Without JavaScript the import form is a plain file form.
  const plainImport=await browser.newContext({javaScriptEnabled:false});
  try{
   const plain=await plainImport.newPage();await plain.goto(base+'/import');
   if(!await plain.locator('[name=document]').isVisible())throw Error('no-JavaScript import hides the file input');
   await plain.locator('[name=document]').setInputFiles({name:'plain.md',mimeType:'text/markdown',buffer:Buffer.from('# Plain import')});
   await plain.locator('summary').click();await plain.locator('#import-slug').fill('plain-import');await plain.getByRole('button',{name:'Save private page'}).click();await plain.waitForURL('**/plain-import');
  }finally{await plainImport.close();}
  // Draft protection: a reload restores unsaved writing, discard clears it, saving clears it.
  await page.goto(base+'/new');await page.getByLabel('Title',{exact:true}).fill('Unsaved thought');await page.getByLabel('Markdown',{exact:true}).fill('Words I would hate to lose.');await page.waitForTimeout(600);
  page.once('dialog',dialog=>dialog.accept());await page.reload();
  await page.locator('#draft-note').getByText(/^Draft restored from /).waitFor();
  if(await page.getByLabel('Title',{exact:true}).inputValue()!=='Unsaved thought'||await page.getByLabel('Markdown',{exact:true}).inputValue()!=='Words I would hate to lose.')throw Error('draft not restored');
  await Promise.all([page.waitForNavigation(),page.getByRole('button',{name:'Discard draft'}).click()]);
  if(await page.getByLabel('Title',{exact:true}).inputValue()!==''||await page.locator('#draft-note').isVisible())throw Error('draft not discarded');
  await page.getByLabel('Title',{exact:true}).fill('Saved thought');await page.getByLabel('Markdown',{exact:true}).fill('Kept for good.');await page.waitForTimeout(600);
  await page.getByRole('button',{name:/Save page/}).click();await page.waitForURL(url=>url.pathname!=='/new');
  await page.goto(base+'/new');if(await page.locator('#draft-note').isVisible()||await page.getByLabel('Title',{exact:true}).inputValue()!=='')throw Error('saved page came back as a draft');
  // Drafts of saved pages are kept by page id, last 30 days, and go when the page is deleted in this browser,
  // so a new page that reuses a deleted page's link name never opens with the old draft.
  {
   const d=await browser.newPage({viewport:{width:1280,height:900}});d.on('dialog',dialog=>dialog.accept());
   try{
    const make=title=>d.request.post(base+'/api/pages',{headers:{authorization:'Bearer browser-smoke-token-at-least-32-characters','content-type':'application/json'},data:{title,agent:'ExampleAgent',original_request:'Draft check',slug:'draft-reuse',markdown:'# '+title+'\n\nSaved words.'}});
    if(!(await make('First of the name')).ok())throw Error('draft page not published');
    await d.goto(base+'/edit/draft-reuse');await d.getByLabel('Title',{exact:true}).fill('Unsaved edit');await d.waitForTimeout(600);
    const keys=await d.evaluate(()=>Object.keys(localStorage).filter(k=>k.startsWith('cairn.draft:')));
    if(!keys.some(k=>/^cairn\.draft:page:[0-9a-f]{32}$/.test(k))||keys.some(k=>k.includes('/edit/')))throw Error('edit draft not kept by page id: '+keys);
    const told=await d.evaluate(async()=>{const body=new URLSearchParams({csrf:document.querySelector('[name=csrf]').value,action:'delete'});const r=await fetch('/manage/draft-reuse/delete',{method:'POST',body});return r.ok&&document.cookie.includes('cairn_deleted=');});
    if(!told)throw Error('deleting the page did not tell the browser');
    if(!(await make('Second of the name')).ok())throw Error('reused link name not published');
    await d.goto(base+'/edit/draft-reuse');
    if(await d.locator('#draft-note').isVisible()||await d.getByLabel('Title',{exact:true}).inputValue()!=='Second of the name')throw Error('a deleted page\'s draft filled the new page');
    const left=await d.evaluate(()=>({drafts:Object.keys(localStorage).filter(k=>k.startsWith('cairn.draft:page:')).length,cookie:document.cookie.includes('cairn_deleted=')}));
    if(left.drafts||left.cookie)throw Error('deleted page draft or cookie kept: '+JSON.stringify(left));
    await d.evaluate(()=>localStorage.setItem('cairn.draft:/new',JSON.stringify({title:'Ancient',markdown:'Old words',saved:Date.now()-31*864e5})));
    await d.goto(base+'/new');
    if(await d.locator('#draft-note').isVisible()||await d.evaluate(()=>localStorage.getItem('cairn.draft:/new'))!==null)throw Error('a draft older than 30 days came back');
    // Reading the page in the reader marks it read; an image tag on another site would not.
    const unread=async()=>{const row=(await (await d.request.get(base+'/')).text()).match(/<[a-z]+[^>]*data-slug="draft-reuse"[^>]*>/);if(!row)throw Error('draft-reuse row missing');return row[0].includes('is-unread');};
    if(!await unread())throw Error('a new agent page is not unread');
    await d.goto(base+'/draft-reuse');
    if(await unread())throw Error('opening the reader did not mark the page read');
   }finally{await d.close();}
  }
  // Errors are pages, not text: the owner keeps a way back, visitors see nothing private.
  await page.goto(base+'/no-such-page');await page.getByRole('heading',{name:'This page isn’t in your library'}).waitFor();
  if(!await page.getByRole('link',{name:'Back to library'}).isVisible()||await page.locator('.back-link').count())throw Error('owner 404 has no way back, or two');
  await page.goto(publicBase+'/no-such-page');await page.getByRole('heading',{name:'This link isn’t working'}).waitFor();
  if(await page.locator('a[href="/"]').count()||await page.locator('a[href^="/settings"],a[href^="/manage"]').count())throw Error('visitor 404 links into the library');
  // Reader: copying in place, keyboard paths, pinned header rows below the bar, the end of the page,
  // the phone bar and its menu, the visitor strip and contents without JavaScript.
  {
   const auth={'content-type':'application/json',authorization:'Bearer browser-smoke-token-at-least-32-characters'};
   const bulbs=Array.from({length:20},(_,i)=>`| Bulb ${i+1} | E27 | ${4+i%5} W |`).join('\n'),parts=Array.from({length:6},(_,i)=>`## Part ${i+1}\n\n${'Reading text. '.repeat(80)}`).join('\n\n');
   for(const p of [
    {title:'Reader older',agent:'ExampleAgent',original_request:'PRIVATE-READER-OLDER',slug:'reader-older',markdown:'Older.'},
    {title:'Reader checks',agent:'ExampleAgent',original_request:'PRIVATE-READER-REQUEST',slug:'reader-checks',markdown:'# Reader checks\n\nOpening.\n\n## The list\n\n| Bulb | Fitting | Power |\n|---|---|---:|\n'+bulbs+'\n\n'+parts},
    {title:'Reader newer',agent:'ExampleAgent',original_request:'PRIVATE-READER-NEWER',slug:'reader-newer',markdown:'Newer.'},
   ]){const created=await fetch(base+'/api/pages',{method:'POST',headers:auth,body:JSON.stringify(p)});if(created.status!==201)throw Error(await created.text());}
   const reading=await browser.newContext({viewport:{width:1280,height:900},permissions:['clipboard-read','clipboard-write']});
   try{
    const r=await reading.newPage();const report=()=>r.frames().find(f=>f!==r.mainFrame());
    await r.goto(base+'/reader-checks');await r.frameLocator('.report-frame').locator('table').waitFor();await r.waitForTimeout(500);
    // Copy link says so on the button itself, with no tooltip, and copies the page's address.
    await r.locator('.reader-cover .copy-link').click();
    await r.waitForFunction(()=>document.querySelector('.reader-cover .copy-link').classList.contains('is-copied')&&document.querySelector('#copy-status').textContent.startsWith('Link copied'));
    if(await r.evaluate(()=>navigator.clipboard.readText())!==base+'/reader-checks')throw Error('reader copied the wrong address');
    // From the middle of a freshly opened page Tab reaches the skip link and then the bar, without jumping to the top.
    await r.reload();await r.frameLocator('.report-frame').locator('table').waitFor();await r.waitForTimeout(400);
    await r.mouse.move(640,600);await r.mouse.wheel(0,1800);await r.waitForTimeout(500);
    const y=await r.evaluate(()=>scrollY),stops=[];
    for(let i=0;i<3;i++){await r.keyboard.press('Tab');stops.push(await r.evaluate(()=>document.activeElement.closest('.compact-reader')?'bar':document.activeElement.className));}
    if(await r.evaluate(()=>scrollY)!==y||stops.join()!=='skip-link,bar,bar')throw Error('Tab from mid-page: '+stops.join());
    // The reader tells the report which part is in view, so a long table's header row stays below the bar.
    const tableTop=await report().evaluate(()=>document.querySelector('.table-scroll table').getBoundingClientRect().top+scrollY);
    await r.evaluate(top=>scrollTo(0,document.querySelector('.report-frame').getBoundingClientRect().top+scrollY+top+200),tableTop);
    await report().waitForFunction(()=>parseFloat(document.querySelector('.table-scroll table').style.getPropertyValue('--pin'))>0);
    // Page details take focus; Escape gives it back to the title.
    await r.locator('.compact-title').focus();await r.keyboard.press('Enter');
    if(await r.evaluate(()=>document.activeElement.id)!=='reader-details')throw Error('page details did not take focus');
    await r.keyboard.press('Escape');
    if(!await r.evaluate(()=>document.activeElement.classList.contains('compact-title')))throw Error('focus did not return to the bar');
    // Back at the top with focus in the bar, the bar and the focus stay.
    await r.evaluate(()=>scrollTo(0,0));await r.waitForTimeout(400);
    if(await r.evaluate(()=>document.activeElement===document.body||!document.querySelector('.compact-reader').classList.contains('is-visible')))throw Error('focus dropped when the bar went away');
    // The end of the page leads to the neighbours in library order.
    await r.evaluate(()=>scrollTo(0,document.documentElement.scrollHeight));await r.waitForTimeout(400);
    const end=await r.evaluate(()=>({visible:getComputedStyle(document.querySelector('.reader-end')).visibility,prev:document.querySelector('.end-page.is-previous')?.getAttribute('href'),next:document.querySelector('.end-page.is-next')?.getAttribute('href')}));
    if(end.visible!=='visible'||end.prev!=='/reader-newer'||end.next!=='/reader-older')throw Error('end of page: '+JSON.stringify(end));
    // ? lists the shortcuts and ] opens the next page.
    await r.keyboard.press('?');await r.locator('#reader-shortcuts').getByText('Previous or next page in your library').waitFor();await r.keyboard.press('Escape');
    await Promise.all([r.waitForURL('**/reader-older'),r.keyboard.press(']')]);
    // Archive at the end of the page returns to the library.
    await r.waitForTimeout(400);await r.evaluate(()=>scrollTo(0,document.documentElement.scrollHeight));
    await Promise.all([r.waitForURL(u=>new URL(u).pathname==='/'),r.getByRole('button',{name:'Archive page'}).click()]);
    await r.goto(base+'/reader-older');await r.getByRole('button',{name:'Restore page'}).waitFor();
    // A tablet: C opens the contents with focus in them, and walking out with Tab closes them.
    await r.setViewportSize({width:820,height:1000});await r.goto(base+'/reader-checks');await r.waitForTimeout(600);
    await r.evaluate(()=>scrollTo(0,1500));await r.waitForTimeout(400);await r.keyboard.press('c');
    if(!await r.evaluate(()=>document.body.classList.contains('contents-open')&&!!document.activeElement.closest('.reader-toc')))throw Error('C did not open the contents with focus in them');
    for(let i=0;i<20&&await r.evaluate(()=>document.body.classList.contains('contents-open'));i++){await r.keyboard.press('Tab');await r.waitForTimeout(40);}
    if(await r.evaluate(()=>document.body.classList.contains('contents-open')))throw Error('Tab walked out of the open contents');
   }finally{await reading.close();}
   // A phone: the section's name opens the contents and everything else sits in one labelled menu.
   const handheld=await browser.newContext({viewport:{width:390,height:844},hasTouch:true,isMobile:true,permissions:['clipboard-read','clipboard-write']});
   try{
    const m=await handheld.newPage();await m.goto(base+'/reader-checks');await m.waitForTimeout(600);
    await m.evaluate(()=>scrollTo(0,2000));await m.waitForTimeout(500);
    const shown=await m.evaluate(()=>[...document.querySelectorAll('.compact-reader a,.compact-reader button')].filter(e=>e.offsetParent).map(e=>[e.className.split(' ')[0],Math.round(e.getBoundingClientRect().height)]));
    if(shown.map(s=>s[0]).join()!=='brand,compact-title,compact-more'||shown.some(s=>s[1]<44))throw Error('phone bar: '+JSON.stringify(shown));
    await m.locator('.compact-title').tap();
    if(!await m.evaluate(()=>document.body.classList.contains('contents-open')))throw Error('the section name did not open the contents');
    await m.keyboard.press('Escape');
    await m.locator('.compact-more').tap();const menu=m.locator('#reader-menu');
    for(const name of ['Copy link','Page details','Manage page'])if(!await menu.getByText(name,{exact:true}).isVisible())throw Error('phone menu lacks '+name);
    await menu.locator('.menu-copy').tap();await m.waitForFunction(()=>document.querySelector('.menu-copy').classList.contains('is-copied'));
   }finally{await handheld.close();}
   // Visitors get a slim strip with a way to copy the link, and nothing private.
   const token=(await (await fetch(base+'/settings')).text()).match(/name="csrf" value="([^"]+)"/)[1];
   await fetch(base+'/manage/reader-checks',{method:'POST',redirect:'manual',headers:{origin:base,'content-type':'application/x-www-form-urlencoded'},body:new URLSearchParams({csrf:token,action:'share'})});
   const visit=await browser.newContext({viewport:{width:1280,height:900}});
   try{
    const v=await visit.newPage();await v.goto(publicBase+'/reader-checks');await v.frameLocator('.report-frame').locator('table').waitFor();await v.locator('body.report-settled').waitFor();
    if(!await v.locator('.reader-strip .copy-link').isVisible()||await v.locator('#reader-menu,.access-status,.end-pages').count()||(await v.content()).includes('PRIVATE-READER')||!await v.getByText('Shared with Cairn').isVisible())throw Error('visitor strip shows the wrong things');
   }finally{await visit.close();}
   // Without JavaScript the contents open from the cover and the request shows in full.
   const plainReader=await browser.newContext({javaScriptEnabled:false,viewport:{width:390,height:844}});
   try{
    const p=await plainReader.newPage();await p.goto(base+'/reader-checks');
    await p.locator('.contents-fallback summary').click();
    const first=p.locator('.contents-fallback a').first();
    if(!await first.isVisible()||await first.getAttribute('target')!=='report'||!await p.locator('#report-request-text').getByText('PRIVATE-READER-REQUEST').isVisible()||!await p.locator('.reader-end').isVisible())throw Error('no-JavaScript reader');
   }finally{await plainReader.close();}
  }
  // Minimal uses the device's own fonts, so it never downloads DM Sans; Cairn does.
  const fontRequests=async()=>{
   const fresh=await browser.newContext();const fonts=[];
   // Settings too: under Minimal its theme cards preview the other themes in the device's sans.
   try{const p=await fresh.newPage();p.on('request',r=>{if(r.url().endsWith('.woff2'))fonts.push(r.url())});for(const route of ['/','/new','/import','/manage/report','/settings','/report',publicBase+'/report']){await p.goto(route.startsWith('http')?route:base+route);await p.evaluate(()=>document.fonts.ready);}}finally{await fresh.close();}
   return fonts;
  };
  await setThemes('minimal');
  if((await fontRequests()).length)throw Error('Minimal downloaded DM Sans');
  await setThemes('cairn');
  if(!(await fontRequests()).length)throw Error('Cairn did not load DM Sans');
  {
   // Dates follow the browser's clock, not the server's: two time zones a day apart show the same
   // instant differently, each in 24-hour time.
   const titles=[];
   for(const timezoneId of ['Pacific/Kiritimati','Pacific/Pago_Pago']){
    const ctx=await browser.newContext({timezoneId});
    try{
     const p=await ctx.newPage();await p.goto(base+'/');
     const seen=await p.evaluate(()=>{const t=document.querySelector('time.date[data-local]');const d=new Date(t.getAttribute('datetime'));const pad=n=>String(n).padStart(2,'0');return {title:t.title,want:' at '+pad(d.getHours())+':'+pad(d.getMinutes()),spoken:t.querySelector('.sr-only').textContent};});
     if(!seen.title.endsWith(seen.want)||!seen.spoken.endsWith(seen.want)||/AM|PM/.test(seen.title))throw Error('date not in the browser clock: '+JSON.stringify(seen));
     titles.push(seen.title);
    }finally{await ctx.close();}
   }
   if(titles[0]===titles[1])throw Error('dates ignore the browser time zone: '+titles[0]);
  }
  {
   // Touch screens: every title link and choice radio is a 44px target, and fields are large enough
   // that iOS does not zoom into them.
   const ctx=await browser.newContext({viewport:{width:390,height:844},hasTouch:true,isMobile:true,deviceScaleFactor:2});
   try{
    const p=await ctx.newPage();
    const tall=async(selector,what)=>{const boxes=await p.locator(selector).evaluateAll(els=>els.filter(el=>el.getClientRects().length).map(el=>{const r=el.getBoundingClientRect();return [Math.round(r.width),Math.round(r.height)];}));if(!boxes.length||boxes.some(([w,h])=>w<44-.5||h<44-.5))throw Error(what+' under 44px on touch: '+JSON.stringify(boxes.slice(0,4)));};
    await p.goto(base+'/');await tall('.row-title','library title link');
    const size=async(selector)=>parseFloat(await p.locator(selector).first().evaluate(el=>getComputedStyle(el).fontSize));
    if(await size('#search')<16)throw Error('library search zooms on iOS');
    await p.goto(base+'/new');await p.locator('#access-trigger').click();await tall('.access-choice input[type=radio]','access radio');
    await p.goto(base+'/settings');await tall('.theme-name input[type=radio]','theme radio');
    if(await size('input[name=author]')<16)throw Error('settings field zooms on iOS');
    await p.goto(base+'/manage/report');if(await size('#link-name')<16)throw Error('manage field zooms on iOS');
   }finally{await ctx.close();}
   // With a mouse, fields keep the body size.
   const desk=await browser.newPage({viewport:{width:1280,height:900}});
   try{await desk.goto(base+'/settings');if(await desk.locator('input[name=author]').evaluate(el=>getComputedStyle(el).fontSize)!=='15px')throw Error('desktop field size changed');}finally{await desk.close();}
  }
  {
   // The reader and its reports, as of the reader polish: one grid for every page type, contents that
   // never float over a page in its own design or slice through an entry, the section the bar names,
   // keys that respect the owner's setting and show where they put focus, diagrams fetched only when
   // they come near, what a report's own script is allowed to tell the reader, and text alternatives
   // for charts and diagrams.
   const auth={'content-type':'application/json',authorization:'Bearer browser-smoke-token-at-least-32-characters'};
   const publish=async p=>{const r=await fetch(base+'/api/pages',{method:'POST',headers:auth,body:JSON.stringify(p)});if(r.status!==201)throw Error(p.slug+' '+await r.text());};
   const rows=Array.from({length:8},(_,i)=>'| Lamp '+(i+1)+' | E27 | '+(5+i)+' W | 806 lm | 2700 K | Yes | Two years | Needs a hub to dim on the old circuit |').join('\n');
   const chain=Array.from({length:9},(_,i)=>`  S${i}[Step ${i+1} of the work] --> S${i+1}`).join('\n');
   const words=n=>'A sentence of reading text that fills the line. '.repeat(n);
   await publish({title:'Lamps compared side by side',agent:'ExampleAgent',original_request:'Compare the lamps.',slug:'lamps-wide',template:'comparison',markdown:'# Lamps compared side by side\n\nSix lamps, one table.\n\n## Before the table\n\n'+words(30)+'\n\n## The table\n\n| Lamp | Fitting | Power | Brightness | Colour | Dimmable | Warranty | Notes |\n|---|---|---:|---:|---|---|---|---|\n'+rows+'\n\n## After the table\n\n'+words(100)+'\n\n## Which to buy\n\nLamp 3.\n\n## Why\n\nIt dims without a hub.'});
   await publish({title:'Rail beside a wide diagram',agent:'ExampleAgent',original_request:'R',slug:'rail-wide',markdown:'# Rail beside a wide diagram\n\n'+Array.from({length:14},(_,i)=>`## Section ${i+1}\n\n${words(i===3?60:8)}`).join('\n\n')+'\n\n## The order of work\n\n```mermaid\nflowchart LR\n'+chain+'\n```\n\n'+words(220)+'\n\n## Last\n\nEnd.'});
   await publish({title:'A page that runs on and on so the bar has to choose between its title and the section being read',agent:'ExampleAgent',original_request:'R',slug:'long-names',markdown:'# Long names\n\n'+Array.from({length:4},(_,i)=>`## A section heading long enough to fill most of the bar on its own, part ${i+1}\n\n${words(40)}`).join('\n\n')});
   await publish({title:'Diagram far down',agent:'ExampleAgent',original_request:'R',slug:'diagram-late',markdown:'# Diagram far down\n\n'+words(400)+'\n\n## The drawing\n\n```mermaid\nflowchart LR\n  A[Read] --> B[Draw]\n```\n\nAfter.'});
   const ctx=await browser.newContext({viewport:{width:1440,height:900}});
   const p=await ctx.newPage();
   const brand=()=>p.evaluate(()=>Math.round(document.querySelector('.reader-cover .brand').getBoundingClientRect().left));
   try{
    // One grid: the brand stands still across a report, a short page, a comparison and a custom page.
    for(const width of [1440,390]){
     await p.setViewportSize({width,height:900});const seen={};
     for(const slug of ['reader-checks','reader-older','lamps-wide','flowing']){await p.goto(base+'/'+slug);await p.waitForTimeout(300);seen[slug]=await brand();}
     if(new Set(Object.values(seen)).size!==1)throw Error('brand moves between page types at '+width+': '+JSON.stringify(seen));
    }
    // A page in its own design gets the neutral strip, and its contents never float over it, at any width.
    for(const width of [1440,1920]){
     await p.setViewportSize({width,height:900});await p.goto(base+'/flowing');await p.waitForTimeout(800);
     const custom=await p.evaluate(()=>({strip:!!document.querySelector('header.owner-strip')&&!document.querySelector('.blue-hour'),floating:document.body.classList.contains('toc-floating'),shown:getComputedStyle(document.querySelector('.reader-toc')).display}));
     if(!custom.strip||custom.floating||custom.shown!=='none')throw Error('custom page at '+width+': '+JSON.stringify(custom));
    }
    await p.locator('.reader-cover .more-button').click();await p.locator('#reader-menu .menu-contents').click();
    if(!await p.evaluate(()=>document.body.classList.contains('contents-open')&&!!document.activeElement.closest('.reader-toc')))throw Error('the More menu did not open the contents on a custom page');
    await p.keyboard.press('Escape');
    // Comparisons use the full width, with no rail beside the table.
    await p.setViewportSize({width:1440,height:900});await p.goto(base+'/lamps-wide');await p.waitForTimeout(900);
    const comparison=await p.frames().find(f=>f!==p.mainFrame()).evaluate(()=>{const s=document.querySelector('.table-scroll');return {width:Math.round(document.querySelector('.table-frame').getBoundingClientRect().width),scrolls:s.scrollWidth>s.clientWidth+1};});
    if(await p.evaluate(()=>document.body.classList.contains('toc-floating'))||comparison.width<1100||comparison.scrolls)throw Error('comparison at 1440: '+JSON.stringify(comparison));
    // The rail on a report keeps its place, ends between entries above a wide diagram, steps aside as it
    // passes, and comes back.
    await p.goto(base+'/rail-wide');await p.locator('body.toc-floating').waitFor();
    const frame=()=>p.frames().find(f=>f!==p.mainFrame());
    for(let y=0;y<6000;y+=500){await p.evaluate(y=>scrollTo(0,y),y);await p.waitForTimeout(60);}
    await frame().locator('.diagram svg').waitFor();
    const frameTop=await p.evaluate(()=>document.querySelector('.report-frame').getBoundingClientRect().top+scrollY);
    const diagramTop=await frame().evaluate(()=>document.querySelector('figure.diagram').getBoundingClientRect().top+scrollY);
    const sliced=()=>p.evaluate(()=>{const panel=document.querySelector('.toc-panel').getBoundingClientRect();return [...document.querySelectorAll('.toc-panel a')].map(a=>a.getBoundingClientRect()).some(r=>r.top<panel.bottom-1&&r.bottom>panel.bottom+1);});
    let stillThere=false;
    for(const before of [700,560,420,300,200]){
     await p.evaluate(y=>scrollTo(0,y),frameTop+diagramTop-before);await p.waitForTimeout(400);
     const rail=await p.evaluate(()=>({bottom:document.querySelector('.toc-panel').getBoundingClientRect().bottom,yield:document.body.classList.contains('toc-yield'),frame:document.querySelector('.report-frame').getBoundingClientRect().top}));
     if(rail.yield)continue;
     stillThere=true;
     if(rail.bottom>rail.frame+diagramTop||await sliced())throw Error('rail runs into or slices beside the coming diagram at '+before+': '+JSON.stringify(rail));
    }
    if(!stillThere)throw Error('rail left too early');
    await p.evaluate(y=>scrollTo(0,y),frameTop+diagramTop-60);await p.waitForTimeout(500);
    if(!await p.evaluate(()=>document.body.classList.contains('toc-yield')&&getComputedStyle(document.querySelector('.reader-toc')).visibility==='hidden'))throw Error('rail did not step aside for the wide diagram');
    await p.evaluate(()=>scrollTo(0,document.documentElement.scrollHeight-1600));await p.waitForTimeout(500);
    if(await p.evaluate(()=>document.body.classList.contains('toc-yield')))throw Error('rail did not come back after the diagram');
    // C focuses the rail entry and its ring shows, as Tab's would.
    await p.goto(base+'/reader-checks');await p.locator('body.toc-floating').waitFor();await p.keyboard.press('c');
    const ring=await p.evaluate(()=>({inRail:!!document.activeElement.closest('.reader-toc'),outline:getComputedStyle(document.activeElement).outlineStyle,modality:document.documentElement.dataset.inputModality}));
    if(!ring.inRail||ring.outline==='none'||ring.modality!=='keyboard')throw Error('C moved focus without a ring: '+JSON.stringify(ring));
    // Never two truncations in the bar: the section wins when both do not fit.
    await p.setViewportSize({width:1280,height:800});await p.goto(base+'/long-names');await p.waitForTimeout(600);await p.evaluate(()=>scrollTo(0,1500));await p.waitForTimeout(600);
    const bar=await p.evaluate(()=>{const cut=e=>{if(e.offsetParent===null||getComputedStyle(e).display==='none')return false;const r=document.createRange();r.selectNodeContents(e);return r.getBoundingClientRect().right>e.getBoundingClientRect().right+0.5;};return {page:cut(document.querySelector('.compact-page')),section:cut(document.querySelector('.compact-section')),text:document.querySelector('.compact-section').textContent};});
    if((bar.page&&bar.section)||!bar.text)throw Error('bar truncates twice: '+JSON.stringify(bar));
    // A report's claims are checked: it cannot restyle the rail or move the current section with
    // headings the page does not have.
    await p.setViewportSize({width:1280,height:900});await p.goto(base+'/reader-checks');await p.waitForTimeout(800);
    const rail=()=>p.evaluate(()=>({toc:getComputedStyle(document.querySelector('.toc-panel')).color,font:getComputedStyle(document.querySelector('.toc-panel')).fontFamily,current:document.querySelector('.reader-toc a[aria-current]')?.dataset.id||''}));
    const before=await rail();
    await frame().evaluate(()=>{parent.postMessage({type:'cairn:layout',lead:40,wide:[],ink:'rgb(255, 0, 0)',paper:'red',accent:'red',font:'Comic Sans MS'},'*');parent.postMessage({type:'cairn:headings',items:[['made-up',0],['part-1',999999]]},'*');});
    await p.waitForTimeout(600);
    const after=await rail();
    if(JSON.stringify(after)!==JSON.stringify(before))throw Error('a report restyled the rail or moved the section: '+JSON.stringify({before,after}));
    // Only a real click or key press inside the report scrolls the reader: a report's own script cannot.
    let sections='';for(let i=1;i<=6;i++)sections+=`<h2 id="h${i}">Part ${i}</h2><p>${'Reading text. '.repeat(150)}</p>`;
    await publish({title:'Pushy report',agent:'ExampleAgent',original_request:'R',slug:'pushy',html:'<h1>Pushy report</h1>'+sections+'<script>addEventListener("load",()=>setTimeout(()=>{parent.postMessage({type:"cairn:scroll-to",top:3000},"*");document.body.dataset.sent="yes";},1200))</script>'});
    await p.goto(base+'/pushy');await frame().waitForFunction(()=>document.body.dataset.sent==='yes');await p.waitForTimeout(500);
    if(await p.evaluate(()=>scrollY)!==0)throw Error('a report scrolled the reader without a click');
    // A height the page could not need ends the full-height frame; the report scrolls itself instead.
    await frame().evaluate(()=>parent.postMessage({type:'cairn:report-height',height:199999},'*'));await p.waitForTimeout(400);
    const tall=await p.evaluate(()=>({height:document.querySelector('.report-frame').offsetHeight,flowing:document.body.classList.contains('flowing-report'),limit:+document.body.dataset.reportLimit}));
    if(tall.flowing||tall.height>1000||!(tall.limit>0&&tall.limit<199999))throw Error('a report stretched the reader: '+JSON.stringify(tall));
    // Mermaid is fetched only when a diagram comes near what is being read.
    const fetched=[];p.on('request',r=>{if(/\/_runtime\/mermaid\.js/.test(r.url()))fetched.push(r.url());});
    await p.goto(base+'/diagram-late');await p.waitForTimeout(1500);
    if(fetched.length)throw Error('Mermaid loaded before its diagram came near');
    if(await frame().evaluate(()=>!document.querySelector('figure.diagram[data-pending]')))throw Error('the diagram is not waiting in place');
    for(let y=0,end=await p.evaluate(()=>document.documentElement.scrollHeight);y<end;y+=400){await p.evaluate(y=>scrollTo(0,y),y);await p.waitForTimeout(80);}
    await frame().locator('.diagram svg').waitFor();
    if(fetched.length!==1||!/\?v=[0-9a-f]{12}$/.test(fetched[0]))throw Error('Mermaid fetched '+JSON.stringify(fetched));
    // Charts and diagrams carry text alternatives: a sentence and the chart's data as a table, the
    // diagram's steps and links.
    await p.goto(base+'/interactive');await frame().locator('.diagram svg').waitFor();await frame().locator('.chart-canvas canvas').waitFor();
    const alt=await frame().evaluate(()=>{const chart=document.querySelector('.chart-canvas'),diagram=document.querySelector('.diagram-scroll'),table=document.querySelector('.chart-data table');return {chart:document.getElementById(chart.getAttribute('aria-describedby'))?.textContent,rows:table?[...table.rows].map(r=>[...r.cells].map(c=>c.textContent).join('|')):[],caption:table?.caption?.textContent,diagram:document.getElementById(diagram.getAttribute('aria-describedby'))?.textContent,hidden:table?table.parentElement.getBoundingClientRect().width<=1:false};});
    if(alt.chart!=='Bar chart. Its data follows as a table.'||alt.rows.join()!=='|Value,A|2,B|5'||alt.caption!=='Data for Interactive report'&&!/^Data for /.test(alt.caption||'')||!/^Flowchart\. Steps: Chat; Page\. Links: Chat to Page\.$/.test(alt.diagram||'')||!alt.hidden)throw Error('text alternatives: '+JSON.stringify(alt));
   }finally{await ctx.close();}
   // On a phone the bar names the section being read: not the next one while the last still fills the screen.
   const phone=await browser.newContext({viewport:{width:390,height:844},hasTouch:true,isMobile:true});
   try{
    const m=await phone.newPage();await m.goto(base+'/reader-checks');await m.waitForTimeout(800);
    const frame=m.frames().find(f=>f!==m.mainFrame());
    const frameTop=await m.evaluate(()=>document.querySelector('.report-frame').getBoundingClientRect().top+scrollY);
    const part=async n=>frameTop+await frame.evaluate(n=>document.getElementById('part-'+n).getBoundingClientRect().top+scrollY,n);
    const named=async y=>{await m.evaluate(y=>scrollTo(0,y),y);await m.waitForTimeout(400);return m.evaluate(()=>document.querySelector('.compact-section').textContent);};
    if(await named(await part(3)-400)!=='Part 2')throw Error('the bar ran ahead of the section being read');
    if(await named(await part(3)-40)!=='Part 3')throw Error('the bar did not reach the section at the top');
    if(await named(await m.evaluate(()=>document.documentElement.scrollHeight))!=='Part 6')throw Error('the last section was not named at the end');
   }finally{await phone.close();}
   // With shortcuts off in Settings, the reader leaves single keys alone.
   const keys=await browser.newContext({viewport:{width:1280,height:900}});
   try{
    const k=await keys.newPage();await k.goto(base+'/settings');
    const csrf=await k.locator('input[name=csrf]').first().inputValue();
    await k.evaluate(async csrf=>{await fetch('/settings',{method:'POST',body:new URLSearchParams({csrf,action:'shortcuts',shortcuts:'off'})});},csrf);
    await k.goto(base+'/reader-checks');await k.waitForTimeout(600);
    for(const key of ['c','?',']'])await k.keyboard.press(key);await k.waitForTimeout(300);
    if(await k.evaluate(()=>document.documentElement.dataset.shortcuts!=='off'||location.pathname!=='/reader-checks'||document.querySelector('#reader-shortcuts').matches(':popover-open')||document.activeElement.closest('.reader-toc')))throw Error('a single key acted with shortcuts off');
    await k.goto(base+'/settings');await k.evaluate(async csrf=>{await fetch('/settings',{method:'POST',body:new URLSearchParams({csrf,action:'shortcuts',shortcuts:'on'})});},csrf);
   }finally{await keys.close();}
   // In the library's preview drawer, keys pressed inside the page still step and close the drawer.
   const drawer=await browser.newContext({viewport:{width:1280,height:900}});
   try{
    const d=await drawer.newPage();await d.goto(base+'/');
    await d.locator('.report-row .row-title').first().click();await d.locator('#preview[open]').waitFor();
    const inside=d.frameLocator('#preview-frame');await inside.locator('body').waitFor();await d.waitForTimeout(700);
    const first=await d.locator('#preview-title').textContent();
    await inside.locator('body').click({position:{x:12,y:12}});await d.keyboard.press('j');
    await d.waitForFunction(t=>document.querySelector('#preview-title').textContent!==t,first);
    await d.waitForTimeout(700);await inside.locator('body').click({position:{x:12,y:12}});
    if(!await d.evaluate(()=>document.activeElement===document.querySelector('#preview-frame')))throw Error('a click in the preview did not put focus in it');
    await d.keyboard.press('Escape');await d.waitForFunction(()=>!document.querySelector('#preview').open);
   }finally{await drawer.close();}
   // Reduced motion: nothing moves; the bar only fades, briefly.
   const still=await browser.newContext({viewport:{width:1280,height:900},reducedMotion:'reduce'});
   try{
    const r=await still.newPage();await r.goto(base+'/reader-checks');await r.waitForTimeout(500);
    const motion=await r.evaluate(()=>{const s=getComputedStyle(document.querySelector('.compact-reader'));return {property:s.transitionProperty,duration:s.transitionDuration};});
    if(/transform/.test(motion.property)||!/opacity/.test(motion.property)||motion.duration.split(',').some(d=>parseFloat(d)>0.2))throw Error('reduced motion: '+JSON.stringify(motion));
   }finally{await still.close();}
  }
  {
   // Owner surfaces (p7c): one way to show errors, toasts that never move the page, the settings
   // save model, the shortcut setting, a phone library that starts in the first screen, and no
   // sideways scrolling anywhere at 320px.
   const api=(body)=>fetch(base+'/api/pages',{method:'POST',headers:{'content-type':'application/json',authorization:'Bearer browser-smoke-token-at-least-32-characters'},body:JSON.stringify(body)});
   const lum=c=>{const v=c.match(/[\d.]+/g).map(Number);const f=x=>{x/=255;return x<=.03928?x/12.92:((x+.055)/1.055)**2.4;};return .2126*f(v[0])+.7152*f(v[1])+.0722*f(v[2]);};
   const ratio=(a,b)=>{const x=lum(a),y=lum(b);return (Math.max(x,y)+.05)/(Math.min(x,y)+.05);};
   const long='Household admin and bills research assistant';
   if(long.length!==44)throw Error('the long author name must be 44 characters');
   if(!(await api({title:'Council tax band check',agent:long,original_request:'is our band right',slug:'council-tax',markdown:'With the £7,500 difference in valuation an appeal is worth it.'})).ok)throw Error('long author page');
   const o=await browser.newContext({viewport:{width:1280,height:900}});
   try{
    const p=await o.newPage();p.setDefaultTimeout(10000);
    // Every owner form checks itself: novalidate, no browser bubble, the error beside its field.
    for(const [route,selector] of [['/new','#editor-form'],['/import','form.stacked-form'],['/manage/report','.title-form'],['/manage/report','#sharing-form'],['/settings','#writing-form'],['/settings','#kindle-form'],['/settings','#connect-agent']]){
     await p.goto(base+route);
     if(!await p.locator(selector).evaluate(f=>f.noValidate))throw Error(route+' '+selector+' can show browser bubbles');
    }
    await p.goto(base+'/import');await p.getByRole('button',{name:'Save private page'}).click();
    await p.locator('#document-error').getByText('Choose a file to import.').waitFor();
    if(new URL(p.url()).pathname!=='/import'||await p.evaluate(()=>document.activeElement.id)!=='document'||await p.locator('#document').getAttribute('aria-invalid')!=='true'||!(await p.locator('#document').getAttribute('aria-describedby')).startsWith('document-error'))throw Error('import did not show its error in place');
    await p.goto(base+'/manage/report');await p.locator('#share-password').fill('short');await p.getByRole('button',{name:'Save sharing'}).click();
    await p.locator('#share-password-error').getByText('Use at least 8 characters.').waitFor();
    if(await p.evaluate(()=>document.activeElement.id)!=='share-password')throw Error('short password did not keep focus on its field');
    await p.goto(base+'/new');await p.getByRole('button',{name:/^Save/}).click();
    const editorErrors=await p.evaluate(()=>{const after=(field,error)=>!!(document.getElementById(field).compareDocumentPosition(document.getElementById(error))&Node.DOCUMENT_POSITION_FOLLOWING);return {title:after('page-title','page-title-error'),body:after('markdown','markdown-error'),colours:getComputedStyle(document.getElementById('page-title-error')).color===getComputedStyle(document.getElementById('markdown-error')).color,rails:getComputedStyle(document.getElementById('page-title')).borderLeftColor===getComputedStyle(document.getElementById('markdown')).borderLeftColor};});
    if(Object.values(editorErrors).includes(false))throw Error('editor errors are not under their fields alike: '+JSON.stringify(editorErrors));
    // An invalid link name never previews as an address.
    await p.locator('#access-trigger').click();await p.locator('[name=access][value=public]').check({force:true});await p.locator('#page-slug').fill('kitchen plan!');
    await p.locator('#page-slug-error').waitFor();if((await p.locator('#address-full').innerText()).includes('kitchen plan!'))throw Error('an invalid link name shows as an address');
    // The panel re-places itself on the next frame when the button's words change.
    const aligned=await p.waitForFunction(()=>{const a=document.querySelector('#sharing-panel').getBoundingClientRect(),b=document.querySelector('#access-trigger').getBoundingClientRect();return Math.abs(a.right-b.right)<=1;},null,{timeout:2000}).then(()=>true,()=>false);
    if(!aligned)throw Error('the access panel does not line up with its button');
    await p.keyboard.press('Escape');
    // The writing is coloured by a copy behind the textarea that wraps exactly as the textarea does.
    await p.locator('#markdown').fill('# Heading\n\nA **long** line with a [link](https://example.com "Title") and `code` that runs on well past the edge of the column so it has to wrap.\n\n```sh\ncairn publish\n```\n');
    await p.waitForFunction(()=>document.querySelector('#markdown-field').classList.contains('is-highlighted')&&!!document.querySelector('#markdown-highlight .md-heading'));
    const layer=await p.evaluate(()=>{const l=document.querySelector('#markdown-highlight'),t=document.querySelector('#markdown'),a=l.getBoundingClientRect(),b=t.getBoundingClientRect();return {text:l.textContent.replace(/\u200b/g,'')===t.value,x:a.x===b.x,width:a.width===b.width,height:Math.abs(l.scrollHeight-t.scrollHeight)<=2,heading:!!l.querySelector('.md-heading'),link:!!l.querySelector('.md-link'),code:!!l.querySelector('.md-code'),clear:getComputedStyle(t).webkitTextFillColor};});
    if(!layer.text||!layer.x||!layer.width||!layer.height||!layer.heading||!layer.link||!layer.code||layer.clear!=='rgba(0, 0, 0, 0)')throw Error('the writing colours do not line up: '+JSON.stringify(layer));
    // Formatting help is a reference sheet that names every kind of block.
    await p.getByRole('button',{name:'Formatting help'}).click();
    for(const name of ['Tables','Callouts','Footnotes','Images','Diagrams','Charts'])await p.locator('#format-help').getByRole('heading',{name}).waitFor();
    await p.keyboard.press('Escape');
    // Toasts: one live region from the start whose text changes; the page never moves.
    await p.goto(base+'/');await p.evaluate(()=>scrollTo(0,120));
    const live=await p.evaluate(()=>{const el=document.getElementById('toast-live');el.dataset.mark='first';return el.textContent;});
    const before=await p.evaluate(()=>({y:scrollY,top:document.querySelector('.sheet').getBoundingClientRect().top}));
    await p.locator('.report-row .row-title').first().focus();await p.keyboard.press('e');await p.locator('.toast').waitFor();
    await p.waitForFunction(()=>document.getElementById('toast-live').textContent.startsWith('Archived'));
    const after=await p.evaluate(()=>({y:scrollY,top:document.querySelector('.sheet').getBoundingClientRect().top,same:document.getElementById('toast-live').dataset.mark==='first',regions:document.querySelectorAll('[role=status]#toast-live').length,fixed:getComputedStyle(document.querySelector('.toasts')).position}));
    if(live!==''||after.y!==before.y||after.top!==before.top||!after.same||after.regions!==1||after.fixed!=='fixed')throw Error('the toast moved the page or replaced its live region: '+JSON.stringify({before,after}));
    // Hovering holds it; the close button dismisses it; Undo still works with the keyboard.
    await p.locator('.toast').hover();await p.waitForTimeout(400);
    await p.locator('.toast').getByRole('button',{name:'Undo'}).focus();await p.keyboard.press('Enter');await p.locator('.toast').getByText(/^Restored/).waitFor();
    await p.locator('.toast-close').click();await p.locator('.toast').waitFor({state:'detached'});
    // The preview drawer starts on the page's title, not on its close button.
    await p.locator('.row-title').first().click();await p.locator('#preview[open]').waitFor();
    if(await p.evaluate(()=>document.activeElement.id)!=='preview-title')throw Error('the drawer did not start on the title');
    await p.keyboard.press('Escape');
    // Settings: fields save themselves with a tick and a toast; no Save buttons with JavaScript.
    await p.goto(base+'/settings');
    if(await p.getByRole('button',{name:'Save defaults'}).isVisible()||await p.getByRole('button',{name:'Save theme'}).isVisible())throw Error('settings shows Save buttons although fields save themselves');
    await p.locator('#link-length').fill('4');await p.keyboard.press('Tab');
    await p.locator('.save-state[data-for="link-length"].is-saved').waitFor();await p.locator('.toast').getByText('Defaults saved.').waitFor();
    if(new URL(p.url()).pathname!=='/settings'||!(await (await fetch(base+'/settings')).text()).includes('name="link_length" min="3" max="7" inputmode="numeric" value="4"'))throw Error('link length did not save in place');
    await p.locator('#link-length').fill('9');await p.keyboard.press('Tab');await p.locator('#link-length-error').getByText('Choose a length from 3 to 7 characters.').waitFor();
    await p.locator('#link-length').fill('3');await p.keyboard.press('Tab');await p.locator('.toast').getByText('Defaults saved.').waitFor();
    // A Kindle address that isn't Amazon's is kept, with a warning.
    await p.locator('#kindle-address').fill('chris@gmail.com');await p.keyboard.press('Tab');
    await p.locator('#kindle-address-warning').getByText(/@kindle\.com or @free\.kindle\.com/).waitFor();
    await p.locator('#kindle-address').fill('reader@kindle.com');await p.keyboard.press('Tab');await p.locator('#kindle-address-warning').waitFor({state:'hidden'});
    // The default author's own row offers no key, and says why.
    const own=p.locator('.person-row',{has:p.locator('.key-status.is-own')});
    if(!await own.count()||await own.locator('.manage-key').count())throw Error('the owner row offers a key');
    // Initials take a tint from the name: stable, different for different names, readable.
    const tints=await p.evaluate(()=>[...document.querySelectorAll('.settings-avatar[class*="tint-"]')].map(el=>({name:el.closest('.person-row').querySelector('.person-name').textContent,tint:[...el.classList].find(c=>c.startsWith('tint-')),fg:getComputedStyle(el).color,bg:getComputedStyle(el).backgroundColor})));
    if(new Set(tints.map(t=>t.tint)).size<2)throw Error('avatar tints do not vary: '+JSON.stringify(tints));
    for(const t of tints)if(ratio(t.fg,t.bg)<4.5)throw Error('avatar tint fails contrast: '+JSON.stringify(t));
    await p.reload();
    const again=await p.evaluate(()=>[...document.querySelectorAll('.settings-avatar[class*="tint-"]')].map(el=>[...el.classList].find(c=>c.startsWith('tint-'))));
    if(again.join()!==tints.map(t=>t.tint).join())throw Error('avatar tints changed on reload');
    // The shortcut setting: off, single keys do nothing in the library; on again, they work.
    await p.locator('#shortcuts').uncheck();await p.locator('.toast').getByText(/Keyboard shortcuts are off/).waitFor();
    if(await p.locator('html').getAttribute('data-shortcuts')!=='off')throw Error('the page kept its shortcuts');
    await p.goto(base+'/');if(await p.locator('html').getAttribute('data-shortcuts')!=='off')throw Error('the library does not know shortcuts are off');
    await p.locator('body').click({position:{x:5,y:300}});await p.keyboard.press('j');await p.keyboard.press('?');
    if(await p.evaluate(()=>!!document.activeElement.closest('.report-row'))||await p.locator('#shortcuts').evaluate(d=>d.open)||await p.locator('#show-shortcuts').count())throw Error('single keys still act with shortcuts off');
    await p.goto(base+'/new');await p.locator('#markdown').fill('Words');
    const printKey=await p.evaluate(()=>{const e=new KeyboardEvent('keydown',{key:'s',metaKey:true,ctrlKey:true,bubbles:true,cancelable:true});document.dispatchEvent(e);return e.defaultPrevented;});
    if(printKey)throw Error('the editor took Command S with shortcuts off');
    await p.evaluate(()=>{onbeforeunload=null;});
    p.once('dialog',dialog=>dialog.accept());await p.goto(base+'/settings');await p.locator('#shortcuts').check();await p.locator('.toast').getByText('Keyboard shortcuts are on.').waitFor();
    await p.goto(base+'/');await p.locator('body').click({position:{x:5,y:300}});await p.keyboard.press('j');
    if(!await p.evaluate(()=>!!document.activeElement.closest('.report-row')))throw Error('J did not move once shortcuts were back on');
    // Single keys act only in the list or on the page itself, never from another control.
    await p.locator('.topbar-settings').focus();await p.keyboard.press('j');
    if(await p.evaluate(()=>document.activeElement.classList.contains('topbar-settings'))!==true)throw Error('J acted from the top bar');
   }finally{await o.close();}
   // Phones: the library's first row starts in the first screen; the order control fits with a long author.
   for(const width of [390,320]){
    const m=await browser.newContext({viewport:{width,height:844},hasTouch:true,isMobile:true});
    try{
     const p=await m.newPage();await p.goto(base+'/');
     const top=await p.locator('.report-row').first().evaluate(el=>el.getBoundingClientRect().top);
     if(width===390&&top>420)throw Error('the first row starts '+top+'px down on a phone');
     await p.locator('#filter-toggle').click();await p.locator('select[name=agent]').selectOption(long);await p.waitForTimeout(500);
     await p.locator('#filter-toggle').click().catch(()=>{});if(!await p.locator('select[name=sort]').isVisible())await p.locator('#filter-toggle').click();
     for(const name of ['agent','sort']){const box=await p.locator('select[name='+name+']').boundingBox();if(!box||box.x<0||box.x+box.width>width)throw Error(name+' control off screen at '+width+': '+JSON.stringify(box));}
     const add=await p.locator('.split-create > .button').boundingBox();
     if(add.height<44||add.y<500||!(await p.locator('.split-create > .button').innerText()).includes('New page'))throw Error('New page is not a named control in reach of the thumb');
     // No owner route scrolls sideways.
     for(const route of ['/','/?shared=1','/?archived=1','/new','/edit/council-tax','/import','/manage/report','/settings','/no-such-page']){
      await p.goto(base+route);await p.waitForTimeout(150);
      const wide=await p.evaluate(()=>document.documentElement.scrollWidth-innerWidth);
      if(wide>0)throw Error(route+' scrolls sideways at '+width+'px by '+wide);
     }
    }finally{await m.close();}
   }
   // Without JavaScript the settings forms keep their Save buttons, and key controls are not offered.
   const nojs=await browser.newContext({javaScriptEnabled:false,viewport:{width:1280,height:900}});
   try{
    const p=await nojs.newPage();await p.goto(base+'/settings');
    for(const name of ['Save defaults','Save Kindle address','Save theme'])if(!await p.getByRole('button',{name}).isVisible())throw Error('no-JavaScript settings lacks '+name);
    if(await p.locator('#connect-agent').isVisible()||await p.locator('.manage-key').first().isVisible())throw Error('key controls show without JavaScript');
   }finally{await nojs.close();}
  }
  {
   // Broken states (p9): what looked broken or said something false, each checked the way a person meets it.
   const api=body=>fetch(base+'/api/pages',{method:'POST',headers:{'content-type':'application/json',authorization:'Bearer browser-smoke-token-at-least-32-characters'},body:JSON.stringify(body)});
   const ownerForm=async(path,fields)=>{const csrf=(await (await fetch(base+'/settings')).text()).match(/name="csrf" value="([^"]+)"/)[1];return fetch(base+path,{method:'POST',redirect:'manual',headers:{origin:base,'content-type':'application/x-www-form-urlencoded'},body:new URLSearchParams({csrf,...fields})});};
   const fence='```';
   const filler=n=>Array.from({length:n},(_,i)=>'Paragraph '+(i+1)+' of this part runs long enough to wrap across several lines at every width, as a real report would, so the section fills a screen.').join('\n\n');
   const parts=['Where the kitchen is today','Timeline and the order of every trade on site','Finishes chosen to hide wear near the garden doors','Budget and where every pound goes','What to book this week'];
   const gantt=fence+'mermaid\ngantt\n    title Kitchen works, January to March\n    dateFormat YYYY-MM-DD\n    section Strip and prep\n    Strip out old units :a1, 2027-01-11, 5d\n    Remove floor tiles :a2, after a1, 3d\n    Re-plaster two walls :a3, after a2, 5d\n    section First fix\n    Electrics first fix :b1, after a2, 4d\n    Plumbing first fix :b2, after a2, 3d\n    Underfloor heating mat :b3, after b2, 2d\n    section Fit\n    Floor screed and cure :c1, after b3, 10d\n    Cabinets fitted :c2, after c1, 5d\n    Worktop template :milestone, c3, after c2, 0d\n    Worktop made and fitted :c4, after c3, 12d\n    section Finish\n    Second fix electrics :d1, after c4, 2d\n    Splashback tiling :d2, after c4, 3d\n    Paint and snag :d3, after d2, 4d\n'+fence;
   const pie=fence+'mermaid\npie title Where the money goes\n    "Cabinets" : 27\n    "Worktop" : 16\n    "Floor" : 8\n    "Tiles" : 3\n    "Labour" : 37\n    "Contingency" : 9\n'+fence;
   const kitchen='# Kitchen renovation plan: timeline, finishes and budget\n\nTwelve weeks from strip-out to the last coat of paint.\n\n'+parts.map((name,i)=>'## '+name+'\n\n'+filler(3)+(i===1?'\n\n### The first fortnight on site\n\n'+filler(3)+'\n\n'+gantt:'')+(i===3?'\n\n'+pie+'\n\nA design load of 6.1 kW at 45 °C, and Drives (2 × 8 TB) in the cupboard. '+filler(1):'')).join('\n\n')+'\n\n## The end\n\n'+filler(10);
   for(const [slug,markdown] of [['p9-kitchen',kitchen],['p9-kitchen-short',kitchen.replace(/^## (.+)$/gm,(_,name)=>'## '+name.split(' ')[0])]])if(!(await api({title:'Kitchen renovation plan: timeline, finishes and budget',agent:'Codex',original_request:'Plan the kitchen.',request_source:'Claude Code',slug,template:'visual',markdown})).ok)throw Error('p9 kitchen page');
   const o=await browser.newContext({viewport:{width:1440,height:900}});
   try{
    const p=await o.newPage();p.setDefaultTimeout(10000);
    // 1. The owner's own name is refused beside the field; no key dialog opens for it.
    await p.goto(base+'/settings');const owner=await p.locator('#connect-agent').getAttribute('data-owner');
    await p.locator('#new-person-name').fill(owner.toLowerCase());await p.locator('#new-person-key').click();
    await p.locator('#new-person-name-error').getByText('You write as '+owner+' without a key, so give the agent a name of its own.').waitFor();
    if(await p.locator('#key-dialog').evaluate(d=>d.open)||await p.locator('#new-person-name').getAttribute('aria-invalid')!=='true')throw Error('the owner\'s own name opened the key dialog');
    // 3. An address that can never arrive is refused and not saved; one that isn't Amazon's is saved with one amber warning.
    await p.locator('#kindle-address').fill('chris@kindle');await p.keyboard.press('Tab');
    await p.locator('#kindle-address-error').getByText('Enter one email address, such as name@kindle.com.').waitFor();
    let kindle=await p.evaluate(()=>({tick:document.querySelector('.save-state[data-for="kindle-address"]').classList.contains('is-saved'),warning:!document.querySelector('#kindle-address-warning').hidden}));
    if(kindle.tick||kindle.warning||(await (await fetch(base+'/settings')).text()).includes('value="chris@kindle"'))throw Error('an undeliverable Kindle address was saved: '+JSON.stringify(kindle));
    await p.locator('#kindle-address').fill('chris@gmail.com');await p.keyboard.press('Tab');await p.locator('#kindle-address-warning').waitFor();
    kindle=await p.evaluate(()=>{const icon=getComputedStyle(document.querySelector('#kindle-address-warning .icon')).color,root=getComputedStyle(document.documentElement);const probe=document.createElement('span');document.body.append(probe);probe.style.color='var(--warning-ink)';const amber=getComputedStyle(probe).color;probe.style.color='var(--error-ink)';const brick=getComputedStyle(probe).color;probe.remove();return {icon,amber,brick,error:!document.querySelector('#kindle-address-error').hidden,invalid:document.querySelector('#kindle-address').getAttribute('aria-invalid'),warnings:[...document.querySelectorAll('#kindle .is-warning:not([hidden]), .toast')].map(el=>el.textContent).filter(text=>text.includes('@free.kindle.com')).length};});
    if(kindle.icon!==kindle.amber||kindle.icon===kindle.brick||kindle.error||kindle.invalid==='true'||kindle.warnings!==1)throw Error('a non-Kindle address is not one amber warning: '+JSON.stringify(kindle));
    await p.locator('#kindle-address').fill('reader@kindle.com');await p.keyboard.press('Tab');await p.locator('#kindle-address-warning').waitFor({state:'hidden'});
    // 2. A taken link name never shows as an address; Private shows the name the page will use.
    await p.goto(base+'/new');await p.getByLabel('Title',{exact:true}).fill('Loft quotes');await p.getByLabel('Markdown',{exact:true}).fill('Three firms quoted.');
    await p.locator('#access-trigger').click();await p.locator('[name=access][value=public]').check({force:true});
    await p.locator('#page-slug').fill('report');await p.locator('#page-slug-error').getByText(/already uses this link/).waitFor();
    if(await p.locator('#address-full').textContent()!=='')throw Error('a taken link name still shows as an address');
    await p.locator('#page-slug').fill('p9-loft-quotes');await p.locator('#address-full').getByText('https://reports.example.com/p9-loft-quotes').waitFor();
    await p.locator('[name=access][value=private]').check({force:true});await p.locator('#address-full').getByText(new URL(base).host+'/p9-loft-quotes').waitFor();
    // 10. A restored draft is the page's own toast, whole on the screen.
    await p.waitForTimeout(600);p.once('dialog',dialog=>dialog.accept());await p.reload();
    await p.locator('#toasts #draft-note').getByText(/^Draft restored from /).waitFor();
    const draft=await p.locator('#draft-note').evaluate(t=>{const r=t.getBoundingClientRect();return {left:r.left,right:r.right,width:r.width,vw:innerWidth};});
    if(draft.left<0||draft.right>draft.vw||draft.width>420)throw Error('the draft note is not a toast on screen: '+JSON.stringify(draft));
    await p.locator('#draft-note').getByRole('button',{name:'Discard draft'}).click();await p.waitForURL('**/new');
    // 13. Every formatting sample reads whole inside the sheet.
    await p.getByRole('button',{name:'Formatting help'}).click();
    const clipped=await p.locator('.format-sample').evaluateAll(els=>els.filter(e=>e.scrollWidth>e.clientWidth+1).map(e=>e.textContent.slice(0,30)));
    if(clipped.length)throw Error('formatting samples run off the sheet: '+clipped.join(' | '));
    await p.keyboard.press('Escape');
    // 16. The edit refusal says why once; a toast in the reader keeps clear of the text column.
    if(!(await api({title:'Custom energy page',agent:'Hermes',original_request:'Energy.',slug:'p9-custom',html:'<!doctype html><title>Energy</title><p>Mine.</p>'})).ok)throw Error('p9 custom page');
    await p.goto(base+'/edit/p9-custom');const refusal=await p.locator('main').innerText();
    if((refusal.match(/can’t edit this page/g)||[]).length!==1||!refusal.includes('It’s custom HTML from Hermes.'))throw Error('the edit refusal repeats its heading: '+refusal);
    relayMode='busy';
    await p.goto(base+'/p9-kitchen');await p.locator('.reader-cover .more-button').click();await p.locator('#reader-menu').getByRole('button',{name:/Send to Kindle/}).click();
    await p.locator('.toast').getByText('The mail server asked Cairn to try later, so nothing was sent.').waitFor();
    const clear=await p.evaluate(()=>{const t=document.querySelector('.toast').getBoundingClientRect(),pad=parseFloat(getComputedStyle(document.querySelector('.reader-grid')).paddingLeft);return {toast:t.left,column:innerWidth-pad,retry:!!document.querySelector('.toast .toast-actions button')};});
    if(clear.toast<clear.column||!clear.retry)throw Error('the Kindle toast covers the text or lost Try again: '+JSON.stringify(clear));
    // 4. Each failure says its cause; Try again only where it can help.
    relayMode='auth';
    await p.locator('.toast-close').click();await p.locator('.reader-cover .more-button').click();await p.locator('#reader-menu').getByRole('button',{name:/Send to Kindle/}).click();
    await p.locator('.toast').getByText(/refused Cairn’s sign-in/).waitFor();
    if(await p.locator('.toast .toast-actions button').count()||!await p.locator('.toast').getByRole('link',{name:'Mail troubleshooting'}).count())throw Error('a refused sign-in offers Try again');
    relayMode='ok';
    // 8. The request label reads with its separators.
    const request=await p.locator('#request-label').evaluate(h=>h.closest('section').getAttribute('aria-labelledby'));
    if(!await p.getByRole('heading',{name:/^Your request\s*,\s*via Claude Code\s*,\s*only you see this$/}).count()||request!=='request-label')throw Error('the request label runs its parts together');
    // 6. When the title and the section do not both fit, the section shows alone: never a cut title beside a
    // section. Whole-pixel widths hide a cut of less than one, so the window narrows a pixel at a time, and a
    // late change of room (the contents button coming back) refits the bar too.
    const bothCut=()=>p.evaluate(async()=>{await new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r)));const page=document.querySelector('.compact-page'),section=document.querySelector('.compact-section');if(getComputedStyle(page).display==='none'||!section.textContent)return false;const cut=el=>{const r=document.createRange();r.selectNodeContents(el);return r.getBoundingClientRect().right>el.getBoundingClientRect().right+0.5;};return cut(page)||cut(section);});
    for(const slug of ['p9-kitchen','p9-kitchen-short']){
     await p.goto(base+'/'+slug);await p.waitForFunction(()=>document.body.classList.contains('report-settled'));
     const frame=p.frames().find(f=>f!==p.mainFrame());const tops=await frame.evaluate(()=>[...document.querySelectorAll('h2[id],h3[id]')].map(h=>h.getBoundingClientRect().top+scrollY));
     const frameTop=await p.evaluate(()=>document.querySelector('.report-frame').getBoundingClientRect().top+scrollY);
     for(const top of tops){await p.evaluate(y=>scrollTo(0,y),frameTop+top-40);await p.waitForTimeout(200);if(await bothCut())throw Error('the bar truncates twice on '+slug);}
    }
    await p.evaluate(()=>scrollTo(0,0));await p.waitForTimeout(100);
    const short=await p.frames().find(f=>f!==p.mainFrame()).evaluate(()=>document.querySelectorAll('h2[id]')[1].getBoundingClientRect().top+scrollY);
    await p.evaluate(y=>scrollTo(0,y+document.querySelector('.report-frame').getBoundingClientRect().top),short-40);await p.waitForTimeout(200);
    for(let width=1440;width>=1001;width-=1){await p.setViewportSize({width,height:900});if(await bothCut())throw Error('the bar truncates twice at '+width+'px');}
    await p.setViewportSize({width:1440,height:900});
    await p.evaluate(()=>document.body.classList.remove('toc-floating'));if(await bothCut())throw Error('the bar did not refit when its room changed');
    // 18. Gantt grid lines stop at task names; pie shares sit in their slice or beside the pie, without a halo; no hairline frame.
    await p.goto(base+'/p9-kitchen');await p.waitForFunction(()=>document.body.classList.contains('report-settled'));
    for(let y=0;y<9000;y+=500){await p.evaluate(y=>scrollTo(0,y),y);await p.waitForTimeout(120);}
    const kf=p.frames().find(f=>f!==p.mainFrame());
    await kf.waitForFunction(()=>document.querySelectorAll('figure.diagram[data-kind]').length===2,null,{timeout:15000});
    const drawn=await kf.evaluate(()=>{
     const g=document.querySelector('figure.diagram[data-kind="gantt"] svg'),lines=[...g.querySelectorAll('.grid .tick line')].map(l=>l.getBoundingClientRect()).filter(r=>r.height>0);
     let crossed=0;const struck=[];
     for(const t of g.querySelectorAll('text.taskTextOutsideRight, text.taskTextOutsideLeft')){const r=t.getBoundingClientRect();for(const l of lines){if(l.left<=r.left+1||l.left>=r.right-1)continue;crossed++;const top=document.elementFromPoint(l.left+l.width/2,(r.top+r.bottom)/2);if(top?.closest('.grid'))struck.push(t.textContent.trim());}}
     const s=document.querySelector('figure.diagram[data-kind="pie"] svg'),ring=s.querySelector('circle.pieOuterCircle').getBoundingClientRect(),cx=(ring.left+ring.right)/2,cy=(ring.top+ring.bottom)/2,radius=ring.width/2,box=s.getBoundingClientRect();
     const labels=[...s.querySelectorAll('text.slice')].map(t=>{const r=t.getBoundingClientRect(),st=getComputedStyle(t);return {text:t.textContent,outside:Math.hypot((r.left+r.right)/2-cx,(r.top+r.bottom)/2-cy)>radius,inside:r.left>=box.left&&r.right<=box.right&&r.top>=box.top&&r.bottom<=box.bottom,halo:st.stroke!=='none'&&parseFloat(st.strokeWidth)>0};});
     const frames=[...document.querySelectorAll('figure.diagram:not([data-overflow]) > .diagram-scroll')].map(s=>getComputedStyle(s).backgroundImage);
     return {crossed,struck,labels,frames};
    });
    if(!drawn.crossed||drawn.struck.length)throw Error('gantt grid lines strike through task names: '+JSON.stringify(drawn));
    const tiles=drawn.labels.find(l=>l.text==='3%');
    if(drawn.labels.some(l=>l.halo)||!tiles?.outside||!drawn.labels.every(l=>l.inside)||drawn.labels.filter(l=>l.outside).length!==1)throw Error('pie shares: '+JSON.stringify(drawn.labels));
    if(!drawn.frames.length||drawn.frames.some(b=>b!=='none'))throw Error('a diagram that does not scroll keeps its edge shadows: '+JSON.stringify(drawn.frames));
    // 17. Manage: Save title waits for an edit, Save sharing is the one primary button, Remove password sits under the password.
    await ownerForm('/manage/p9-kitchen',{action:'share'});await ownerForm('/manage/p9-kitchen',{action:'password',password:'kitchen-password'});
    await p.goto(base+'/manage/p9-kitchen');
    if(!await p.getByRole('button',{name:'Save title'}).isDisabled())throw Error('Save title looks ready with nothing changed');
    await p.locator('#page-title').fill('Kitchen plan, renamed');if(await p.getByRole('button',{name:'Save title'}).isDisabled())throw Error('Save title stays off after an edit');
    const manage=await p.evaluate(()=>{const save=[...document.querySelectorAll('#sharing button')].find(b=>b.textContent.trim()==='Save sharing'),remove=[...document.querySelectorAll('#sharing button')].find(b=>b.textContent.trim()==='Remove password'),probe=document.createElement('span');document.body.append(probe);probe.style.color='var(--accent)';const accent=getComputedStyle(probe).color;probe.remove();const filled=[...document.querySelectorAll('#sharing button:not([hidden])')].filter(b=>getComputedStyle(b).backgroundColor===accent&&!b.classList.contains('switch')).map(b=>b.textContent.trim());return {filled,under:document.querySelector('#share-password').closest('.field').contains(remove),quiet:getComputedStyle(remove).backgroundColor};});
    if(manage.filled.join()!=='Save sharing'||!manage.under||manage.quiet!=='rgba(0, 0, 0, 0)')throw Error('manage sharing actions: '+JSON.stringify(manage));
    // 12. An imported PDF offers its file once, and a visitor's copy offers none.
    await p.goto(base+'/settings');
    const imported=await p.evaluate(async()=>{const body=new FormData();body.set('csrf',document.querySelector('[name=csrf]').value);body.set('slug','p9-pdf');body.set('document',new File(['%PDF-1.4\n'+'x'.repeat(48000)],'boiler-service-2026.pdf',{type:'application/pdf'}));const r=await fetch('/import',{method:'POST',body,redirect:'manual'});return r.status||r.type;});
    if(imported!=='opaqueredirect'&&imported!==303)throw Error('PDF import: '+imported);
    await ownerForm('/manage/p9-pdf',{action:'share'});
    await p.goto(base+'/p9-pdf');await p.waitForFunction(()=>document.body.classList.contains('report-settled'));
    if(await p.getByText('Download original').count()||!await p.getByRole('link',{name:'Download',exact:true}).count())throw Error('the owner sees the file offered twice or not at all');
    const visitor=await browser.newPage();await visitor.goto(publicBase+'/p9-pdf');await visitor.waitForFunction(()=>document.body.classList.contains('report-settled'));
    if(await visitor.getByRole('link',{name:'Download',exact:true}).count()!==1||await visitor.getByText('Download original').count())throw Error('shared file must offer one Download');
    await visitor.close();
   }finally{await o.close();}
   // 5. The bar names a section once its heading reaches the bar's bottom edge: when the last line before it has
   // gone under the bar. Heading by heading, at a phone, a tablet and a desktop width.
   if(!(await api({title:'A long read for the bar',agent:'Claude',original_request:'Long read.',slug:'p9-spy',markdown:'# A long read for the bar\n\nAn opening paragraph.\n\n'+['The house as it stands','Heat loss, room by room','The loft question','Option one: a new gas combi','Option two: an air-source heat pump','Payback'].map((name,i)=>(i===1||i===2?'### ':'## ')+name+'\n\n'+filler(i%2?2:3)).join('\n\n')+'\n\n## What to do next\n\n'+filler(14)})).ok)throw Error('p9 spy page');
   for(const [width,height,phone] of [[390,844,true],[820,1180,false],[1440,900,false]]){
    const c=await browser.newContext({viewport:{width,height},isMobile:phone,hasTouch:phone});
    try{
     const m=await c.newPage();await m.goto(base+'/p9-spy');await m.waitForFunction(()=>document.body.classList.contains('report-settled'));await m.waitForTimeout(300);
     const frame=m.frames().find(f=>f!==m.mainFrame());
     const heads=await frame.evaluate(()=>[...document.querySelectorAll('h2[id],h3[id]')].map(h=>({name:h.textContent.trim(),before:h.previousElementSibling.getBoundingClientRect().bottom+scrollY})));
     const frameTop=await m.evaluate(()=>document.querySelector('.report-frame').getBoundingClientRect().top+scrollY);
     const named=async y=>{await m.evaluate(y=>scrollTo(0,y),y);await m.waitForTimeout(150);return m.evaluate(()=>[document.querySelector('.compact-section').textContent,document.querySelector('.compact-reader').offsetHeight]);};
     const [,bar]=await named(frameTop);
     for(let i=0;i<heads.length-1;i++){
      // The last line before the heading 1px under the bar's bottom edge, then 8px into view again.
      const at=Math.ceil(frameTop+heads[i].before-bar)+1;
      const [now]=await named(at);if(now!==heads[i].name)throw Error(width+'px: the bar names "'+now+'" with "'+heads[i].name+'" directly under it');
      const [before]=await named(at-9);if(before!==(i?heads[i-1].name:''))throw Error(width+'px: the bar names "'+before+'" while "'+(i?heads[i-1].name:'the opening')+'" still shows under it');
     }
    }finally{await c.close();}
   }
   // Phones: 7, 9, 11, 13, 14.
   const ph=await browser.newContext({viewport:{width:390,height:844},isMobile:true,hasTouch:true});
   try{
    const m=await ph.newPage();m.setDefaultTimeout(10000);
    // 9. Search: blocks never run together, the rarer word explains the hit, and the highlight shows on a phone.
    for(const page of [
     {title:'October energy use',agent:'Hermes',original_request:'Energy.',slug:'p9-energy',html:'<!doctype html><title>October energy use</title><h1>October energy use</h1><div class="card"><b>1,046 kWh</b><div>gas, up 61% as heating came on</div></div>'},
     {title:'NAS boxes compared',agent:'Codex',original_request:'Compare quiet two-bay NAS boxes, about £600 with drives.',slug:'p9-nas',markdown:'# NAS boxes compared\n\nAll prices include two 8 TB Seagate IronWolf drives at £178 each.'},
     {title:'Two nights in the Lakes',agent:'Claude',original_request:'Plan two nights.',slug:'p9-lakes',markdown:'# Two nights in the Lakes\n\nGrasmere to Rydal along the old coffin route. Flat, 6 km, with a stop at the Wordsworth Museum at Dove Cottage before the bus back.'},
    ])if(!(await api(page)).ok)throw Error('p9 search page '+page.slug);
    const snippet=async(q,slug)=>{await m.goto(base+'/?q='+encodeURIComponent(q));return m.locator('.report-row[data-slug="'+slug+'"] .snippet').evaluate(s=>{const mark=s.querySelector('mark'),a=s.getBoundingClientRect(),b=mark.getBoundingClientRect();return {html:s.innerHTML,seen:b.right<=a.right+1&&b.bottom<=a.bottom+1};});};
    let found=await snippet('gas','p9-energy');if(found.html.includes('kWhgas')||!found.html.includes('<mark>gas</mark>'))throw Error('blocks run together in the index: '+found.html);
    found=await snippet('IronWolf drives','p9-nas');if(!found.html.includes('<mark>IronWolf</mark>'))throw Error('the hit is explained without the rarer word: '+found.html);
    found=await snippet('wordsworth','p9-lakes');if(!found.seen)throw Error('the highlight is cut off on a phone: '+found.html);
    // 11. New page sits in a bar at the foot: nothing of a row shows beside it, the last row clears it, and a file row reads whole.
    await m.goto(base+'/');
    for(const y of [200,400,700]){
     await m.evaluate(y=>scrollTo(0,y),y);await m.waitForTimeout(100);
     const beside=await m.evaluate(()=>{const b=document.querySelector('.split-create').getBoundingClientRect(),dock=document.querySelector('.create-dock'),d=dock.getBoundingClientRect(),y=(b.top+b.bottom)/2,rows=[];for(let x=2;x<innerWidth;x+=24){const el=document.elementFromPoint(x,y);if(el?.closest('.report-row'))rows.push(x);}return {rows,full:d.left<=0&&d.right>=innerWidth,bg:getComputedStyle(dock).backgroundColor,inside:b.left>=d.left&&b.right<=d.right&&b.top>=d.top&&b.bottom<=d.bottom};});
     if(beside.rows.length||!beside.full||!beside.inside||/rgba\(.*, 0\)$/.test(beside.bg))throw Error('New page sits on rows: '+JSON.stringify(beside));
    }
    await m.evaluate(()=>scrollTo(0,document.documentElement.scrollHeight));await m.waitForTimeout(150);
    const end=await m.evaluate(()=>{const rows=[...document.querySelectorAll('.report-row')],last=rows[rows.length-1].getBoundingClientRect();return {last:last.bottom,dock:document.querySelector('.create-dock').getBoundingClientRect().top};});
    if(end.last>end.dock)throw Error('the last row ends under New page: '+JSON.stringify(end));
    await m.evaluate(()=>scrollTo(0,0));
    const pdfRow=await m.locator('.report-row[data-slug="p9-pdf"] .excerpt').evaluate(e=>{const r=document.createRange();r.selectNodeContents(e);return {text:e.textContent,whole:r.getBoundingClientRect().right<=e.getBoundingClientRect().right+0.5};});
    if(!/^PDF · 47 KB$/.test(pdfRow.text)||!pdfRow.whole)throw Error('the PDF row does not read whole: '+JSON.stringify(pdfRow));
    // 13. The preview on a phone fits a four-column table whole.
    await m.goto(base+'/new');await m.getByLabel('Title',{exact:true}).fill('Loft conversion quotes, first look');
    await m.getByLabel('Markdown',{exact:true}).fill('Two came back within a week.\n\n## The quotes\n\n| Firm | Price | Weeks | Includes stairs |\n|---|---:|---:|---|\n| Loftcraft | £48,500 | 9 | Yes |\n| Hartley Builds | £52,000 | 8 | Yes |\n');
    await m.getByRole('button',{name:'Preview'}).click();
    const preview=m.frames().find(f=>f.name()==='editor-preview');await preview.waitForSelector('.table-scroll',{timeout:10000});await m.waitForTimeout(400);
    const table=await preview.evaluate(()=>{const s=document.querySelector('.table-scroll');return [s.scrollWidth,s.clientWidth];});
    if(table[0]>table[1]+1)throw Error('the preview cuts the table: '+table);
    await m.evaluate(()=>{try{localStorage.clear();}catch{}});
    // 14. A number keeps its unit on its line.
    await m.goto(base+'/p9-kitchen');await m.waitForFunction(()=>document.body.classList.contains('report-settled'));
    const units=await m.frames().find(f=>f!==m.mainFrame()).evaluate(()=>{const text=document.body.textContent;const split=[];const walker=document.createTreeWalker(document.body,NodeFilter.SHOW_TEXT);let n;while((n=walker.nextNode())){if(n.parentElement.closest('pre,code,svg,.sr-only'))continue;for(const m of n.data.matchAll(/(\d)([  ])(kW|°C|×|TB)/g)){const a=document.createRange();a.setStart(n,m.index);a.setEnd(n,m.index+1);const b=document.createRange();b.setStart(n,m.index+2);b.setEnd(n,m.index+2+m[3].length);if(Math.abs(a.getBoundingClientRect().top-b.getBoundingClientRect().top)>4||m[2]===' ')split.push(m[0]);}}return {split,joined:text.includes('6.1 kW')&&text.includes('2 × 8 TB')};});
    if(units.split.length||!units.joined)throw Error('numbers part from their units: '+JSON.stringify(units));
    // 18 on a phone: a share moved beside the pie never lands on the legend under it, nor leaves the drawing.
    for(let y=0;y<14000;y+=700){await m.evaluate(y=>scrollTo(0,y),y);await m.waitForTimeout(100);}
    const pieFrame=m.frames().find(f=>f!==m.mainFrame());await pieFrame.waitForFunction(()=>document.querySelector('figure.diagram[data-kind="pie"] text.slice-outside'),null,{timeout:15000});
    const clash=await pieFrame.evaluate(()=>{const s=document.querySelector('figure.diagram[data-kind="pie"] svg'),box=s.getBoundingClientRect(),legend=[...s.querySelectorAll('g.legend')].map(g=>g.getBoundingClientRect());return [...s.querySelectorAll('text.slice')].map(t=>t.getBoundingClientRect()).filter(r=>r.left<box.left-1||r.right>box.right+1||r.top<box.top-1||r.bottom>box.bottom+1||legend.some(l=>r.right>l.left&&r.left<l.right&&r.bottom>l.top&&r.top<l.bottom)).length;});
    if(clash)throw Error('a pie share lands on the legend or outside the drawing on a phone');
    // 7. A visitor who mistypes once and retypes at once gets in; after repeated mistakes the try waits, keeping what was typed.
    await ownerForm('/manage/p9-lakes',{action:'share'});await ownerForm('/manage/p9-lakes',{action:'password',password:'lakes-password'});
    const v=await ph.newPage();await v.goto(publicBase+'/p9-lakes');
    await v.locator('#gate-password').fill('lakes-pasword');await v.getByRole('button',{name:'Unlock page'}).click();await v.locator('#gate-password-error').getByText('That password didn’t match. Check it and try again.').waitFor();
    await v.locator('#gate-password').fill('lakes-password');await v.getByRole('button',{name:'Unlock page'}).click();
    await v.frameLocator('iframe').locator('h1').first().waitFor({state:'attached'});
    await v.context().clearCookies();await v.goto(publicBase+'/p9-lakes');
    for(const guess of ['first-guess','second-guess']){await v.locator('#gate-password').fill(guess);await Promise.all([v.waitForNavigation({timeout:15000}),v.getByRole('button',{name:'Unlock page'}).click()]);}
    await v.locator('#gate-password-error').getByText(/You can try again in \d+ seconds?\./).waitFor();
    await v.locator('#gate-password').fill('lakes-password');await v.getByRole('button',{name:'Unlock page'}).click();
    await v.locator('#gate-password-error').getByText(/^Cairn will check it in \d+ seconds?\.$/).waitFor();
    if(await v.locator('#gate-password').inputValue()!=='lakes-password')throw Error('the held try lost what was typed');
    await v.frameLocator('iframe').locator('h1').first().waitFor({state:'attached',timeout:15000});
    await v.close();
    // 15. Minimal gives the reader's portrait the ringed disc it uses everywhere else.
    const theme=await (async()=>{await m.goto(base+'/settings');return m.locator('html').getAttribute('data-theme');})();
    await ownerForm('/settings',{action:'themes',theme:'minimal',reader_theme:'',public_theme:''});
    await m.goto(base+'/p9-kitchen');
    const portrait=await m.locator('.reader-cover .author-portrait').evaluate(e=>{const s=getComputedStyle(e),cover=getComputedStyle(e.closest('.reader-cover'));return {bg:s.backgroundColor,cover:cover.backgroundColor,ring:s.boxShadow};});
    await ownerForm('/settings',{action:'themes',theme,reader_theme:'',public_theme:''});
    if(portrait.bg===portrait.cover||!/inset/.test(portrait.ring))throw Error('the Minimal portrait has no disc: '+JSON.stringify(portrait));
    // 8. Private, the default, is not announced on every row.
    await m.goto(base+'/');if(await m.locator('.report-row .row-head').filter({hasText:'Private'}).count())throw Error('rows announce Private');
   }finally{await ph.close();}
  }
  console.log('Headless Chromium passed: broken states put right (the owner\'s own name refused before any key dialog, taken link names never shown as addresses, Kindle addresses checked before saving with one amber warning, Send to Kindle failures named by cause with Try again only where it helps, the bar naming a section once its heading reaches the bar at 390, 820 and 1440px, never two truncations, one wrong password costing nothing and a held try keeping what was typed, spoken separators and no announced default, search snippets that explain the hit, the draft note as a toast, New page docked clear of the rows, file pages offering one download to the owner and authorised visitors, whole formatting samples and preview tables, numbers kept with their units, Minimal portraits, one-sentence refusals, toasts clear of the text, one primary action in sharing, and Mermaid labels clear of grid lines and halos), owner forms without browser bubbles, toasts that keep the page still, settings that save as you go, the shortcut setting, a phone library in the first screen, no sideways scrolling at 320px, dates in each browser time zone, 44px touch targets and unzoomed fields, one reader grid for every page type, contents that never float over custom pages and end between entries beside wide diagrams, full-width comparisons, the bar naming the section being read, keys that follow the shortcut setting and show their focus, a single truncation in the bar, reports that cannot steer or stretch the reader, Mermaid fetched only near its diagram, text alternatives for charts and diagrams, reduced motion as a fade, responsive readers, sandbox isolation, images, charts, imports, library lifecycle, matching share paths, passwords, revocation, editor preview/save, agent keys, settings, four themes and System applied in one click (every surface dark in Dark, Futuristic and System at night, reports following through the scheme message, custom HTML kept as its own sheet, shared pages on their own theme, the no-JavaScript form), Send to Kindle, the agent key dialog (create, copy, setup text, test, rotate and revoke in place, focus kept and returned), the shared shell, visible focus, touch targets, cached assets, deferred runtime, font loading per theme, the sharing control, the password gate, the import drop zone, draft restore (kept by page, expiring after 30 days, cleared on delete), themed errors, first run, search as you type, keyboard list navigation, undo archive, paging, editing saved pages and Markdown reports on phones (footnotes, scroll regions, diagrams, charts, pinned header rows and the dark report scheme), and the reader keyboard paths, overflow menu, copying in place, pinned header rows below the bar, end of page, visitor strip and contents without JavaScript.');
 }finally{
  if(browser)await browser.close();
  relay.close();
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
