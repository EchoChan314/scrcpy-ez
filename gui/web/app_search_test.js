// app_search.js 单测（node app_search_test.js）：
// 名称子串/包名（原有能力保留）· 拼音（全拼/前缀/首字母/混拼/多音字）· 降级 · 空查询。
(function () {
  'use strict';
  global.pinyinPro = require('./pinyin_pro.js');
  var AS = require('./app_search.js');
  var ok = 0, fail = 0;
  function t(name, cond) {
    if (cond) { ok++; console.log('ok   ' + name); }
    else { fail++; console.log('FAIL ' + name); }
  }

  var apps = [
    { name: '文件', pkg: 'com.android.documentsui' },
    { name: '设置', pkg: 'com.android.settings' },
    { name: '微信', pkg: 'com.tencent.mm' },
    { name: '哔哩哔哩', pkg: 'tv.danmaku.bili' },
    { name: 'Chrome', pkg: 'com.android.chrome' },
    { name: '360安全卫士', pkg: 'com.qihoo360.mobilesafe' },
    { name: '重庆生活', pkg: 'com.cq.life' },
  ];
  function hit(q) {
    return AS.filterApps(apps, q).map(function (a) { return a.name; });
  }

  // ① 名称子串（原有能力）
  t('中文名子串', hit('文件').indexOf('文件') >= 0);
  t('英文名子串大小写不敏感', hit('chrome').indexOf('Chrome') >= 0 && hit('CHROME').indexOf('Chrome') >= 0);
  t('英文名片段 ome', hit('ome').indexOf('Chrome') >= 0);

  // ② 包名子串（主人点名保留的能力）
  t('包名片段 settings（不出现于名称）', hit('settings').indexOf('设置') >= 0);
  t('包名片段 tencent', hit('tencent').indexOf('微信') >= 0);
  t('包名片段 danmaku', hit('danmaku').indexOf('哔哩哔哩') >= 0);

  // ③ 拼音（本次新增）
  t('全拼 wenjian → 文件', hit('wenjian').indexOf('文件') >= 0);
  t('前缀 wenj → 文件（主人案例）', hit('wenj').indexOf('文件') >= 0);
  t('逐键中间态无闪烁', ['w', 'we', 'wen', 'wenj', 'wenji', 'wenjia', 'wenjian'].every(function (k) {
    return hit(k).indexOf('文件') >= 0;
  }));
  t('首字母 wj → 文件', hit('wj').indexOf('文件') >= 0);
  t('后半全拼 jian → 文件', hit('jian').indexOf('文件') >= 0);
  t('首字母 sz → 设置', hit('sz').indexOf('设置') >= 0);
  t('混拼 weix → 微信', hit('weix').indexOf('微信') >= 0);
  t('混拼 blbl → 哔哩哔哩', hit('blbl').indexOf('哔哩哔哩') >= 0);
  t('多音字 chongqing → 重庆生活', hit('chongqing').indexOf('重庆生活') >= 0);
  t('多音字 zhongqing → 重庆生活（宽容）', hit('zhongqing').indexOf('重庆生活') >= 0);

  // 反例（不应误命中）
  t('反例 wenx 不中文件', hit('wenx').indexOf('文件') < 0);
  t('反例 wx 不中文件（j 开头约束）', hit('wx').indexOf('文件') < 0);
  t('反例 超长 wenjianli 不中', hit('wenjianli').indexOf('文件') < 0);

  // ④ 数字查询（不触发拼音分支，名称子串命中）
  t('数字查询 360', hit('360').indexOf('360安全卫士') >= 0);
  t('数字查询 360 不重（键控）', hit('360').length === 1);

  // ⑤ 空查询=全量副本，不改入参
  t('空查询全量', AS.filterApps(apps, '').length === apps.length);
  t('空查询 null', AS.filterApps(apps, null).length === apps.length);
  t('空查询不改入参', apps.length === 7);
  t('入参为 null 不炸', AS.filterApps(null, 'a').length === 0);
  t('元素为 null 不炸', AS.filterApps([null, apps[0]], '文件').length === 1);

  // ⑥ 降级：pinyinPro 缺失时 ①②③ 中的拼音分支自动跳过，其余照常
  var saved = global.pinyinPro;
  delete global.pinyinPro;
  t('降级：无拼音库时拼音查询无结果', AS.filterApps(apps, 'wenjian').length === 0);
  t('降级：无拼音库时名称/包名仍可', AS.filterApps(apps, '文件').length === 1 && AS.filterApps(apps, 'settings').length === 1);
  global.pinyinPro = saved;

  console.log('\n' + ok + ' ok, ' + fail + ' fail');
  if (fail > 0) process.exit(1);
})();
