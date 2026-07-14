// CCVAR CRM 后台交互：只做两件小事，其余全靠原生表单。
(function () {
  // 复制草稿 / 密钥
  document.addEventListener('click', function (e) {
    var btn = e.target.closest('[data-copy]');
    if (!btn) return;
    var pre = btn.parentElement.querySelector('pre');
    if (!pre) return;
    navigator.clipboard.writeText(pre.innerText).then(function () {
      var old = btn.textContent;
      btn.textContent = '已复制 ✓';
      setTimeout(function () { btn.textContent = old; }, 1500);
    });
  });

  // flash 自动淡出
  var flash = document.querySelector('.flash:not(.flash-err)');
  if (flash) {
    setTimeout(function () {
      flash.style.opacity = '0';
      setTimeout(function () { flash.remove(); }, 700);
    }, 4000);
  }
})();
