document.addEventListener('DOMContentLoaded', async () => {
  if (window.mermaid) {
    for (const block of document.querySelectorAll('pre code.language-mermaid')) {
      const diagram = document.createElement('pre');
      diagram.className = 'mermaid'; diagram.textContent = block.textContent;
      block.parentElement.replaceWith(diagram);
    }
    mermaid.initialize({ startOnLoad: false, securityLevel: 'strict', theme: 'neutral' });
    try { await mermaid.run(); } catch { /* Mermaid displays its parse error in the report. */ }
  }
  if (window.echarts) {
    for (const block of document.querySelectorAll('pre code.language-echarts')) {
      try {
        const options = JSON.parse(block.textContent);
        const container = document.createElement('div');
        container.style.cssText = 'width:100%;height:360px;min-width:0';
        container.setAttribute('role', 'img'); container.setAttribute('aria-label', 'Interactive chart');
        block.parentElement.replaceWith(container);
        const chart = echarts.init(container); chart.setOption(options);
        new ResizeObserver(() => chart.resize()).observe(container);
      } catch { block.textContent = 'This chart could not be rendered. Check its JSON options.'; }
    }
  }
});
