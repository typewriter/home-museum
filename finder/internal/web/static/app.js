// finder のフロントは素の JS のみ。フォーム送信は通常の GET なので、
// ここでやるのは操作の手数を減らすことだけ。
(function () {
  'use strict';

  // ページ送りやソートを除く <select> は、選んだ時点で検索し直す。
  document.querySelectorAll('form.filters select').forEach(function (el) {
    el.addEventListener('change', function () {
      // ページ番号は条件を変えたら 1 に戻す。
      var form = el.form;
      if (form) {
        var page = form.querySelector('input[name=page]');
        if (page) page.value = '1';
        form.submit();
      }
    });
  });

  // "/" で検索欄へ。入力中は無効。
  document.addEventListener('keydown', function (e) {
    if (e.key !== '/' || e.metaKey || e.ctrlKey || e.altKey) return;
    var t = e.target;
    if (t && (t.tagName === 'INPUT' || t.tagName === 'TEXTAREA' || t.isContentEditable)) return;
    var box = document.querySelector('form.filters input[type=search]');
    if (box) {
      e.preventDefault();
      box.focus();
      box.select();
    }
  });

  // SQL コンソールは Ctrl+Enter / Cmd+Enter で実行。
  var sql = document.querySelector('form.sqlform textarea');
  if (sql) {
    sql.addEventListener('keydown', function (e) {
      if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) {
        e.preventDefault();
        sql.form.submit();
      }
    });
  }

  // 表のセルはクリックで全選択する。ID や person_key をコピーしやすくするため。
  document.querySelectorAll('table.grid').forEach(function (table) {
    table.addEventListener('dblclick', function (e) {
      var td = e.target.closest('td');
      if (!td) return;
      var range = document.createRange();
      range.selectNodeContents(td);
      var sel = window.getSelection();
      sel.removeAllRanges();
      sel.addRange(range);
    });
  });
})();
