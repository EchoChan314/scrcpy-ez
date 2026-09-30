// 应用窗口卡片状态文字纯函数单测（node web/appwin_text_test.js）
// 覆盖：closing 最高优先、phaseText 次之、稳态形态后缀（usb→有线模式 / wifi→无线模式）、
// 形态未知/为空兜底（只显示"正在窗口"）。
'use strict';
const T = require('./appwin_text.js');

let failed = 0;
function eq(name, got, want) {
  const g = JSON.stringify(got), w = JSON.stringify(want);
  if (g !== w) { failed++; console.error('FAIL ' + name + ': got ' + g + ', want ' + w); }
  else { console.log('ok   ' + name); }
}

// --- closing：最高优先（形态/阶段文字都不影响） ---
eq('closing 显示"正在关闭"', T.cardStateText({ closing: true, phaseText: '检测到 USB 插线，切换有线投屏…', mode: 'usb' }), '正在关闭');

// --- phaseText：转换中优先于稳态+形态 ---
eq('phaseText 非空 → 阶段文字', T.cardStateText({ phaseText: '正在启动虚拟屏…', mode: 'wifi' }), '正在启动虚拟屏…');

// --- 稳态 + 形态后缀 ---
eq('稳态 usb → 正在窗口 · 有线模式', T.cardStateText({ mode: 'usb' }), '正在窗口 · 有线模式');
eq('稳态 wifi → 正在窗口 · 无线模式', T.cardStateText({ mode: 'wifi' }), '正在窗口 · 无线模式');

// --- 兜底：形态未知/为空只显示"正在窗口" ---
eq('mode 空串 → 只显示正在窗口', T.cardStateText({ mode: '' }), '正在窗口');
eq('mode 未知值 → 只显示正在窗口', T.cardStateText({ mode: 'tcpip' }), '正在窗口');
eq('无 mode 字段（乐观插入卡）→ 正在窗口', T.cardStateText({ pkg: 'com.example' }), '正在窗口');
eq('入参 undefined（防御）→ 正在窗口', T.cardStateText(undefined), '正在窗口');

// --- modeLabel 直接覆盖 ---
eq('modeLabel usb', T.modeLabel('usb'), '有线模式');
eq('modeLabel wifi', T.modeLabel('wifi'), '无线模式');
eq('modeLabel 空串', T.modeLabel(''), '');

if (failed) { console.error(failed + ' 个用例失败'); process.exit(1); }
console.log('全部用例通过');
