/*! rawprint.js - browser client for RawPrint Agent. MIT License. */
(function (root, factory) {
  if (typeof module === 'object' && module.exports) module.exports = factory();
  else root.RawPrint = factory();
})(typeof self !== 'undefined' ? self : this, function () {
  'use strict';

  function RawPrintError(code, message, details) {
    var e = new Error(message);
    e.name = 'RawPrintError';
    e.code = code;
    e.details = details;
    return e;
  }

  function RawPrint(options) {
    options = options || {};
    this.baseUrl = (options.baseUrl || 'http://127.0.0.1:18310').replace(/\/$/, '');
    this.storageKey = options.storageKey || 'rawprint.token';
    this.appName = options.appName || document.title || location.host;
    this.timeout = options.timeout || 8000;
  }

  RawPrint.prototype.token = function () {
    try { return localStorage.getItem(this.storageKey) || ''; } catch (e) { return ''; }
  };

  RawPrint.prototype.setToken = function (t) {
    try {
      if (t) localStorage.setItem(this.storageKey, t);
      else localStorage.removeItem(this.storageKey);
    } catch (e) { /* storage disabled */ }
  };

  RawPrint.prototype.request = function (method, path, body, timeout) {
    var self = this;
    var ctrl = typeof AbortController !== 'undefined' ? new AbortController() : null;
    var timer = ctrl ? setTimeout(function () { ctrl.abort(); }, timeout || this.timeout) : null;
    var headers = {};
    if (body !== undefined) headers['Content-Type'] = 'application/json';
    if (this.token()) headers['X-Agent-Token'] = this.token();
    var init = {
      method: method,
      headers: headers,
      body: body !== undefined ? JSON.stringify(body) : undefined,
      signal: ctrl ? ctrl.signal : undefined,
      cache: 'no-store'
    };
    // Chrome's Local Network Access asks once per site for a loopback target.
    init.targetAddressSpace = 'loopback';
    return fetch(this.baseUrl + path, init).then(function (r) {
      if (timer) clearTimeout(timer);
      return r.text().then(function (txt) {
        var data = {};
        try { data = txt ? JSON.parse(txt) : {}; } catch (e) { /* non JSON */ }
        if (r.ok) return data;
        var err = data.error || {};
        if (r.status === 401) self.setToken('');
        throw RawPrintError(err.code || 'http_' + r.status, err.message || r.statusText, data);
      });
    }, function (e) {
      if (timer) clearTimeout(timer);
      throw RawPrintError('agent_unreachable', 'RawPrint Agent is not running on this computer', e);
    });
  };

  /** Resolves to {running, paired, version, printer, profile}; never rejects. */
  RawPrint.prototype.detect = function () {
    return this.request('GET', '/health', undefined, 1500).then(function (h) {
      h.running = true;
      return h;
    }, function (e) {
      return { running: false, paired: false, error: e };
    });
  };

  /**
   * Starts pairing. onCode(code, approveUrl) is called when the user has to
   * approve the request in the agent settings page. Resolves when paired.
   */
  RawPrint.prototype.pair = function (onCode, opts) {
    var self = this;
    opts = opts || {};
    var interval = opts.interval || 1500;
    var deadline = Date.now() + (opts.timeout || 5 * 60 * 1000);
    return this.request('POST', '/pair', { name: opts.name || this.appName }).then(function (r) {
      if (r.status === 'approved') { self.setToken(r.token); return r; }
      if (onCode) onCode(r.code, r.approve_url);
      return new Promise(function (resolve, reject) {
        (function poll() {
          if (Date.now() > deadline) return reject(RawPrintError('pairing_timeout', 'Pairing was not approved in time'));
          self.request('GET', '/pair/' + encodeURIComponent(r.request_id)).then(function (p) {
            if (p.status === 'approved') { self.setToken(p.token); resolve(p); }
            else if (p.status === 'rejected') reject(RawPrintError('pairing_rejected', 'Pairing was rejected'));
            else setTimeout(poll, interval);
          }, reject);
        })();
      });
    });
  };

  RawPrint.prototype.printers = function () { return this.request('GET', '/printers'); };

  RawPrint.prototype.profile = function (printer) {
    return this.request('GET', '/profile' + (printer ? '?printer=' + encodeURIComponent(printer) : ''));
  };

  RawPrint.prototype.saveProfile = function (profile, printer) {
    return this.request('PUT', '/profile', { printer: printer || '', profile: profile });
  };

  RawPrint.prototype.testPrint = function (printer) {
    return this.request('POST', '/test-print', { printer: printer || '' });
  };

  /** job: {jobId, dataBase64, printer?, docName?, copies?} */
  RawPrint.prototype.print = function (job) {
    return this.request('POST', '/print', {
      job_id: job.jobId,
      data_base64: job.dataBase64,
      printer: job.printer || '',
      doc_name: job.docName || '',
      copies: job.copies || 1
    }, 30000);
  };

  RawPrint.prototype.job = function (jobId) {
    return this.request('GET', '/jobs/' + encodeURIComponent(jobId));
  };

  /** Polls until the job is done or failed. Resolves with the final job. */
  RawPrint.prototype.waitJob = function (jobId, opts) {
    var self = this;
    opts = opts || {};
    var deadline = Date.now() + (opts.timeout || 20000);
    return new Promise(function (resolve, reject) {
      (function poll() {
        self.job(jobId).then(function (r) {
          var j = r.job;
          if (j.state === 'done') return resolve(j);
          if (j.state === 'error') return reject(RawPrintError('job_failed', j.detail || 'Print failed', j));
          // still queued after the timeout usually means the printer is off or out of paper
          if (Date.now() > deadline) return resolve(j);
          setTimeout(poll, opts.interval || 1000);
        }, reject);
      })();
    });
  };

  RawPrint.Error = RawPrintError;
  return RawPrint;
});
