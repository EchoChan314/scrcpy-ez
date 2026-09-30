// 应用窗口设置浮窗纯状态机单测（node web/appsetting_state_test.js）
// v2.1.74：size 栏与主投屏 res 栏同口径——值为长边像素数（number），
// 短边由后端按设备宽高比换算；isSize 分支随 WxH 文案一并移除。
'use strict';
const S = require('./appsetting_state.js');

let failed = 0;
function eq(name, got, want) {
  const g = JSON.stringify(got), w = JSON.stringify(want);
  if (g !== w) { failed++; console.error(`FAIL ${name}: got ${g}, want ${w}`); }
  else { console.log(`ok   ${name}`); }
}
function mk(v, b, custom, edit, input) {
  return { val: v, base: b, custom: custom || false, edit: edit || false, input: input || '' };
}

// ---------- 档位 / 自定义进出 ----------
eq('clickTier 退出输入态（数字栏）', S.clickTier(mk(90, 60, true, true, '90'), 120),
  { val: 120, base: 60, custom: true, edit: false, input: '' });
eq('clickTier（size 栏=长边数字）', S.clickTier(mk(1600, 1280, true, true, '1600'), 1280),
  { val: 1280, base: 1280, custom: true, edit: false, input: '' });
eq('startCustom 进入空输入态', S.startCustom(mk(90, 60, false, false, '')),
  { val: 90, base: 60, custom: false, edit: true, input: '' });
eq('typeCustom 仅记录文本', S.typeCustom(mk(90, 60, false, true, ''), '75'),
  { val: 90, base: 60, custom: false, edit: true, input: '75' });

// ---------- 提交（全部数字栏口径） ----------
eq('commitCustom 数字合法', S.commitCustom(mk(90, 60, false, true, ''), '75'),
  { val: 75, base: 60, custom: false, edit: true, input: '75' });
eq('commitCustom 数字非法回 base', S.commitCustom(mk(90, 60, false, true, ''), 'abc'),
  { val: 60, base: 60, custom: false, edit: false, input: '' });
eq('commitCustom 数字空回 base', S.commitCustom(mk(90, 60, false, true, ''), ''),
  { val: 60, base: 60, custom: false, edit: false, input: '' });
eq('commitCustom 数字 0 拒', S.commitCustom(mk(90, 60, false, true, ''), '0'),
  { val: 60, base: 60, custom: false, edit: false, input: '' });
eq('commitCustom size 长边合法', S.commitCustom(mk(1280, 1280, false, true, ''), '1600'),
  { val: 1600, base: 1280, custom: false, edit: true, input: '1600' });
eq('commitCustom size 带空格 trim', S.commitCustom(mk(1280, 1280, false, true, ''), ' 2000 '),
  { val: 2000, base: 1280, custom: false, edit: true, input: '2000' });
eq('commitCustom size 四位合法', S.commitCustom(mk(1280, 1280, false, true, ''), '2560'),
  { val: 2560, base: 1280, custom: false, edit: true, input: '2560' });
eq('commitCustom size 非数字回 base', S.commitCustom(mk(1280, 1280, false, true, ''), 'abc'),
  { val: 1280, base: 1280, custom: false, edit: false, input: '' });

// ---------- resetField ----------
eq('resetField 回 base', S.resetField(mk(1600, 1280, true, true, '1600')),
  { val: 1280, base: 1280, custom: true, edit: false, input: '' });
eq('resetSizeAndRatio 同时重置长边并清空比例',
  S.resetSizeAndRatio(mk(1600, 1280, true, true, '1600')),
  { size: { val: 1280, base: 1280, custom: true, edit: false, input: '' }, ratioW: '', ratioH: '' });

// ---------- infer（打开推断） ----------
eq('infer size 自定义值在档位 → 无输入框', S.infer(mk(1920, 1280, true, false, ''), [2560, 1920, 1080, 720]),
  { val: 1920, base: 1280, custom: true, edit: false, input: '' });
eq('infer size 自定义值非档位 → 输入态', S.infer(mk(1700, 1280, true, false, ''), [2560, 1920, 1080, 720]),
  { val: 1700, base: 1280, custom: true, edit: true, input: '1700' });
eq('infer 数字栏自定义值非档位 → 输入态', S.infer(mk(75, 60, true, false, ''), [120, 90, 60, 30]),
  { val: 75, base: 60, custom: true, edit: true, input: '75' });
eq('infer 未自定义 → 无输入框', S.infer(mk(60, 60, false, false, ''), [120, 90, 60, 30]),
  { val: 60, base: 60, custom: false, edit: false, input: '' });

// ---------- settle（保存结算） ----------
eq('settle 数字输入态合法 → number', S.settle(mk(75, 60, false, true, '80')),
  { value: 80, custom: true });
eq('settle 数字输入态非法 → base', S.settle(mk(75, 60, false, true, 'xyz')),
  { value: 60, custom: false });
eq('settle size 输入态合法 → number', S.settle(mk(1280, 1280, false, true, '1600')),
  { value: 1600, custom: true });
eq('settle size 输入态非法 → base', S.settle(mk(1280, 1280, false, true, 'abc')),
  { value: 1280, custom: false });
eq('settle 档位点击（值≠base）→ 自定义', S.settle(mk(1920, 1280, false, false, '')),
  { value: 1920, custom: true });
eq('settle 未改动 → base 自动档', S.settle(mk(1280, 1280, false, false, '')),
  { value: 1280, custom: false });
eq('settle 数字未改动 → base', S.settle(mk(60, 60, false, false, '')),
  { value: 60, custom: false });

// ---------- buildTiers（设备默认值插档，v2.1.48） ----------
eq('buildTiers 默认值不在固定档 → 插入并降序（数字）',
  S.buildTiers(15, [32, 16, 8, 4]), [32, 16, 15, 8, 4]);
eq('buildTiers 默认值已在固定档 → 去重',
  S.buildTiers(60, [120, 90, 60, 30]), [120, 90, 60, 30]);
eq('buildTiers size 长边插档（默认 2560 已在）',
  S.buildTiers(2560, [2560, 1920, 1080, 720]), [2560, 1920, 1080, 720]);
eq('buildTiers size 长边居中 → 插到中位',
  S.buildTiers(1500, [1920, 1080, 720]), [1920, 1500, 1080, 720]);
eq('buildTiers 空默认值 → 只回固定档',
  S.buildTiers('', [32, 16, 8]), [32, 16, 8]);
eq('buildTiers 默认值大于最大档 → 排最前',
  S.buildTiers(64, [32, 16, 8]), [64, 32, 16, 8]);

// ---------- 初始比例 ----------
eq('parseRatio 双空=跟随设备', S.parseRatio('', ''),
  { ok: true, width: 0, height: 0, followDevice: true });
eq('parseRatio 1:1 合法', S.parseRatio('1', '1'),
  { ok: true, width: 1, height: 1, followDevice: false });
eq('parseRatio 空一个不完整', S.parseRatio('16', ''), { ok: false, reason: 'incomplete' });
eq('parseRatio 非整数拒绝', S.parseRatio('16x', '9'), { ok: false, reason: 'invalid' });
eq('parseRatio 超范围拒绝', S.parseRatio('10001', '9'), { ok: false, reason: 'invalid' });

if (failed) { console.error(`\n${failed} 项失败`); process.exit(1); }
console.log('\nappsetting_state 全部通过');
