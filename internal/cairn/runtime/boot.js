// Cairn's report runtime. It draws Mermaid diagrams and ECharts charts in the
// report's own type and colours, keeps their text at 12px or more on phones,
// resizes charts with the page, gives each drawing a text alternative, and
// shows the source when a drawing fails. Colours and type come from the
// report's --report-* tokens when it has them, otherwise from its body text,
// headings and links.
//
// Mermaid is large, so a report that only has Cairn's runtime gets it on
// demand: boot.js is told where it lives (data-mermaid) and fetches it once a
// diagram comes within a screen or so of the part being read.
(() => {
  const root = document.documentElement;
  const reduced = matchMedia('(prefers-reduced-motion: reduce)');
  const later = document.currentScript?.dataset.mermaid || '';
  const charts = [];
  const diagrams = [];
  const sources = new WeakMap();
  let drawn = 0;

  // Any CSS colour as #rrggbb (or rgba()), so colours can be mixed.
  const swatch = document.createElement('canvas').getContext('2d');
  function rgb(colour) {
    swatch.fillStyle = '#000';
    swatch.fillStyle = colour;
    const value = swatch.fillStyle;
    if (value[0] === '#') return [1, 3, 5].map(i => parseInt(value.slice(i, i + 2), 16));
    const parts = value.match(/[\d.]+/g) || [0, 0, 0];
    return parts.slice(0, 3).map(Number);
  }
  // mix(a, b, w): w of a over b, as #rrggbb.
  function mix(a, b, w) {
    const x = rgb(a), y = rgb(b);
    return '#' + x.map((v, i) => Math.round(v * w + y[i] * (1 - w)).toString(16).padStart(2, '0')).join('');
  }
  // contrast(a, b): the WCAG contrast ratio of two colours.
  function contrast(a, b) {
    const lum = colour => {
      const [r, g, bl] = rgb(colour).map(v => { v /= 255; return v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4; });
      return 0.2126 * r + 0.7152 * g + 0.0722 * bl;
    };
    const x = lum(a), y = lum(b);
    return (Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05);
  }

  function tokens() {
    const css = getComputedStyle(root), body = getComputedStyle(document.body);
    const read = (name, fallback) => css.getPropertyValue(name).trim() || fallback;
    const heading = document.querySelector('h2, h3, h1, th');
    const link = document.querySelector('a[href]');
    const clear = colour => !colour || colour === 'transparent' || /rgba\(.*,\s*0\)$/.test(colour);
    const t = {
      ink: read('--report-ink', body.color),
      muted: read('--report-muted', body.color),
      paper: read('--report-paper', clear(body.backgroundColor) ? (clear(css.backgroundColor) ? '#ffffff' : css.backgroundColor) : body.backgroundColor),
      rule: read('--report-rule', 'rgba(128, 128, 128, 0.35)'),
      accent: read('--report-accent', link ? getComputedStyle(link).color : body.color),
      alert: read('--report-alert', '#b3412f'),
      tint: read('--report-tint', 'rgba(128, 128, 128, 0.08)'),
      code: read('--report-code', 'rgba(128, 128, 128, 0.06)'),
      font: read('--report-sans', (heading ? getComputedStyle(heading) : body).fontFamily),
      series: read('--report-series', '').split(',').map(colour => colour.trim()).filter(Boolean),
      dark: css.colorScheme === 'dark'
    };
    if (!t.series.length) t.series = t.dark ? ['#4a8cc8', '#d0743c', '#3a9e7c', '#b98d22', '#9078cf', '#cf5f78'] : ['#2468a8', '#d06a32', '#2f9a78', '#b88a14', '#8466c4', '#c94f6a'];
    return t;
  }

  // The nearest heading above an element names it for screen readers.
  function nearestHeading(element) {
    for (let node = element; node && node !== document.body; node = node.parentElement) {
      for (let previous = node.previousElementSibling; previous; previous = previous.previousElementSibling) {
        const heading = /^H[1-6]$/.test(previous.tagName) ? previous : [...previous.querySelectorAll('h1, h2, h3, h4, h5, h6')].pop();
        const text = heading?.textContent.replace(/\s+/g, ' ').trim();
        if (text) return text;
      }
    }
    return '';
  }

  function sourceBlock(source) {
    const pre = document.createElement('pre');
    const code = document.createElement('code');
    code.textContent = source;
    pre.append(code);
    pre.tabIndex = 0;
    pre.dataset.scroll = '';
    return pre;
  }

  function failed(figure, kind, source, message) {
    const note = document.createElement('p');
    note.className = 'diagram-note';
    note.textContent = message;
    figure.className = kind + ' diagram-failed';
    figure.removeAttribute('data-pending');
    figure.replaceChildren(note, sourceBlock(source));
  }

  let said = 0;
  // A visually hidden description, read with the drawing it belongs to.
  function described(owner, text, cls) {
    const id = 'cairn-text-' + (++said);
    const note = document.createElement('p');
    note.className = cls;
    note.id = id;
    note.textContent = text;
    owner.setAttribute('aria-describedby', id);
    return note;
  }

  // Diagrams

  const kinds = /^(flowchart|graph|sequenceDiagram|gantt|classDiagram|stateDiagram(?:-v2)?|pie|journey|erDiagram|mindmap|timeline|gitGraph|quadrantChart|xychart(?:-beta)?|requirementDiagram|sankey(?:-beta)?|block(?:-beta)?|kanban|architecture(?:-beta)?|packet(?:-beta)?|radar(?:-beta)?|C4\w*)\b/;
  function kindOf(source) {
    for (const line of source.split('\n')) {
      const text = line.trim();
      if (!text || text.startsWith('%%') || text.startsWith('---')) continue;
      const found = text.match(kinds);
      if (found) return found[1].replace(/-(v2|beta)$/, '');
    }
    return '';
  }
  const lines = source => source.split('\n').map(line => line.trim()).filter(line => line && !line.startsWith('%%'));

  // Room a diagram is likely to take before it is drawn, so drawing it
  // later moves as little as possible.
  function estimate(source) {
    const kind = kindOf(source), rows = lines(source).length;
    let height = 280;
    if (kind === 'gantt') height = 120 + 32 * lines(source).filter(line => line.includes(':') && !/^(title|dateFormat|axisFormat|tickInterval|excludes|todayMarker)\b/.test(line)).length;
    else if (kind === 'sequenceDiagram') height = 140 + 46 * lines(source).filter(line => /--?[>x)]/.test(line)).length;
    else if (kind === 'flowchart' || kind === 'graph') height = /^\s*(flowchart|graph)\s+(LR|RL)/m.test(source) ? 220 : 40 + 115 * new Set([...source.matchAll(/(?:^|-->|---|==>|-\.->|\|)\s*([A-Za-z][\w-]*)\s*(?=[[({]|-->|---|==>|$)/gm)].map(m => m[1])).size;
    else if (kind === 'pie') height = 380;
    else if (kind === 'journey') height = 120 + 34 * rows;
    else height = 60 * Math.min(rows, 12);
    return Math.round(Math.min(Math.max(height, 160), 900));
  }

  function mermaidConfig(t, kind, width, source) {
    const pies = Object.fromEntries(t.series.map((colour, i) => ['pie' + (i + 1), colour]));
    const task = mix(t.accent, t.paper, t.dark ? 0.45 : 0.22), band = mix(t.ink, t.paper, t.dark ? 0.08 : 0.04);
    const narrow = width < 560;
    const sections = lines(source).filter(line => line.startsWith('section ')).map(line => line.slice(8).trim());
    return {
      startOnLoad: false,
      securityLevel: 'strict',
      theme: 'base',
      darkMode: t.dark,
      fontFamily: t.font,
      themeVariables: {
        darkMode: t.dark, background: t.paper, fontFamily: t.font, fontSize: '16px',
        primaryColor: t.tint, primaryTextColor: t.ink, primaryBorderColor: t.muted,
        secondaryColor: t.code, secondaryTextColor: t.ink, secondaryBorderColor: t.rule,
        tertiaryColor: t.paper, tertiaryTextColor: t.ink, tertiaryBorderColor: t.rule,
        mainBkg: t.tint, nodeBorder: t.muted, nodeTextColor: t.ink, textColor: t.ink, titleColor: t.ink,
        lineColor: t.muted, edgeLabelBackground: t.paper, clusterBkg: t.code, clusterBorder: t.rule,
        noteBkgColor: t.code, noteTextColor: t.ink, noteBorderColor: t.rule,
        actorBkg: t.tint, actorBorder: t.muted, actorTextColor: t.ink, actorLineColor: t.muted,
        signalColor: t.ink, signalTextColor: t.ink, labelBoxBkgColor: t.tint, labelBoxBorderColor: t.muted,
        labelTextColor: t.ink, loopTextColor: t.ink, activationBkgColor: t.code, activationBorderColor: t.muted,
        sequenceNumberColor: t.paper, pieStrokeColor: t.paper, pieOuterStrokeColor: t.rule,
        pieTitleTextColor: t.ink, pieSectionTextColor: t.ink, pieSectionTextSize: '19px', pieLegendTextColor: t.ink, pieOpacity: '1', ...pies,
        // Gantt: bands and bars from the report's own colours, light or dark.
        sectionBkgColor: band, altSectionBkgColor: t.paper, sectionBkgColor2: band, excludeBkgColor: t.code,
        taskBkgColor: task, taskBorderColor: t.accent, taskTextColor: t.ink, taskTextLightColor: t.ink, taskTextDarkColor: t.ink,
        taskTextOutsideColor: t.ink, taskTextClickableColor: t.accent, activeTaskBkgColor: mix(t.accent, t.paper, 0.5), activeTaskBorderColor: t.accent,
        doneTaskBkgColor: t.code, doneTaskBorderColor: t.muted, critBkgColor: mix(t.alert, t.paper, t.dark ? 0.45 : 0.22), critBorderColor: t.alert,
        gridColor: t.rule, todayLineColor: t.alert,
        // Journeys: the series colours, so faces and sections read on the paper.
        fillType0: mix(t.series[0], t.paper, 0.25), fillType1: mix(t.series[1], t.paper, 0.25), fillType2: mix(t.series[2], t.paper, 0.25), fillType3: mix(t.series[3], t.paper, 0.25),
        fillType4: mix(t.series[4], t.paper, 0.25), fillType5: mix(t.series[5], t.paper, 0.25), fillType6: mix(t.series[0], t.paper, 0.4), fillType7: mix(t.series[1], t.paper, 0.4),
        faceColor: t.paper
      },
      // Flat shapes: the report draws no shadows anywhere else.
      themeCSS: '.node rect, .node polygon, .node circle, .node path, .node .outer-path, .cluster rect, .actor, rect.note { filter: none !important; }' +
        // A title starts where the reader's eye does, not in the middle of a drawing that may scroll.
        '.titleText { text-anchor: start !important; }' +
        // Axis dates at the report's 12px floor, like every other label.
        '.tick text { font-size: 12px !important; }' +
        // Slice shares at a large-text size, coloured per slice once drawn (tidy).
        '.slice { font-weight: 600; }',
      flowchart: { htmlLabels: true, padding: 12, nodeSpacing: narrow ? 28 : 40, rankSpacing: narrow ? 32 : 40 },
      sequence: { wrap: true, width: narrow ? 120 : 150, actorMargin: narrow ? 24 : 50, actorFontFamily: t.font, noteFontFamily: t.font, messageFontFamily: t.font, messageFontSize: 15, actorFontSize: 15, noteFontSize: 14, mirrorActors: false },
      // Gantt charts draw to the width they are given. On a phone they keep
      // a readable width and scroll; dates read as "1 Mar".
      gantt: { useWidth: Math.max(width, narrow ? 600 : 0) - (gantts.get(width) || 0), fontSize: 12, sectionFontSize: 12, barHeight: 24, barGap: 8, topPadding: 48, rightPadding: 24, gridLineStartPadding: 32, leftPadding: Math.min(180, Math.max(72, 16 + 7.5 * Math.max(0, ...sections.map(name => name.length)))), axisFormat: '%-d %b', titleTopMargin: 24, numberSectionStyles: 2 },
      journey: { taskFontFamily: t.font, titleFontFamily: t.font, titleFontSize: '18px', taskFontSize: 14, leftMargin: narrow ? 90 : 110, width: 112, taskMargin: 24, diagramMarginX: 16, sectionFills: t.series.map(colour => mix(colour, t.paper, 0.32)), sectionColours: [t.ink], actorColours: t.series },
      // On a phone the legend goes under the pie, so the pie can use the width.
      pie: { textPosition: 0.72, legendPosition: narrow ? 'bottom' : 'right' }
    };
  }

  function collectDiagrams() {
    const blocks = [
      ...[...document.querySelectorAll('pre code.language-mermaid')].map(code => [code.closest('pre'), code.textContent]),
      ...[...document.querySelectorAll('.mermaid')].filter(el => !el.closest('.diagram')).map(el => [el, el.textContent])
    ];
    for (const [block, source] of blocks) {
      if (!block?.isConnected) continue;
      const figure = document.createElement('figure');
      figure.className = 'diagram';
      figure.setAttribute('data-pending', '');
      const scroll = document.createElement('div');
      scroll.className = 'diagram-scroll';
      scroll.dataset.scroll = '';
      scroll.setAttribute('role', 'group');
      scroll.setAttribute('aria-roledescription', 'diagram');
      scroll.setAttribute('aria-label', nearestHeading(block) || 'Diagram');
      // Its room is kept until it is drawn.
      scroll.style.minHeight = estimate(source) + 'px';
      const holder = document.createElement('div');
      holder.className = 'mermaid';
      scroll.append(holder);
      const hint = document.createElement('p');
      hint.className = 'diagram-hint';
      hint.textContent = 'Scroll sideways to see the whole diagram.';
      // Printed before it is drawn, the page shows the diagram's source.
      const kept = sourceBlock(source);
      kept.className = 'diagram-source';
      kept.removeAttribute('tabindex');
      kept.hidden = true;
      figure.append(scroll, hint, kept);
      sources.set(figure, source);
      block.replaceWith(figure);
      diagrams.push(figure);
      new ResizeObserver(() => overflowing(figure)).observe(figure);
    }
  }

  // Mermaid measures text in a scratch element. Left in the body it would grow
  // the page for a moment, and Cairn's reader never shrinks a frame back, so it
  // measures in a fixed box that takes up no room.
  let scratch;
  function measuringBox() {
    if (!scratch?.isConnected) {
      scratch = document.createElement('div');
      scratch.setAttribute('aria-hidden', 'true');
      scratch.style.cssText = 'position:fixed;left:0;top:0;width:100%;height:0;overflow:hidden;pointer-events:none';
      document.body.append(scratch);
    }
    return scratch;
  }

  // How much wider than asked a gantt chart comes out (its paddings), so the
  // next one can ask for less and land on the column exactly.
  const gantts = new Map();
  async function drawDiagram(figure, t, again) {
    const source = sources.get(figure), holder = figure.querySelector('.mermaid'), scroll = figure.querySelector('.diagram-scroll');
    if (!holder) return;
    const id = 'cairn-diagram-' + (++drawn), kind = kindOf(source), width = Math.round(scroll.clientWidth || figure.clientWidth || 600);
    try {
      mermaid.initialize(mermaidConfig(t, kind, width, source));
      const { svg, bindFunctions } = await mermaid.render(id, source, measuringBox());
      holder.innerHTML = svg;
      bindFunctions?.(holder);
      if (kind === 'gantt' && !again) {
        const drawnWidth = holder.querySelector('svg')?.viewBox?.baseVal?.width || 0, asked = Math.max(width, width < 560 ? 600 : 0);
        if (drawnWidth > asked + 1 && !gantts.has(width)) {
          gantts.set(width, Math.ceil(drawnWidth - asked));
          return drawDiagram(figure, t, true);
        }
      }
      figure.removeAttribute('data-pending');
      scroll.style.minHeight = '';
      scroll.tabIndex = 0;
      tidy(figure, kind, t);
      fit(figure);
      explain(figure, kind, source);
    } catch {
      document.getElementById(id)?.remove();
      document.getElementById('d' + id)?.remove();
      failed(figure, 'diagram', source, 'This diagram could not be drawn. Its source is below.');
    }
  }

  // After drawing: a title that starts at the left, and date labels that
  // never print over one another. Where they would, every other one goes.
  // Task names beside their bars sit on their row's colour, so the grid's
  // lines stop at a name instead of striking through it. Pie shares read on
  // their own slice, or beside the pie when the slice is too thin for them.
  function tidy(figure, kind, t) {
    const svg = figure.querySelector('.mermaid svg');
    if (!svg) return;
    figure.dataset.kind = kind;
    const title = svg.querySelector('.titleText');
    if (title && kind === 'gantt') title.setAttribute('x', '8');
    if (kind === 'pie') return tidyPie(svg, t);
    if (kind !== 'gantt') return;
    const rows = [...svg.querySelectorAll('rect.section')].map(rect => [rect.getBoundingClientRect(), getComputedStyle(rect).fill]);
    for (const label of svg.querySelectorAll('text.taskTextOutsideRight, text.taskTextOutsideLeft')) {
      const box = label.getBoundingClientRect(), middle = (box.top + box.bottom) / 2;
      const row = rows.find(([r]) => middle >= r.top && middle <= r.bottom);
      const fill = row && row[1] !== 'none' && !/rgba\(.*,\s*0\)$/.test(row[1]) ? row[1] : t.paper;
      const own = label.getBBox(), back = document.createElementNS('http://www.w3.org/2000/svg', 'rect');
      back.setAttribute('x', own.x - 3);
      back.setAttribute('y', own.y);
      back.setAttribute('width', own.width + 6);
      back.setAttribute('height', own.height);
      back.setAttribute('fill', fill);
      back.classList.add('task-label-back');
      label.before(back);
    }
    const ticks = [...svg.querySelectorAll('.tick text')].filter(text => text.textContent.trim());
    for (let step = 1; step < 8; step++) {
      const shown = ticks.filter((_, i) => i % step === 0);
      let last = -Infinity, clash = false;
      for (const text of shown) {
        const box = text.getBBox(), at = text.getCTM()?.e ?? 0;
        if (at - box.width / 2 < last + 8) { clash = true; break; }
        last = at + box.width / 2;
      }
      if (!clash) {
        ticks.forEach((text, i) => text.style.display = i % step === 0 ? '' : 'none');
        break;
      }
    }
  }

  // Each share takes ink or paper, whichever reads better on its slice, and
  // no halo. A share wider than its slice moves out beside the pie, in ink.
  function tidyPie(svg, t) {
    const slices = [...svg.querySelectorAll('path.pieCircle')], labels = [...svg.querySelectorAll('text.slice')];
    const outer = svg.querySelector('circle.pieOuterCircle'), radius = Number(outer?.getAttribute('r')) || 0;
    labels.forEach((label, i) => {
      const fill = slices[i] ? getComputedStyle(slices[i]).fill : t.paper;
      const at = (label.getAttribute('transform') || '').match(/translate\(\s*([-\d.e]+)[\s,]+([-\d.e]+)\s*\)/);
      label.style.stroke = 'none';
      label.style.fill = contrast(t.ink, fill) >= contrast(t.paper, fill) ? t.ink : t.paper;
      if (!at || !radius) return;
      const x = Number(at[1]), y = Number(at[2]), r = Math.hypot(x, y) || 1;
      const share = parseFloat(label.textContent) / 100, box = label.getBBox();
      // The arc a share's label sits on, against the label's own width.
      if (!(share > 0) || 2 * Math.PI * r * share >= box.width + 12) return;
      const out = (radius + 10 + Math.hypot(box.width, box.height) / 2) / r;
      label.setAttribute('transform', 'translate(' + (x * out).toFixed(1) + ',' + (y * out).toFixed(1) + ')');
      label.style.fill = t.ink;
      label.classList.add('slice-outside');
    });
    // A share moved beside the pie may land on the legend (under the pie on a
    // phone): the legend moves down by the overlap and the drawing grows.
    const legend = [...svg.querySelectorAll('g.legend')], box = svg.viewBox?.baseVal;
    const outside = [...svg.querySelectorAll('text.slice-outside')].map(label => label.getBoundingClientRect());
    if (!legend.length || !outside.length || !box?.width) return;
    const items = legend.map(item => item.getBoundingClientRect());
    const top = Math.min(...items.map(r => r.top)), bottom = Math.max(...items.map(r => r.bottom)), left = Math.min(...items.map(r => r.left)), right = Math.max(...items.map(r => r.right));
    let push = 0;
    for (const r of outside) if (r.bottom + 4 > top && r.top < bottom && r.right > left && r.left < right) push = Math.max(push, r.bottom + 8 - top);
    if (!push) return;
    const dy = push * box.width / (svg.getBoundingClientRect().width || box.width);
    for (const item of legend) {
      const at = (item.getAttribute('transform') || '').match(/translate\(\s*([-\d.e]+)[\s,]+([-\d.e]+)\s*\)/);
      if (at) item.setAttribute('transform', 'translate(' + at[1] + ',' + (Number(at[2]) + dy).toFixed(1) + ')');
    }
    box.height += dy;
  }

  // A diagram shrinks with the column until its smallest text would fall
  // under 12px, then holds that size and scrolls sideways instead.
  function fit(figure) {
    const svg = figure.querySelector('.mermaid svg');
    if (!svg) return;
    const box = svg.viewBox?.baseVal;
    const natural = box?.width || svg.getBBox().width;
    let smallest = Infinity;
    for (const el of svg.querySelectorAll('*')) {
      if (el.localName === 'style' || ![...el.childNodes].some(node => node.nodeType === 3 && node.data.trim())) continue;
      if (el.closest('[style*="display: none"]')) continue;
      const size = parseFloat(getComputedStyle(el).fontSize);
      if (size > 0) smallest = Math.min(smallest, size);
    }
    if (!Number.isFinite(smallest)) smallest = 16;
    const least = Math.ceil(natural * 12 / smallest);
    svg.removeAttribute('height');
    svg.setAttribute('width', Math.ceil(natural));
    svg.style.width = '100%';
    svg.style.minWidth = least + 'px';
    svg.style.maxWidth = Math.max(natural, least) + 'px';
    overflowing(figure);
  }

  function overflowing(figure) {
    const scroll = figure.querySelector('.diagram-scroll');
    if (scroll && !figure.hasAttribute('data-pending')) figure.toggleAttribute('data-overflow', scroll.scrollWidth > scroll.clientWidth + 1);
  }

  // A text alternative from what the diagram says: its steps and links, its
  // messages, its tasks or its slices, read after the diagram's name.
  function explain(figure, kind, source) {
    const scroll = figure.querySelector('.diagram-scroll'), svg = figure.querySelector('.mermaid svg');
    if (!scroll || !svg) return;
    figure.querySelector('.diagram-text')?.remove();
    const clean = text => (text || '').replace(/<br\s*\/?>/gi, ' ').replace(/\s+/g, ' ').trim();
    let text = '';
    if (kind === 'flowchart' || kind === 'graph') {
      const names = new Map();
      for (const node of svg.querySelectorAll('g.node')) {
        const key = node.dataset.id || (node.id.match(/(?:^|-)flowchart-(.+)-\d+$/) || [])[1];
        if (key) names.set(key, clean(node.querySelector('.nodeLabel')?.innerText || node.textContent));
      }
      const links = [];
      for (const line of lines(source)) {
        // A --> B, A -- text --> B, A -->|text| B, with optional shapes on either side.
        const found = line.match(/^([\w-]+)(?:\s*[[({>][^\n]*?[\])}])?\s*(?:--\s*([^->|][^>]*?)\s*-->|-->\|([^|]*)\||-\.->|==>|-->|---|-\.-)\s*([\w-]+)/);
        if (found) links.push(names.get(found[1]) + ' to ' + names.get(found[4]) + (clean(found[2] || found[3]) ? ' (' + clean(found[2] || found[3]) + ')' : ''));
      }
      text = 'Flowchart. Steps: ' + [...names.values()].join('; ') + '.' + (links.length && !links.some(link => link.includes('undefined')) ? ' Links: ' + links.join('; ') + '.' : '');
    } else if (kind === 'sequenceDiagram') {
      const messages = lines(source).map(line => line.match(/^([^-:>]+?)\s*--?(?:>>|>|x|\))\s*[+-]?\s*([^:]+?)\s*:\s*(.+)$/)).filter(Boolean).map(m => clean(m[1]) + ' to ' + clean(m[2]) + ': ' + clean(m[3]));
      text = 'Sequence. ' + messages.join('. ') + '.';
    } else if (kind === 'gantt') {
      const parts = [];
      for (const line of lines(source)) {
        if (line.startsWith('section ')) parts.push([line.slice(8).trim(), []]);
        else if (line.includes(':') && !/^(title|dateFormat|axisFormat|tickInterval|excludes|includes|todayMarker|weekday|accTitle|accDescr)\b/.test(line)) {
          if (!parts.length) parts.push(['', []]);
          parts[parts.length - 1][1].push(clean(line.split(':')[0]));
        }
      }
      text = 'Plan. ' + parts.map(([name, tasks]) => (name ? name + ': ' : '') + tasks.join(', ')).join('. ') + '.';
    } else if (kind === 'pie') {
      const slices = lines(source).map(line => line.match(/^"([^"]+)"\s*:\s*([\d.]+)/)).filter(Boolean);
      const total = slices.reduce((sum, m) => sum + Number(m[2]), 0) || 1;
      text = 'Pie chart. ' + slices.map(m => clean(m[1]) + ' ' + m[2] + ' (' + Math.round(Number(m[2]) / total * 100) + '%)').join(', ') + '.';
    }
    if (!text || text.length < 20) {
      const words = [];
      for (const el of svg.querySelectorAll('text, foreignObject')) {
        const said = clean(el.textContent);
        if (said && !words.includes(said)) words.push(said);
      }
      text = 'Diagram text: ' + words.slice(0, 120).join('; ') + '.';
    }
    figure.append(described(scroll, text.slice(0, 4000), 'diagram-text'));
  }

  // Which part of the page is being read, in page pixels. Cairn's reader
  // says so as it scrolls (the frame is at full height and never scrolls
  // itself); opened on its own, or in a frame that scrolls, the page knows.
  const framed = window.parent !== window;
  let view = null, waited = false;
  addEventListener('message', event => {
    const data = event.data;
    if (event.source !== window.parent || data?.type !== 'cairn:viewport' || !Number.isFinite(data.top) || !Number.isFinite(data.height)) return;
    view = { top: Math.max(0, data.top), height: Math.max(0, Math.min(data.height, 20000)) };
    soon();
  });
  function reading() {
    if (view) return [view.top, view.top + view.height];
    if (!framed || root.scrollHeight > innerHeight + 1) return [scrollY, scrollY + innerHeight];
    // At full height in a frame that has not said what is visible: everything, once it is clear no one will.
    return waited ? [0, Infinity] : null;
  }

  // Drawing runs one pass at a time, so when the colours change twice in
  // quick succession the last pass is the one left on the page.
  let drawing = Promise.resolve(), loading = null, checking = false;
  function library() {
    if (window.mermaid) return Promise.resolve(true);
    if (!later) return Promise.resolve(false);
    return loading ||= new Promise(resolve => {
      const script = document.createElement('script');
      script.src = later;
      script.dataset.lazy = '';
      script.onload = () => resolve(Boolean(window.mermaid));
      script.onerror = () => resolve(false);
      document.head.append(script);
    });
  }
  // Diagrams above the part being read, or within a screen and a half below
  // it, are drawn; the rest wait. Everything above is drawn too, so scrolling
  // back never meets a diagram changing size.
  function due(all) {
    const range = all ? [0, Infinity] : reading();
    if (!range) return [];
    const ahead = Math.max(1200, (range[1] - range[0]) * 1.5);
    return diagrams.filter(figure => figure.hasAttribute('data-pending') && !('queued' in figure.dataset) && (all || figure.getBoundingClientRect().top + scrollY < range[1] + ahead));
  }
  function drawDiagrams(next) {
    if (!next.length) return drawing;
    for (const figure of next) figure.dataset.queued = '';
    drawing = drawing.then(async () => {
      const ready = await library(), colours = tokens();
      for (const figure of next) {
        delete figure.dataset.queued;
        if (!figure.isConnected || !figure.querySelector('.mermaid')) continue;
        if (ready) await drawDiagram(figure, colours);
        else failed(figure, 'diagram', sources.get(figure), 'This diagram could not be drawn. Its source is below.');
        if (!figure.querySelector('.mermaid')) diagrams.splice(diagrams.indexOf(figure), 1);
      }
    }).catch(() => {});
    return drawing;
  }
  function soon() {
    if (checking) return;
    checking = true;
    requestAnimationFrame(() => { checking = false; drawDiagrams(due(false)); });
  }

  // Charts

  const list = value => Array.isArray(value) ? value : value ? [value] : [];
  const plain = value => value !== null && typeof value === 'object' && !Array.isArray(value);

  function chartTheme(t) {
    const axis = {
      axisLine: { lineStyle: { color: t.rule } },
      axisTick: { lineStyle: { color: t.rule } },
      axisLabel: { color: t.muted, fontSize: 12, fontFamily: t.font },
      splitLine: { lineStyle: { color: t.rule } },
      nameTextStyle: { color: t.muted, fontSize: 12, fontFamily: t.font }
    };
    return {
      color: t.series,
      backgroundColor: 'transparent',
      textStyle: { fontFamily: t.font, color: t.ink, fontSize: 12 },
      title: { textStyle: { color: t.ink, fontSize: 15, fontWeight: 600, fontFamily: t.font }, subtextStyle: { color: t.muted, fontSize: 12 } },
      legend: { textStyle: { color: t.ink, fontSize: 12, fontFamily: t.font }, inactiveColor: t.rule, pageTextStyle: { color: t.muted }, pageIconColor: t.accent, pageIconInactiveColor: t.rule },
      tooltip: { backgroundColor: t.paper, borderColor: t.rule, textStyle: { color: t.ink, fontSize: 13, fontFamily: t.font }, axisPointer: { lineStyle: { color: t.muted }, crossStyle: { color: t.muted }, shadowStyle: { color: t.tint } } },
      categoryAxis: { ...axis, splitLine: { show: false } },
      valueAxis: { ...axis, axisLine: { show: false }, axisTick: { show: false } },
      logAxis: { ...axis, axisLine: { show: false }, axisTick: { show: false } },
      timeAxis: { ...axis, splitLine: { show: false } },
      line: { symbol: 'circle', symbolSize: 6, lineStyle: { width: 2 } },
      pie: { itemStyle: { borderColor: t.paper, borderWidth: 2 } },
      dataZoom: { textStyle: { color: t.muted } },
      visualMap: { textStyle: { color: t.ink } }
    };
  }

  // No chart text under 12px, whatever the options ask for.
  function floorFonts(value) {
    if (!value || typeof value !== 'object') return;
    for (const key of Object.keys(value)) {
      if (key === 'fontSize' && typeof value[key] === 'number' && value[key] < 12) value[key] = 12;
      else floorFonts(value[key]);
    }
  }

  // How wide a line of chart text is, in the chart's own font.
  const ruler = document.createElement('canvas').getContext('2d');
  const measure = (text, font) => { ruler.font = '12px ' + font; return ruler.measureText(String(text)).width; };

  // The names a legend shows: its own list, or the series (or pie slices).
  function legendNames(options) {
    const legend = list(options.legend)[0];
    const given = list(legend?.data).map(item => plain(item) ? item.name : item).filter(name => name !== undefined && name !== null && name !== '');
    if (given.length) return given.map(String);
    const names = [];
    for (const series of list(options.series)) {
      if (/^(pie|funnel)$/.test(series.type)) for (const item of list(series.data)) { if (plain(item) && item.name) names.push(String(item.name)); }
      else if (series.name) names.push(String(series.name));
    }
    return names;
  }

  // Lines a wrapping legend takes at this width.
  function legendLines(names, width, font, legend) {
    const gap = typeof legend?.itemGap === 'number' ? legend.itemGap : 16, icon = typeof legend?.itemWidth === 'number' ? legend.itemWidth : 25;
    let rows = names.length ? 1 : 0, used = 0;
    for (const name of names) {
      const item = icon + 5 + measure(name, font);
      if (used && used + gap + item > width) { rows++; used = item; } else used += (used ? gap : 0) + item;
    }
    return rows;
  }

  // Charts written for a desktop column get phone-sized margins, fewer ticks
  // and labels that give way instead of overprinting. Legends wrap rather
  // than page, titles and legends keep apart, and axis names stay inside the
  // chart. Anything the author set is kept.
  function adapt(options, width, t) {
    const narrow = width < 560;
    floorFonts(options);
    if (options.animation === undefined && reduced.matches) options.animation = false;
    const cartesian = options.xAxis !== undefined || options.yAxis !== undefined;
    const title = list(options.title)[0], legend = list(options.legend)[0];
    const titled = Boolean(title?.text);
    if (options.tooltip === undefined) options.tooltip = {};
    for (const tooltip of list(options.tooltip)) {
      tooltip.confine ??= true;
      if (cartesian) tooltip.trigger ??= 'axis';
    }
    let top = 0;
    if (title) {
      title.left ??= 0;
      title.top ??= 0;
      // Flush with the text column, as headings are.
      title.padding ??= [0, 0, 4, 0];
      if (titled && title.top === 0) top = 28 + (title.subtext ? 20 : 0);
    }
    let legendHeight = 0;
    if (legend && legend.show !== false) {
      const names = legendNames(options);
      // A legend that wraps onto more lines rather than hiding series behind page arrows.
      if (legend.type === undefined || (narrow && legend.type === 'scroll' && legend.pageButtonPosition === undefined)) legend.type = 'plain';
      if (legend.orient === 'vertical' && narrow) Object.assign(legend, { orient: 'horizontal' });
      if (legend.orient !== 'vertical') {
        legend.itemGap ??= 16;
        legend.padding ??= [4, 0];
        legend.left ??= 0;
        if (legend.right === undefined && legend.width === undefined) legend.right = 0;
        if (legend.top === undefined && legend.bottom === undefined) legend.top = top ? top + 4 : 0;
        if (legend.top !== undefined && typeof legend.top === 'number') {
          const rows = legendLines(names, width, t.font, legend);
          legendHeight = rows ? rows * 20 + (rows - 1) * Math.max(0, legend.itemGap - 6) + 12 : 0;
          top = Math.max(top, legend.top + legendHeight);
        }
      }
    }
    options.cairnTop = top;
    if (cartesian) {
      if (options.grid === undefined) options.grid = {};
      const vertical = list(options.yAxis), named = vertical.some(axis => axis.name && (axis.nameLocation === undefined || axis.nameLocation === 'end'));
      for (const grid of list(options.grid)) {
        const untouched = grid.left === undefined && grid.right === undefined && grid.containLabel === undefined && grid.outerBoundsMode === undefined;
        const crowded = side => typeof grid[side] === 'number' ? grid[side] > width * 0.18 : typeof grid[side] === 'string' && parseFloat(grid[side]) > 18;
        if (untouched || (narrow && (crowded('left') || crowded('right')))) {
          // Labels and axis names both stay inside the chart (ECharts 6 keeps
          // only labels with containLabel), however long they are.
          delete grid.containLabel;
          grid.left = 0;
          grid.right = narrow ? 8 : 16;
          grid.outerBoundsMode = 'same';
          grid.outerBoundsContain = 'all';
        }
        grid.top ??= top + (named ? 8 : 12);
        grid.bottom ??= legend && legend.bottom !== undefined ? 40 : 8;
      }
      const grids = list(options.grid).length || 1;
      for (const axis of [...list(options.xAxis), ...list(options.yAxis)]) {
        axis.axisLabel ??= {};
        axis.axisLabel.hideOverlap ??= true;
        const values = axis.type === 'value' || axis.type === 'log' || (axis.type === undefined && !axis.data);
        if (values && axis.splitNumber === undefined) axis.splitNumber = narrow ? 3 : width < 800 ? 4 : undefined;
      }
      // A vertical axis name sits above the axis, starting at it, so it never
      // runs off the chart's left edge.
      for (const axis of vertical) {
        if (!axis.name || (axis.nameLocation !== undefined && axis.nameLocation !== 'end')) continue;
        axis.nameTextStyle ??= {};
        axis.nameTextStyle.align ??= 'left';
        axis.nameGap ??= 12;
      }
      // Category labels that would collide wrap onto a second line, or turn
      // when even a wrapped word would not fit.
      let wrapped = false;
      for (const axis of list(options.xAxis)) {
        if (axis.type !== 'category' || !Array.isArray(axis.data) || !axis.data.length || axis.axisLabel.rotate !== undefined || axis.axisLabel.width !== undefined) continue;
        const labels = axis.data.map(item => String(item?.value ?? item));
        const room = (width / grids - 64) / labels.length - 8;
        if (Math.max(...labels.map(label => measure(label, t.font))) <= room) continue;
        const word = Math.max(...labels.flatMap(label => label.split(/\s+/)).map(part => measure(part, t.font)));
        if (word <= room) {
          Object.assign(axis.axisLabel, { width: Math.floor(room), overflow: 'break', interval: 0, lineHeight: 15 });
          wrapped = true;
        } else if (labels.length <= 6) axis.axisLabel.rotate = 40;
        // Longer runs keep ECharts' own spacing, which skips labels evenly.
      }
      // Long category names on a horizontal bar chart wrap within a share of the width.
      for (const axis of vertical) {
        if (axis.type !== 'category' || !Array.isArray(axis.data) || axis.axisLabel.width !== undefined) continue;
        const room = Math.round(width * (narrow ? 0.42 : 0.32));
        if (Math.max(...axis.data.map(item => measure(String(item?.value ?? item), t.font))) > room) Object.assign(axis.axisLabel, { width: room, overflow: 'break', lineHeight: 15 });
      }
      // ECharts leaves room for one line of label; wrapped labels need two.
      if (wrapped) for (const grid of list(options.grid)) if (typeof grid.bottom === 'number') grid.bottom += 16;
    }
    const horizontal = list(options.yAxis).some(axis => axis.type === 'category');
    const colours = Array.isArray(options.color) ? options.color.length : t.series.length;
    for (const [index, series] of list(options.series).entries()) {
      // Past the palette's last colour, series repeat a colour: a dashed line
      // or a hatched bar keeps them apart.
      const repeat = index >= colours;
      if (series.type === 'bar') {
        series.barMaxWidth ??= 44;
        series.itemStyle ??= {};
        series.itemStyle.borderRadius ??= series.stack ? 0 : horizontal ? [0, 3, 3, 0] : [3, 3, 0, 0];
        if (repeat) series.itemStyle.decal ??= { symbol: 'rect', dashArrayX: [1, 0], dashArrayY: [3, 3], rotation: -0.8, color: t.dark ? 'rgba(0, 0, 0, 0.45)' : 'rgba(255, 255, 255, 0.55)' };
      } else if (series.type === 'line' && repeat) {
        series.lineStyle ??= {};
        series.lineStyle.type ??= 'dashed';
        series.symbol ??= 'diamond';
      } else if (series.type === 'pie' && series.label === undefined) {
        if (narrow) {
          // On a phone the legend names the slices; each slice shows its share,
          // and slices too small to hold it stay quiet.
          // Ink on a paper halo reads on every slice colour.
          series.label = { position: 'inside', formatter: '{d}%', color: t.ink, textBorderColor: t.paper, textBorderWidth: 3, fontSize: 12, fontWeight: 600 };
          series.percentPrecision ??= 0;
          series.labelLine ??= { show: false };
          series.labelLayout ??= { hideOverlap: true };
          series.minShowLabelAngle ??= 18;
        } else {
          // Slice names sit at the chart's edges and wrap there, so none is cut short.
          series.label = { alignTo: 'edge', edgeDistance: 4, overflow: 'break', width: Math.round(Math.max(72, width * 0.18)) };
          series.labelLine ??= { length2: 4 };
        }
      }
    }
    return options;
  }

  // Height follows the data: a row per category for horizontal bars, a
  // squarer box for pies, a landscape box for everything else, plus the
  // room the title and a wrapped legend take.
  function chartHeight(options, width) {
    const narrow = width < 560;
    const series = list(options.series);
    const rows = list(options.yAxis).find(axis => axis.type === 'category');
    const round = series.length && series.every(s => /^(pie|gauge|funnel|radar|sunburst|treemap)$/.test(s.type));
    let height;
    if (rows) height = Math.max(Array.isArray(rows.data) ? rows.data.length : 0, ...series.map(s => list(s.data).length)) * (narrow ? 40 : 38) + 48;
    else if (round) height = Math.min(width, 440) * (narrow ? 0.9 : 0.75);
    else height = Math.min(width * (narrow ? 0.72 : 0.5), 460);
    height += options.cairnTop || 0;
    return Math.round(Math.min(Math.max(height, 240), 900));
  }

  // A pie under a title and a wrapped legend moves down and keeps inside.
  function placeRound(options, width, height) {
    const top = options.cairnTop || 0;
    if (!top) return;
    for (const series of list(options.series)) {
      if (!/^(pie|funnel|gauge|radar|sunburst)$/.test(series.type) || series.center !== undefined) continue;
      const room = height - top - 8, size = Math.min(width * (width < 560 ? 0.9 : 0.7), room) / 2;
      series.center = ['50%', Math.round(top + 4 + room / 2)];
      const scale = value => typeof value === 'string' && value.endsWith('%') ? Math.round(parseFloat(value) / 100 * size) : value;
      if (series.radius !== undefined) series.radius = Array.isArray(series.radius) ? series.radius.map(scale) : scale(series.radius);
      else if (series.type === 'pie') series.radius = Math.round(size * 0.8);
    }
  }

  // The chart's data as a table only screen readers see, with a sentence
  // that says what kind of chart it is.
  function dataTable(options, name) {
    const series = list(options.series).filter(plain);
    if (!series.length) return null;
    const number = item => plain(item) ? item.value : item;
    const cell = value => Array.isArray(value) ? value.join(', ') : value === undefined || value === null ? '' : String(value);
    let head = [], rows = [];
    const categoryAxis = [...list(options.xAxis), ...list(options.yAxis)].find(axis => axis.type === 'category' && Array.isArray(axis.data));
    if (series.every(s => /^(pie|funnel)$/.test(s.type))) {
      head = ['Name', 'Value'];
      for (const s of series) for (const item of list(s.data)) if (plain(item)) rows.push([item.name ?? '', cell(item.value)]);
    } else if (categoryAxis) {
      head = [categoryAxis.name || '', ...series.map((s, i) => s.name || (series.length > 1 ? 'Series ' + (i + 1) : 'Value'))];
      rows = categoryAxis.data.map((category, i) => [cell(plain(category) ? category.value : category), ...series.map(s => cell(number(list(s.data)[i])))]);
    } else {
      head = ['Series', 'Values'];
      for (const [i, s] of series.entries()) for (const item of list(s.data).slice(0, 200)) rows.push([s.name || 'Series ' + (i + 1), cell(number(item))]);
    }
    if (!rows.length) return null;
    // A table cannot be narrower than its words, so it sits in a box that can.
    const box = document.createElement('div');
    box.className = 'chart-data';
    const table = document.createElement('table');
    box.append(table);
    const caption = table.createCaption();
    caption.textContent = 'Data for ' + name;
    const headRow = table.createTHead().insertRow();
    for (const label of head) { const th = document.createElement('th'); th.scope = 'col'; th.textContent = label; headRow.append(th); }
    const tbody = table.createTBody();
    for (const values of rows.slice(0, 400)) {
      const row = tbody.insertRow();
      values.forEach((value, i) => { const c = document.createElement(i ? 'td' : 'th'); if (!i) c.scope = 'row'; c.textContent = value; row.append(c); });
    }
    const kinds = [...new Set(series.map(s => s.type || 'chart'))].join(' and ');
    return { table: box, summary: kinds.charAt(0).toUpperCase() + kinds.slice(1) + ' chart' + (series.length > 1 ? ' with ' + series.length + ' series' : '') + '. Its data follows as a table.' };
  }

  function collectCharts() {
    for (const code of document.querySelectorAll('pre code.language-echarts')) {
      const pre = code.closest('pre'), source = code.textContent;
      const figure = document.createElement('figure');
      let options;
      try {
        options = JSON.parse(source);
        if (!plain(options)) throw Error('not an object');
      } catch {
        pre.replaceWith(figure);
        failed(figure, 'chart', source, 'This chart could not be drawn because its options are not valid JSON. Its data is below.');
        continue;
      }
      figure.className = 'chart';
      const canvas = document.createElement('div');
      canvas.className = 'chart-canvas';
      canvas.setAttribute('role', 'img');
      canvas.setAttribute('aria-roledescription', 'chart');
      const name = list(options.title)[0]?.text || nearestHeading(pre) || 'Chart';
      canvas.setAttribute('aria-label', name);
      figure.append(canvas);
      const data = dataTable(options, name);
      if (data) figure.append(described(canvas, data.summary, 'chart-summary'), data.table);
      pre.replaceWith(figure);
      const entry = { figure, canvas, source, width: 0, chart: null, theme: '' };
      charts.push(entry);
      let pending = false;
      new ResizeObserver(() => {
        if (pending) return;
        pending = true;
        requestAnimationFrame(() => { pending = false; drawChart(entry); });
      }).observe(figure);
    }
  }

  let theme = '', themeTokens = null;
  function useTheme(t) {
    theme = 'cairn-' + (t.dark ? 'dark' : 'light') + '-' + t.series.join('').replace(/[^0-9a-f]/gi, '').slice(0, 24);
    themeTokens = t;
    echarts.registerTheme(theme, chartTheme(t));
  }

  function drawChart(entry) {
    const width = Math.round(entry.figure.clientWidth);
    if (!width || (width === entry.width && entry.theme === theme)) return;
    entry.width = width;
    let options;
    try {
      options = adapt(JSON.parse(entry.source), width, themeTokens || tokens());
      const height = chartHeight(options, width);
      placeRound(options, width, height);
      delete options.cairnTop;
      entry.canvas.style.height = height + 'px';
      if (entry.theme !== theme) {
        entry.chart?.dispose();
        entry.chart = echarts.init(entry.canvas, theme, { renderer: 'canvas' });
        entry.theme = theme;
      } else {
        entry.chart.resize();
      }
      entry.chart.setOption(options, true);
    } catch {
      entry.chart?.dispose();
      charts.splice(charts.indexOf(entry), 1);
      failed(entry.figure, 'chart', entry.source, 'This chart could not be drawn. Its data is below.');
    }
  }

  function drawCharts(t) {
    if (!window.echarts || !charts.length) return;
    useTheme(t);
    for (const entry of [...charts]) drawChart(entry);
  }

  // Pages saved before Cairn's current templates, and custom HTML reports, do
  // not carry the template stylesheet. These rules have no specificity, so a
  // template's own rules always win over them.
  function baseStyles() {
    const style = document.createElement('style');
    // Text alternatives are for screen readers only, whatever the page's own table or paragraph rules say.
    const hidden = ['position: absolute', 'width: 1px', 'min-width: 0', 'max-width: 1px', 'height: 1px', 'margin: 0', 'padding: 0', 'overflow: hidden', 'clip-path: inset(50%)', 'white-space: nowrap', 'border: 0'].map(rule => rule + ' !important').join('; ');
    style.textContent = ':where(figure.diagram, figure.chart) { margin: 1.5rem 0; }' +
      ':where(.diagram-scroll) { overflow-x: auto; }' +
      ':where(.diagram svg) { display: block; height: auto; margin: 0 auto; }' +
      ':where(.diagram-hint) { display: none; margin: 0.5rem 0 0; font: 13px/1.45 system-ui, sans-serif; }' +
      ':where(.diagram[data-overflow] .diagram-hint) { display: block; }' +
      ':where(.diagram-note) { margin: 0 0 0.5rem; font: 14px/1.45 system-ui, sans-serif; }' +
      ':where(.diagram-failed pre) { overflow-x: auto; }' +
      '.diagram-text, .chart-summary, .chart-data { ' + hidden + ' }' +
      // A diagram waiting to be drawn keeps its room quietly; printed, it shows its source.
      ':where(.diagram[data-pending] .diagram-scroll) { background: none; }' +
      // Edge shadows belong only to a diagram that scrolls; otherwise a sliver
      // of them shows as a hairline frame at its edges.
      '.diagram:not([data-overflow]) > .diagram-scroll { background: none; }' +
      '@media print { :where(.diagram[data-pending] .diagram-scroll) { display: none; } :where(.diagram[data-pending] > .diagram-source[hidden]) { display: block; white-space: pre-wrap; } }';
    document.head.append(style);
  }

  function start() {
    baseStyles();
    if (window.echarts) collectCharts();
    if (window.mermaid || later) collectDiagrams();
    const t = tokens();
    drawCharts(t);
    drawDiagrams(due(false));
    // A frame at full height that never hears what is visible draws everything.
    setTimeout(() => { waited = true; soon(); }, 1200);
    addEventListener('scroll', soon, { passive: true });
    addEventListener('resize', soon);
    // Printing draws every diagram it can in time; what is left prints as source.
    addEventListener('beforeprint', () => drawDiagrams(due(true)));
    // A report can switch scheme or palette later, or follow the device as
    // it changes; redraw in the new colours.
    const redraw = () => {
      drawCharts(tokens());
      drawDiagrams(diagrams.filter(figure => !figure.hasAttribute('data-pending') && !('queued' in figure.dataset)));
    };
    new MutationObserver(redraw).observe(root, { attributes: true, attributeFilter: ['data-report-scheme', 'data-report-palette'] });
    window.matchMedia?.('(prefers-color-scheme: dark)').addEventListener?.('change', () => { if (root.dataset.reportScheme === 'auto') redraw(); });
  }

  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', start);
  else start();
})();
