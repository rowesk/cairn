// Real UI screenshots from disposable, fictional data. Run after `go build -o cairn ./cmd/cairn`.
const {chromium} = require('playwright');
const {spawn} = require('node:child_process');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');

(async () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'cairn-demo-'));
  const token = 'fictional-demo-only-not-a-real-credential';
  const child = spawn(path.resolve('cairn'), ['-data', dir, '-listen', '127.0.0.1:0'], {
    env: {...process.env, CAIRN_PUBLISHER_TOKEN: token}
  });
  let browser;
  try {
    const address = await new Promise((resolve, reject) => {
      let output = '';
      const finish = (error, address) => {
        clearTimeout(timer);
        child.stderr.off('data', onData);
        child.off('error', onError);
        child.off('exit', onExit);
        error ? reject(error) : resolve(address);
      };
      const onData = data => {
        output += data.toString();
        const match = output.match(/private listener: (\S+)/);
        if (match) finish(null, match[1]);
      };
      const onError = error => finish(error);
      const onExit = code => finish(Error('Cairn exited before startup: ' + code));
      const timer = setTimeout(() => finish(Error('Cairn startup timed out')), 10000);
      child.stderr.on('data', onData);
      child.once('error', onError);
      child.once('exit', onExit);
    });
    const base = 'http://' + address;
    const examples = [
      {
        title: 'A home for the reading list', agent: 'Writing assistant', slug: 'r4D',
        original_request: 'Help me organise saved articles without turning reading into another job.',
        markdown: '# Keep the queue short\n\nSeparate what you want to read next from references you want to keep. Give the reading queue a limit of five articles.\n\n## A weekly reset\n\nKeep one long read for the weekend. Archive anything you no longer want to finish.'
      },
      {
        title: 'A weekend away, with room to stop', agent: 'Planning assistant', slug: 'w7K',
        original_request: 'Sketch a relaxed weekend with a walk, good food and no early starts.',
        markdown: '# Leave the morning open\n\nTake a walk after lunch, then stop somewhere for tea. Keep Sunday free until you know the weather.\n\n## Before leaving\n\n- Check the forecast\n- Book somewhere for dinner\n- Download the route for offline use\n\n## Saturday\n\nArrive after lunch and walk the river path while it is still light. Eat early and leave the evening free.\n\n## Sunday\n\nSleep in. If it rains, swap the hill walk for the gallery and a long breakfast.'
      },
      {
        title: 'How the backup gets home', agent: 'Coding assistant', slug: 'b2M',
        original_request: 'Explain a simple backup flow for a small home server.',
        markdown: '# A second copy, somewhere else\n\nStop writes before taking a snapshot. Copy the snapshot to another device and check that you can restore it.\n\n```mermaid\nflowchart LR\n  A[Home server] --> B[Consistent snapshot]\n  B --> C[Encrypted backup]\n  C --> D[Restore check]\n```'
      },
      {
        title: 'The spare room, ready for work', agent: 'Research assistant', slug: 'd3S',
        original_request: 'Compare a few changes to the spare room. Keep the example budget under £200.',
        request_summary: 'Compare practical workspace improvements for a fictional £200 budget.',
        markdown: '# Start with the light\n\nMove the desk beside the window, then add a task lamp for evenings. Keep the rest of the budget for the changes you still notice after a week.\n\n## What the room needs\n\nThe room gets good light until mid-afternoon. After that the desk sits in shadow, the floor collects cables and the chair faces a blank wall.\n\n### Light first\n\nA lamp fixes the evening problem without rewiring anything.\n\n### Then the floor\n\nA cable tray under the desk keeps the floor clear for cleaning.\n\n## A small budget, deliberately spent\n\nIllustrative costs for this demo, not product recommendations.\n\n| Change | Why it earns its place | Budget |\n|---|---|---:|\n| Adjustable task lamp | Light where you read, without lighting the whole room | £45 |\n| Desk mat | A softer surface for the keyboard and mouse | £25 |\n| Cable tray | Clears the floor and makes cleaning easier | £20 |\n| Keep in reserve | Spend after trying the room for a week | £110 |\n\n## Where the £200 goes\n\n```echarts\n' + JSON.stringify({animation: false, color: ['#426b80'], grid: {left: 100, right: 30, top: 16, bottom: 30}, xAxis: {type: 'value', axisLabel: {formatter: '£{value}'}}, yAxis: {type: 'category', data: ['Cable tray', 'Desk mat', 'Task lamp', 'Reserve']}, series: [{type: 'bar', data: [20, 25, 45, 110], barWidth: 24, itemStyle: {borderRadius: [0, 5, 5, 0]}}]}) + '\n```\n\n## After a week\n\nSit in the room for a few evenings before spending the reserve. Most people find the lamp and the tray solve more than they expected.'
      }
    ];
    for (const entry of examples) {
      const response = await fetch(base + '/api/pages', {
        method: 'POST', headers: {authorization: 'Bearer ' + token, 'content-type': 'application/json'},
        body: JSON.stringify(entry)
      });
      if (response.status !== 201) throw Error(await response.text());
    }
    browser = await chromium.launch({headless: true});
    // Reduced motion skips the contents fade-in so the list is captured fully drawn.
    const page = await browser.newPage({viewport: {width: 1440, height: 850}, deviceScaleFactor: 1, reducedMotion: 'reduce'});
    const out = process.env.CAIRN_DEMO_OUT || 'docs/images';
    fs.mkdirSync(out, {recursive: true});
    const settle = async () => { for (const frame of page.frames()) await frame.evaluate(() => document.fonts.ready); };
    const useTheme = async id => {
      await page.goto(base + '/settings');
      for (const view of ['admin', 'reader', 'public']) await page.selectOption('#' + view + '-theme', id);
      await Promise.all([page.waitForURL('**/settings*'), page.locator('#theme-form button:not([type=button])').click()]);
    };
    await page.setViewportSize({width: 390, height: 844});
    for (const theme of ['minimal', 'futuristic', 'cairn']) {
      await useTheme(theme);
      await page.goto(base + '/');
      await settle();
      await page.screenshot({path: path.join(out, 'theme-' + theme + '.png')});
    }
    await page.setViewportSize({width: 1440, height: 850});
    await page.goto(base + '/');
    await settle();
    await page.screenshot({path: path.join(out, 'library.png')});
    await page.setViewportSize({width: 1440, height: 1100});
    await page.goto(base + '/d3S');
    await page.frameLocator('iframe').locator('canvas').waitFor();
    await settle();
    await page.locator('body.toc-floating').waitFor();
    await page.screenshot({path: path.join(out, 'report.png')});
    await page.setViewportSize({width: 390, height: 844});
    await page.goto(base + '/w7K');
    await page.frameLocator('iframe').locator('h1').waitFor();
    await settle();
    await page.waitForFunction(() => document.querySelector('iframe').getBoundingClientRect().height > 400);
    await page.screenshot({path: path.join(out, 'mobile.png')});
    console.log('Captured library, report, mobile and theme screenshots from fictional demo data.');
  } finally {
    if (browser) await browser.close();
    if (child.exitCode === null && child.signalCode === null) {
      const closed = new Promise(resolve => child.once('close', resolve));
      child.kill('SIGTERM');
      await closed;
    }
    fs.rmSync(dir, {recursive: true, force: true});
  }
})().catch(error => { console.error(error); process.exitCode = 1; });
