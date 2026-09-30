// 应用投屏条纯函数（浏览器/Node 双端可用）：
//   设备页设备卡底部的「N 个应用正在投屏 + 全部关闭」条（v2.1.58）。
//   按身份聚合属于该设备的应用窗口数据键（同设备多形态键归一，照抄 v2.1.52
//   身份归一语义：直配键优先 → identity 兜底；identity 为空只做直配、不误配）。
// 纯函数：无 DOM、无闭包副作用——node 单测覆盖（appwin_bar_test.js）。
(function (root, factory) {
  if (typeof module === 'object' && module.exports) {
    module.exports = factory();
  } else {
    root.AppWinBar = factory();
  }
})(typeof self !== 'undefined' ? self : this, function () {
  'use strict';

  // keysForDevice：聚合属于该设备的应用窗口数据键（只收有卡片的键）。
  //   cards       = openAppCards 表（键 → 卡片数组）
  //   directKeys  = 该设备的直配键（serial / wireless / 会话键）
  //   devIdentity = 设备身份（市场名；空=只做直配匹配，不做身份回退）
  //   identityOf  = 键 → 身份 查询回调（浏览器端注入 deviceIdentityOf）
  // 返回键数组（顺序=cards 键序；空表/无匹配=[]）。
  function keysForDevice(cards, directKeys, devIdentity, identityOf) {
    var out = [];
    if (!cards) return out;
    var direct = directKeys || [];
    Object.keys(cards).forEach(function (k) {
      if (!(cards[k] || []).length) return;
      if (direct.indexOf(k) >= 0) { out.push(k); return; }
      if (devIdentity && typeof identityOf === 'function' && identityOf(k) === devIdentity) out.push(k);
    });
    return out;
  }

  // countFor：合计这些键下的应用窗口数。closing 中的窗口仍计入——
  // 条子数字与卡片数一致，逐个摘除时自然递减、清空后条子消失。
  function countFor(cards, keys) {
    var n = 0;
    (keys || []).forEach(function (k) { n += ((cards || {})[k] || []).length; });
    return n;
  }

  // suffix：文案后缀（数字部分单独加粗——前端 <b>N</b> + suffix 组装）。
  var SUFFIX = ' 个应用正在投屏';

  // label：条子文案（当前有多少个应用正在投屏）。
  function label(n) {
    return n + SUFFIX;
  }

  // jumpAction：设备卡点击行为（v2.1.59）——主投屏在 → 'cast'（窗口总览页，既有
  // 语义）；只有应用投屏 → 'appwin'（该设备的应用窗口标签页）；无投屏 → null（不跳）。
  function jumpAction(casting, appWinCount) {
    if (casting) return 'cast';
    if (appWinCount > 0) return 'appwin';
    return null;
  }

  // allClosing：这些键下的应用窗口是否全部处于关闭中（closing/leaving；无窗口=false）。
  // v2.1.60：设备页条子据此把「全部关闭」按钮换成「正在关闭…」灰标签（遮罩语义）。
  function allClosing(cards, keys) {
    var total = countFor(cards, keys);
    if (total <= 0) return false;
    var closing = 0;
    (keys || []).forEach(function (k) {
      ((cards || {})[k] || []).forEach(function (a) {
        if (a && (a.closing || a.leaving)) closing++;
      });
    });
    return closing === total;
  }

  // appendExtraKeys：把补充键（设备档案的 serials ∪ addrs）并入直配键数组
  //（去重；前段保序；过滤空值）。v2.1.64：插拔形态切换后，应用窗口的键可能还是旧
  // 形态键（如 USB 序列号）；档案键=权威全量映射（active IP + USB serial），并入后
  // 条子/跳转匹配跨形态保持（配合 app.js 的 profileKeysFor）。
  function appendExtraKeys(direct, extra) {
    var out = (direct || []).slice();
    (extra || []).forEach(function (k) {
      if (k && out.indexOf(k) < 0) out.push(k);
    });
    return out;
  }

  return { keysForDevice: keysForDevice, countFor: countFor, label: label, suffix: SUFFIX, jumpAction: jumpAction, allClosing: allClosing, appendExtraKeys: appendExtraKeys };
});
