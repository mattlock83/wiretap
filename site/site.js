(() => {
  const tablist = document.querySelector('.demo-tabs');
  if (tablist) {
    const tabs = [...tablist.querySelectorAll('[role="tab"]')];
    const selectTab = (selected, focus = false) => {
      for (const tab of tabs) {
        const active = tab === selected;
        tab.setAttribute('aria-selected', String(active));
        tab.tabIndex = active ? 0 : -1;
        document.getElementById(tab.getAttribute('aria-controls')).hidden = !active;
      }
      if (focus) selected.focus();
    };
    tablist.hidden = false;
    for (const tab of tabs) {
      tab.addEventListener('click', () => selectTab(tab));
      tab.addEventListener('keydown', event => {
        let index = tabs.indexOf(tab);
        if (event.key === 'ArrowRight') index = (index + 1) % tabs.length;
        else if (event.key === 'ArrowLeft') index = (index + tabs.length - 1) % tabs.length;
        else if (event.key === 'Home') index = 0;
        else if (event.key === 'End') index = tabs.length - 1;
        else return;
        event.preventDefault();
        selectTab(tabs[index], true);
      });
    }
  }

  for (const button of document.querySelectorAll('[data-copy]')) {
    const code = document.getElementById(button.dataset.copy);
    const status = button.closest('.quickstart').querySelector('[role="status"]');
    if (!code || !status) continue;
    button.hidden = false;
    button.addEventListener('click', async () => {
      try {
        if (!navigator.clipboard?.writeText) throw new Error('Clipboard unavailable');
        await navigator.clipboard.writeText(code.textContent.trim());
        status.textContent = 'Commands copied.';
      } catch {
        const selection = window.getSelection();
        if (!selection) {
          status.textContent = 'Select the commands and copy them manually.';
          return;
        }
        const range = document.createRange();
        range.selectNodeContents(code);
        selection.removeAllRanges();
        selection.addRange(range);
        status.textContent = 'Commands selected. Press Ctrl+C or ⌘C to copy.';
      }
    });
  }
})();
