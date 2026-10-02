// 应用投屏条纯函数单测（node web/appwin_bar_test.js）
// 覆盖：直配键命中、identity 回退命中、identity 空不误配、空卡片键忽略、
//       多键计数合计、closing 计入、文案。
'use strict';
const B = require('./appwin_bar.js');

let failed = 0;
function ok(name, cond) {
  if (!cond) { failed++; console.error('FAIL ' + name); }
  else { console.log('ok   ' + name); }
}
function eq(name, got, want) {
  const g = JSON.stringify(got), w = JSON.stringify(want);
  if (g !== w) { failed++; console.error('FAIL ' + name + ': got ' + g + ', want ' + w); }
  else { console.log('ok   ' + name); }
}

// --- keysForDevice：直配键命中（键=设备 serial） ---
(function () {
  const cards = { 'TESTUSB0003': [{ pkg: 'com.a' }], 'other0001': [{ pkg: 'com.b' }] };
  eq('直配键命中（另一设备不串）',
    B.keysForDevice(cards, ['TESTUSB0003', '192.168.50.10:5555'], 'Xiaomi Pad 8 Pro', function () { return ''; }),
    ['TESTUSB0003']);
})();

// --- keysForDevice：identity 回退命中（键=无线地址，设备卡键=USB serial） ---
(function () {
  const cards = { '192.168.50.10:5555': [{ pkg: 'com.a' }] };
  const devId = 'Xiaomi Pad 8 Pro';
  const idOf = function (k) { return (k === '192.168.50.10:5555') ? devId : ''; };
  eq('identity 回退命中', B.keysForDevice(cards, ['TESTUSB0003'], devId, idOf), ['192.168.50.10:5555']);
})();

// --- keysForDevice：identity 为空 → 只做直配，不误配 ---
(function () {
  const cards = { '192.168.50.10:5555': [{ pkg: 'com.a' }] };
  eq('identity 空不回退（不误配）',
    B.keysForDevice(cards, ['TESTUSB0003'], '', function () { return 'Xiaomi Pad 8 Pro'; }),
    []);
})();

// --- keysForDevice：空卡片键忽略 / 无卡片=空 ---
(function () {
  const cards = { 'TESTUSB0003': [], '192.168.50.10:5555': [{ pkg: 'com.b' }] };
  eq('空卡片键忽略', B.keysForDevice(cards, ['TESTUSB0003'], '', function () { return ''; }), []);
  eq('null 表防御', B.keysForDevice(null, ['x'], 'id', function () { return 'id'; }), []);
})();

// --- keysForDevice：多键聚合（直配 + 身份，双键并存） ---
(function () {
  const cards = { 'TESTUSB0003': [{ pkg: 'com.a' }], '192.168.50.10:5555': [{ pkg: 'com.b' }] };
  const idOf = function (k) { return (k === '192.168.50.10:5555') ? 'PAD' : ''; };
  eq('多键聚合（直配+身份）',
    B.keysForDevice(cards, ['TESTUSB0003'], 'PAD', idOf),
    ['TESTUSB0003', '192.168.50.10:5555']);
})();

// --- countFor：多键合计 / closing 计入 / 空 ---
(function () {
  const cards = {
    'TESTUSB0003': [{ pkg: 'com.a' }, { pkg: 'com.b', closing: true }],
    '192.168.50.10:5555': [{ pkg: 'com.c' }],
  };
  eq('多键合计 2+1=3', B.countFor(cards, ['TESTUSB0003', '192.168.50.10:5555']), 3);
  eq('closing 中的窗口仍计入', B.countFor(cards, ['TESTUSB0003']), 2);
  eq('空键列表=0', B.countFor(cards, []), 0);
  eq('null 防御=0', B.countFor(null, ['x']), 0);
})();

// --- label / suffix：文案（数字与后缀分离——前端数字单独加粗） ---
eq('label 1', B.label(1), '1 个应用正在投屏');
eq('label 3', B.label(3), '3 个应用正在投屏');
eq('suffix 后缀', B.suffix, ' 个应用正在投屏');
eq('label = 数字 + suffix', B.label(5), '5' + B.suffix);

// --- jumpAction：设备卡点击三态 ---
eq('主投屏在 → cast', B.jumpAction(true, 0), 'cast');
eq('主投屏在（即使有应用投屏）→ cast', B.jumpAction(true, 2), 'cast');
eq('只有应用投屏 → appwin', B.jumpAction(false, 1), 'appwin');
eq('无任何投屏 → 不跳转', B.jumpAction(false, 0), null);

// --- allClosing：全关闭判定（closing/leaving；空=false） ---
(function () {
  const cards = {
    'TESTUSB0003': [{ pkg: 'com.a', closing: true }, { pkg: 'com.b', leaving: true }],
    'x': [{ pkg: 'com.c' }],
  };
  eq('全 closing/leaving → true', B.allClosing(cards, ['TESTUSB0003']), true);
  eq('部分关闭 → false', B.allClosing(cards, ['TESTUSB0003', 'x']), false);
  eq('空表 → false', B.allClosing(cards, ['nope']), false);
  eq('普通条目 → false', B.allClosing(cards, ['x']), false);
})();

// --- appendExtraKeys：补充键并入（去重/保序/过滤空值）——设备档案 serials ∪ addrs ---
eq('并入补充键（去重）', B.appendExtraKeys(['TESTUSB0003', '192.168.50.11:5555'], ['TESTUSB0003', '192.168.50.12:46269']), ['TESTUSB0003', '192.168.50.11:5555', '192.168.50.12:46269']);
eq('空补充', B.appendExtraKeys(['a'], []), ['a']);
eq('空直配', B.appendExtraKeys([], ['x']), ['x']);
eq('过滤空值', B.appendExtraKeys(['a'], ['', null, 'b']), ['a', 'b']);

eq('地址复用时直配键必须服从窗口原档案身份', B.keysForDevice({'192.0.2.10:5555':[{pkg:'pkg'}]}, ['192.0.2.10:5555'], 'device:B', function(){return 'device:A';}), []);
if (failed) { console.error(failed + ' 个用例失败'); process.exit(1); }
console.log('全部用例通过');
