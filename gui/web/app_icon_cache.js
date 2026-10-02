(function (root, factory) {
  if (typeof module === 'object' && module.exports) module.exports = factory();
  else root.SCEZAppIconCache = factory();
})(typeof self !== 'undefined' ? self : this, function () {
  'use strict';
  function create(fetchIcon, fetchBatch) {
    var cache = Object.create(null), pending = Object.create(null);
    var generations = Object.create(null), attached = Object.create(null);
    var queue = [], batchRunning = false, scheduled = false;
    function key(identity, pkg) { return JSON.stringify([identity, pkg]); }
    function live(node) { return node && node.isConnected !== false; }
    function apply(node, data) {
      // A newly constructed card is populated before it is appended to the DOM.
      if (!node || !data) return;
      node.textContent = '';
      node.style.backgroundImage = 'url("' + data + '")';
      node.classList.add('has-img');
    }
    function finish(job, data) {
      if ((generations[job.key] || 0) === job.generation) {
        if (data) cache[job.key] = data;
        job.nodes.forEach(function (node) { if (live(node)) apply(node, data); });
      }
      if (pending[job.key] === job) delete pending[job.key];
      job.resolve();
    }
    function schedule() {
      if (scheduled || batchRunning) return;
      scheduled = true;
      Promise.resolve().then(flush);
    }
    function flush() {
      scheduled = false;
      if (batchRunning || !queue.length) return;
      var identity = queue[0].identity, jobs = [], rest = [];
      queue.forEach(function (job) {
        if (job.identity === identity && jobs.length < 24) jobs.push(job);
        else rest.push(job);
      });
      queue = rest;
      // Requests invalidated while waiting must not create another bridge call.
      var active = jobs.filter(function (job) { return pending[job.key] === job; });
      jobs.forEach(function (job) { if (pending[job.key] !== job) finish(job, ''); });
      if (!active.length) { schedule(); return; }
      batchRunning = true;
      Promise.resolve().then(function () {
        return fetchBatch(identity, active.map(function (job) { return job.pkg; }));
      }).catch(function () {
        // One bounded retry for a transient bridge error, without adding a recurring poll.
        return new Promise(function (resolve) { setTimeout(resolve, 200); }).then(function () {
          var retry = active.filter(function (job) { return pending[job.key] === job && job.nodes.some(live); });
          return retry.length ? fetchBatch(identity, retry.map(function (job) { return job.pkg; })) : {};
        });
      }).then(function (data) {
        active.forEach(function (job) { finish(job, data && data[job.pkg]); });
      }).catch(function () {
        active.forEach(function (job) { finish(job, ''); });
      }).then(function () {
        batchRunning = false;
        // Let the browser paint and handle input between batches.
        if (queue.length) setTimeout(schedule, 0);
      });
    }
    function request(identity, serial, pkg, node) {
      identity = identity || serial;
      var k = key(identity, pkg);
      var nodes = (attached[k] || []).filter(live);
      if (node && nodes.indexOf(node) < 0) nodes.push(node);
      attached[k] = nodes;
      if (cache[k] !== undefined) { apply(node, cache[k]); return Promise.resolve(); }
      if (pending[k]) { pending[k].nodes.push(node); return pending[k].promise; }
      var generation = generations[k] || 0;
      var job = { nodes: [node], key: k, identity: identity, pkg: pkg, generation: generation };
      pending[k] = job;
      if (fetchBatch) {
        job.promise = new Promise(function (resolve) { job.resolve = resolve; });
        queue.push(job);
        schedule();
        return job.promise;
      }
      job.promise = Promise.resolve().then(function () { return fetchIcon(identity || serial, pkg); }).then(function (data) {
        if ((generations[k] || 0) !== generation) return;
        // A missing icon may still be exporting. Do not poison subsequent opens.
        if (data) cache[k] = data;
        job.nodes.forEach(function (n) { if (live(n)) apply(n, data); });
      }).catch(function () {}).then(function () {
        if (pending[k] === job) delete pending[k];
      });
      return job.promise;
    }
    function invalidate(identity, pkg) {
      var k = key(identity, pkg);
      generations[k] = (generations[k] || 0) + 1;
      delete cache[k];
      delete pending[k];
    }
    function refresh(identity, pkgs) {
      var work = [];
      (pkgs || []).forEach(function (pkg) {
        var k = key(identity, pkg), nodes = (attached[k] || []).filter(live);
        attached[k] = nodes;
        invalidate(identity, pkg);
        nodes.forEach(function (node) { work.push(request(identity, identity, pkg, node)); });
      });
      return Promise.all(work);
    }
    return { request: request, invalidate: invalidate, refresh: refresh };
  }
  return { create: create };
});
