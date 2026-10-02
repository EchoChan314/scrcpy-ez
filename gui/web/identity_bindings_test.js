'use strict';
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const source = fs.readFileSync(require('node:path').join(__dirname, 'app.js'), 'utf8');
const scope = { lastState: { devices: [{serial:'192.0.2.10:5555',identity:'device:B'}], profiles: [] },
  sessions: {}, appWins: {}, openAppCards: {} };
vm.createContext(scope);
for (const name of ['deviceIdentityOf', 'findAppKeyFor', 'sessionKeyOf', 'appKeyOf', 'iconIdentity']) {
  const match = source.match(new RegExp('  function ' + name + '\\([\\s\\S]*?\\n  }'));
  assert.ok(match, name);
  vm.runInContext(match[0], scope);
}
const addr = '192.0.2.10:5555';
scope.appWins[addr] = {identity:'device:A'};
scope.appWins['device:B'] = {identity:'device:B'};
scope.sessions['USB_A'] = {identity:'device:A'};
assert.equal(scope.deviceIdentityOf(addr), 'device:B');
assert.equal(scope.findAppKeyFor(addr), 'device:B');
assert.equal(scope.sessionKeyOf(addr), 'USB_A');
assert.equal(scope.iconIdentity(addr), 'device:A');
scope.sessions[addr] = {identity:'device:B'};
assert.equal(scope.sessionKeyOf(addr), 'USB_A');
assert.equal(scope.appKeyOf(addr), 'device:B');
console.log('identity_bindings: current cards and original windows/sessions remain separate after address reuse');
