// app_search.js —— 应用列表搜索过滤（应用窗口浮窗用）。
// 匹配规则（任一命中即保留，保持列表原顺序）：
//   ① 应用名子串（中文/英文/数字，不区分大小写）——原有能力
//   ② 包名子串（com.xxx.yyy 任意片段）——原有能力（主人点名保留）
//   ③ 拼音模糊（仅纯字母/空格查询，需全局 pinyinPro）：
//      全拼/首字母/混拼/部分前缀皆可——「文件」← wenjian / wenj / wenji / wj / jian
//      多音字按任一读音匹配（重庆 ← chongqing 或 zhongqing）。
//   拼音库缺失时自动降级为 ①②（搜索仍可用）。
// 纯函数 node 可单测（app_search_test.js；测试注入 global.pinyinPro）。
(function (root, factory) {
  'use strict';
  if (typeof module === 'object' && module.exports) {
    module.exports = factory();
  } else {
    root.SCEZAppSearch = factory();
  }
})(typeof self !== 'undefined' ? self : this, function () {
  'use strict';

  var ALPHA_Q = /^[a-z ]+$/; // 纯字母（可含空格）才尝试拼音匹配

  // 取拼音匹配器（全局 pinyinPro；不存在=null，自动降级）。
  function pinyinMatcher() {
    try {
      if (typeof pinyinPro !== 'undefined' && pinyinPro && typeof pinyinPro.match === 'function') {
        return pinyinPro;
      }
    } catch (e) { /* 未定义=降级 */ }
    return null;
  }

  // matchesApp：query（调用方需已小写）是否命中单个应用 {name, pkg}。
  // py = 拼音匹配器（可为 null=跳过拼音分支）。
  function matchesApp(a, q, py) {
    if (!a) return false;
    var name = (a.name || '').toLowerCase();
    var pkg = (a.pkg || '').toLowerCase();
    if (name.indexOf(q) >= 0 || pkg.indexOf(q) >= 0) return true;
    if (py) {
      try {
        if (py.match(a.name || '', q)) return true;
      } catch (e) { /* 单个应用匹配出错=跳过 */ }
    }
    return false;
  }

  // filterApps：按查询过滤应用列表（空查询=全量副本，不改入参）。
  function filterApps(list, q) {
    list = list || [];
    q = (q == null ? '' : String(q)).trim().toLowerCase();
    if (!q) return list.slice();
    var py = ALPHA_Q.test(q) ? pinyinMatcher() : null;
    return list.filter(function (a) { return matchesApp(a, q, py); });
  }

  return { filterApps: filterApps, matchesApp: matchesApp };
});
