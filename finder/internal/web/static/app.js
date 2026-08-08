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

  // 未取得の画像を追いかける。
  //
  // サーバーは取得待ちでも 202 + プレースホルダを即返すので、<img> は
  // 「読み込み成功」として扱われる。したがって onerror では検知できず、
  // 状態エンドポイントを見に行く必要がある。
  // 館ごと 10 秒に 1 回しか進まないので、間隔は控えめでよい。
  var pending = Array.prototype.slice.call(
    document.querySelectorAll('img[data-image-id]'));
  if (pending.length) {
    var tries = 0;
    var poll = function () {
      if (!pending.length || tries > 120) return;
      tries++;
      var next = [];
      var checks = pending.map(function (img) {
        var id = img.dataset.imageId;
        return fetch('/img/' + id + '/' + img.dataset.width + '/status', {
          headers: { 'Accept': 'application/json' }
        }).then(function (r) { return r.json(); }).then(function (st) {
          var note = document.querySelector('.imgnote[data-for="' + id + '"]');
          if (st.ready) {
            // キャッシュ回避のため一度だけクエリを付け直す。
            img.src = img.src.split('?')[0] + '?v=' + Date.now();
            if (note) note.textContent =
              '取得済み ' + st.width + '×' + st.height + ' / ' +
              (st.bytes / 1024).toFixed(0) + ' KB';
            return;
          }
          if (st.state === 'gone') {
            if (note) note.textContent = '取得できません: ' + (st.error || '');
            return;
          }
          if (note) {
            note.textContent = st.state === 'failed'
              ? '取得に失敗、再試行待ち (' + st.attempts + ' 回目): ' + (st.error || '')
              : '取得待ち — 同じ館のキューにあと ' + st.ahead + ' 枚 (約 ' +
                Math.ceil((st.eta_sec || 0) / 60) + ' 分)';
          }
          next.push(img);
        }).catch(function () { next.push(img); });
      });
      Promise.all(checks).then(function () {
        pending = next;
        if (pending.length) setTimeout(poll, 5000);
      });
    };
    setTimeout(poll, 1500);
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
