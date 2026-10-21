// The repo picker (repos.html): the free-text input shows only for
// "other…", and the ref field's placeholder names the chosen repo's
// default branch. Without JS both fields show and the server still
// reads them the same way.
(() => {
  for (const pick of document.querySelectorAll('select.repo-pick')) {
    const form = pick.form;
    const other = form.querySelector('input.repo-other');
    const ref = form.querySelector('input[name=ref]');
    const sync = () => {
      const isOther = pick.value === 'other';
      other.hidden = !isOther;
      if (isOther) other.focus();
      const def = pick.selectedOptions[0] && pick.selectedOptions[0].dataset.default;
      if (ref) ref.placeholder = def ? `ref (default: ${def})` : 'ref (default branch)';
    };
    pick.addEventListener('change', sync);
    sync();
  }
})();
