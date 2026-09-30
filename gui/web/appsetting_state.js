// 应用窗口设置浮窗纯状态机（浏览器全局 + node 可 require 单测）。
// 与主投屏 param_state 同构：字段级状态 st = { val, base, custom, edit, input }
//   - val:   当前值（全部为 number；size=长边像素数，与主投屏 res 栏同口径，
//            短边由后端按设备宽高比换算——v2.1.74 起）
//   - base:  默认档值（该栏「重置」目标）
//   - custom: 打开时该套是否已有自定义（档案）
//   - edit:  是否处于自定义输入态
//   - input: 输入框文本（数字文本）
// 数字栏校验正整数；空/非法提交 → 回 base（取消自定义）。
(function (root, factory) {
  'use strict';
  if (typeof module === 'object' && module.exports) {
    module.exports = factory();
  } else {
    root.SCEZAppSetState = factory();
  }
})(typeof self !== 'undefined' ? self : this, function () {
  'use strict';

  // 点击标准档位：值=该档，退出自定义输入态（输入框消失）。
  function clickTier(st, v) {
    return { val: v, base: st.base, custom: st.custom, edit: false, input: '' };
  }

  // 点「＋自定义」：进入输入态，初始为空。
  function startCustom(st) {
    return { val: st.val, base: st.base, custom: st.custom, edit: true, input: '' };
  }

  // 输入框实时输入：仅记录文本（change/提交时才校验）。
  function typeCustom(st, text) {
    return { val: st.val, base: st.base, custom: st.custom, edit: true, input: text };
  }

  // 输入提交（change）：合法（正整数）→ 值=输入、保持输入态；
  // 空/非法 → 取消自定义回 base（输入态退出）。
  function commitCustom(st, text) {
    var t = String(text == null ? '' : text).trim();
    var n = parseInt(t, 10);
    if (n > 0) {
      return { val: n, base: st.base, custom: st.custom, edit: true, input: String(n) };
    }
    return { val: st.base, base: st.base, custom: st.custom, edit: false, input: '' };
  }

  // 重置：回 base（输入态退出）。
  function resetField(st) {
    return { val: st.base, base: st.base, custom: st.custom, edit: false, input: '' };
  }

  // 分辨率行的重置同时恢复设备默认长边并清空自定义比例。
  function resetSizeAndRatio(st) {
    return { size: resetField(st), ratioW: '', ratioH: '' };
  }

  // 打开浮窗推断：仅当"已自定义且值不在档位列表"→ 输入态且输入框=当前值；
  // 否则纯档位选择态（无输入框）。
  function infer(st, tiers) {
    if (st.custom && tiers.indexOf(st.val) < 0) {
      return { val: st.val, base: st.base, custom: st.custom, edit: true, input: String(st.val) };
    }
    return { val: st.val, base: st.base, custom: st.custom, edit: false, input: '' };
  }

  // 保存结算 → {value, custom}：
  //   - 输入态且输入合法 → 值=输入（custom=true，number）；
  //   - 输入态且输入非法 → base（custom=false，防半输入提交脏值）；
  //   - 非输入态且值≠base（档位点击）→ 值=档位（custom=true）；
  //   - 否则 → base（custom=false）。
  function settle(st) {
    if (st.edit && st.input !== '') {
      var n = parseInt(String(st.input).trim(), 10);
      if (n > 0) {
        return { value: n, custom: true };
      }
      return { value: st.base, custom: false };
    }
    if (!st.edit && st.val !== st.base) {
      return { value: st.val, custom: true };
    }
    return { value: st.base, custom: false };
  }

  // tierValue：档位比较值（数字直接比较；字符串取 'x' 前段，防御性兼容）。
  function tierValue(v) {
    if (typeof v === 'number') return v;
    var n = parseInt(String(v).split('x')[0], 10);
    return isNaN(n) ? 0 : n;
  }

  // buildTiers（v2.1.48）：设备默认值插到固定档序列（去重 + 降序）——设备默认随
  // 主投屏规格变化，未必在固定档里；保证默认档始终有对应 chip 可选中。
  function buildTiers(defVal, fixed) {
    var out = [];
    function push(v) {
      if (v === '' || v === null || v === undefined) return;
      if (out.indexOf(v) < 0) out.push(v);
    }
    push(defVal);
    (fixed || []).forEach(push);
    out.sort(function (a, b) { return tierValue(b) - tierValue(a); });
    return out;
  }

  // parseRatio：两个输入都空=跟随设备；必须同时填写正整数，否则阻止保存。
  function parseRatio(width, height) {
    var w = String(width == null ? '' : width).trim();
    var h = String(height == null ? '' : height).trim();
    if (!w && !h) return { ok: true, width: 0, height: 0, followDevice: true };
    if (!w || !h) return { ok: false, reason: 'incomplete' };
    if (!/^\d+$/.test(w) || !/^\d+$/.test(h)) return { ok: false, reason: 'invalid' };
    var wn = Number(w), hn = Number(h);
    if (!Number.isSafeInteger(wn) || !Number.isSafeInteger(hn) || wn < 1 || hn < 1 || wn > 10000 || hn > 10000) {
      return { ok: false, reason: 'invalid' };
    }
    return { ok: true, width: wn, height: hn, followDevice: false };
  }

  return {
    clickTier: clickTier,
    startCustom: startCustom,
    typeCustom: typeCustom,
    commitCustom: commitCustom,
    resetField: resetField,
    resetSizeAndRatio: resetSizeAndRatio,
    infer: infer,
    settle: settle,
    buildTiers: buildTiers,
    parseRatio: parseRatio
  };
});
