// 应用窗口卡片状态文字（浏览器/Node 双端可用的纯函数）：
//   closing        → "正在关闭"；
//   phaseText 非空 → 阶段文字（后端下发，与主投屏同款措辞）；
//   稳态（默认）    → "正在窗口" + 连接形态后缀（"正在窗口 · 有线模式" / "正在窗口 · 无线模式"，
//                    措辞与主投屏 modeTitle 对齐；形态未知/为空 → 只显示"正在窗口"）。
// 纯函数：无 DOM、无闭包副作用——node 单测覆盖（appwin_text_test.js）。
(function (root, factory) {
  if (typeof module === 'object' && module.exports) {
    module.exports = factory();
  } else {
    root.AppWinText = factory();
  }
})(typeof self !== 'undefined' ? self : this, function () {
  'use strict';

  // modeLabel：连接形态（usb/wifi）→ 中文标签（与主投屏"投屏中 · 有线模式"同款措辞）。
  // 未知值（含空串/undefined）→ ''（不显示后缀）。
  function modeLabel(mode) {
    if (mode === 'usb') return '有线模式';
    if (mode === 'wifi') return '无线模式';
    return '';
  }

  // cardStateText：卡片副行文字（优先级：closing > phaseText > 稳态 + 形态）。
  function cardStateText(a) {
    a = a || {};
    if (a.closing) return '正在关闭';
    if (a.phaseText) return a.phaseText;
    var lb = modeLabel(a.mode);
    return lb ? '正在窗口 · ' + lb : '正在窗口';
  }

  return { modeLabel: modeLabel, cardStateText: cardStateText };
});
