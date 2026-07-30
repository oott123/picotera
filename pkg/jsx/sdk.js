;(function () {
  'use strict'

  function Waterfall() {
    this._taps = []
  }
  Waterfall.prototype.tap = function (name, fn, priority) {
    this._taps.push({ name: String(name || 'anonymous'), fn: fn, priority: priority ?? 0 })
    this._taps.sort(function (a, b) {
      return b.priority - a.priority;
    });
  }
  Waterfall.prototype.runWaterfall = function (context, input) {
    let value = input
    for (const tap of this._taps) {
      const out = tap.fn(context, value)
      if (typeof out !== 'undefined') {
        value = out
      }
    }
    return value
  }

  // ---- Annotation API argument validation (fail fast, no coercion) ----

  function checkRequestId(id) {
    if (typeof id !== 'string' || id === '') {
      throw new TypeError('picotera: requestId must be a non-empty string')
    }
    return id
  }

  function checkIntId(name, id) {
    if (!Number.isInteger(id)) {
      throw new TypeError('picotera: ' + name + ' must be an integer')
    }
    return id
  }

  function checkKey(key) {
    if (typeof key !== 'string' || key === '') {
      throw new TypeError('picotera: annotation key must be a non-empty string')
    }
    return key
  }

  // encodeAnnotationValue maps the value argument onto the host encoding: "" for
  // a delete (null / undefined), otherwise the JSON encoding of the string (so
  // an empty-string value stays distinguishable from a delete). Any other type
  // is a TypeError — no implicit String() conversion.
  function encodeAnnotationValue(value) {
    if (value === null || typeof value === 'undefined') return ''
    if (typeof value !== 'string') {
      throw new TypeError('picotera: annotation value must be a string, null, or undefined')
    }
    return JSON.stringify(value)
  }

  globalThis.picotera = {
    hooks: {
      sortProviders: new Waterfall(),
      beforeRequest: new Waterfall(),
      beforeTransform: new Waterfall(),
      rewriteRequest: new Waterfall(),
      rewriteModel: new Waterfall(),
      rewriteProviderModels: new Waterfall(),
      afterUpstreamError: new Waterfall(),
      requestFinished: new Waterfall(),
    },
    request: {
      setAnnotation: function (requestId, key, value) {
        var e = globalThis.__picotera_anno_request(
          checkRequestId(requestId), checkKey(key), encodeAnnotationValue(value))
        if (e) throw new Error(e)
      },
    },
    provider: {
      get: function (providerId) {
        var r = globalThis.__picotera_get_provider(checkIntId('providerId', providerId))
        if (r[1]) throw new Error(r[1])
        return r[0] === '' ? null : JSON.parse(r[0])
      },
      setAnnotation: function (providerId, key, value) {
        var e = globalThis.__picotera_anno_provider(
          checkIntId('providerId', providerId), checkKey(key), encodeAnnotationValue(value))
        if (e) throw new Error(e)
      },
    },
    apiKey: {
      get: function (apiKeyId) {
        var r = globalThis.__picotera_get_apikey(checkIntId('apiKeyId', apiKeyId))
        if (r[1]) throw new Error(r[1])
        return r[0] === '' ? null : JSON.parse(r[0])
      },
      setAnnotation: function (apiKeyId, key, value) {
        var e = globalThis.__picotera_anno_apikey(
          checkIntId('apiKeyId', apiKeyId), checkKey(key), encodeAnnotationValue(value))
        if (e) throw new Error(e)
      },
    },
    kv: {
      get: function (key) {
        var r = globalThis.__picotera_kv_get(String(key));
        if (r[1]) throw new Error(r[1]);
        return r[0] === '' ? null : JSON.parse(r[0]);
      },
      set: function (key, value) {
        var e = globalThis.__picotera_kv_set(String(key), JSON.stringify(value));
        if (e) throw new Error(e);
      },
      setex: function (key, seconds, value) {
        var e = globalThis.__picotera_kv_setex(String(key), Number(seconds), JSON.stringify(value));
        if (e) throw new Error(e);
      },
      ttl: function (key) {
        var r = globalThis.__picotera_kv_ttl(String(key));
        if (r[1]) throw new Error(r[1]);
        return r[0];
      },
      del: function (key) {
        var e = globalThis.__picotera_kv_del(String(key));
        if (e) throw new Error(e);
      },
    },
    fetch: function (url, init) {
      var initJSON = init ? JSON.stringify(init) : ''
      var r = globalThis.__picotera_fetch(String(url), initJSON)
      if (r[1]) throw new Error(r[1])
      return JSON.parse(r[0])
    },
  }

  // ---- Body Proxy machinery (ctx.request.body / rewriteRequest pending.body) ----
  //
  // The two large request bodies a hook can touch live as jsonast trees on the
  // Go side; here we wrap each accessed object/array node in a Proxy keyed by an
  // integer id. Reads/writes/enumeration forward to the obj_* host functions, so
  // scalars cross into JS only on demand and writes land directly on the Go tree.
  // These globals are internal plumbing — not part of the public picotera.* API.

  var proxyById = new Map() // id -> Proxy (keeps body.a === body.a)
  var idByProxy = new WeakMap() // Proxy -> id (for markerReplacer)

  function isIndex(s) {
    var n = Number(s)
    return Number.isInteger(n) && n >= 0 && String(n) === s
  }

  function hostLen(id) {
    var r = globalThis.__picotera_obj_keys(id)
    if (r[1]) throw new Error(r[1])
    var d = JSON.parse(r[0])
    return d.t === 'a' ? d.len : 0
  }

  // descToValue turns a host descriptor (already JSON.parsed) into a JS value:
  // scalars inline, object/array as (cached) Proxies, undefined for "u".
  function descToValue(d) {
    if (d.t === 'j') return d.v
    if (d.t === 'o') return makeProxy(d.id, 'o')
    if (d.t === 'a') return makeProxy(d.id, 'a')
    return undefined
  }

  // markerReplacer is a JSON.stringify replacer used ONLY by the glue layer and
  // the set trap: it rewrites any managed Proxy into a {"__picotera_object":id}
  // marker the Go side restores (deep-copy on set, direct ref on rr output). A
  // script's own JSON.stringify(proxy) omits it, so it fully materializes the
  // tree — which is exactly how JSON.parse(JSON.stringify(x)) deep-copies.
  function markerReplacer(k, v) {
    if (v !== null && typeof v === 'object' && idByProxy.has(v)) {
      return { __picotera_object: idByProxy.get(v) }
    }
    return v
  }

  // ProxiedArrayProto sits between an array Proxy's target ([]) and
  // Array.prototype. It overrides only the mutating methods that relocate
  // existing elements — splice/push/pop/shift/unshift/reverse — routing them
  // through __picotera_arr_* host calls so the Go side reorders the []*Node
  // slice by pointer instead of cloning each moved element via per-index set
  // traps. Read-only methods (map/filter/slice/forEach/join/iteration/spread)
  // fall through to Array.prototype unchanged.
  var ProxiedArrayProto = Object.create(Array.prototype)

  function arrId(self) {
    var id = idByProxy.get(self)
    if (id === undefined) throw new Error('picotera: array op on a non-managed value')
    return id
  }

  function normalizeStart(start, len) {
    var s = Math.trunc(Number(start)) || 0
    if (s < 0) {
      s = len + s
      if (s < 0) s = 0
    } else if (s > len) {
      s = len
    }
    return s
  }

  ProxiedArrayProto.splice = function (start, deleteCount) {
    var id = arrId(this)
    var len = hostLen(id)
    var s = normalizeStart(start, len)
    var dc
    if (arguments.length < 2) {
      dc = len - s
    } else {
      dc = Math.trunc(Number(deleteCount)) || 0
      if (dc < 0) dc = 0
      if (dc > len - s) dc = len - s
    }
    var items = Array.prototype.slice.call(arguments, 2)
    var r = globalThis.__picotera_arr_splice(id, s, dc, JSON.stringify(items, markerReplacer))
    if (r[1]) throw new Error(r[1])
    return JSON.parse(r[0]).removed.map(descToValue)
  }

  ProxiedArrayProto.push = function () {
    var id = arrId(this)
    var len = hostLen(id)
    var items = Array.prototype.slice.call(arguments)
    var r = globalThis.__picotera_arr_splice(id, len, 0, JSON.stringify(items, markerReplacer))
    if (r[1]) throw new Error(r[1])
    return JSON.parse(r[0]).len
  }

  ProxiedArrayProto.unshift = function () {
    var id = arrId(this)
    var items = Array.prototype.slice.call(arguments)
    var r = globalThis.__picotera_arr_splice(id, 0, 0, JSON.stringify(items, markerReplacer))
    if (r[1]) throw new Error(r[1])
    return JSON.parse(r[0]).len
  }

  ProxiedArrayProto.pop = function () {
    var id = arrId(this)
    var len = hostLen(id)
    if (len === 0) return undefined
    var r = globalThis.__picotera_arr_splice(id, len - 1, 1, '[]')
    if (r[1]) throw new Error(r[1])
    return JSON.parse(r[0]).removed.map(descToValue)[0]
  }

  ProxiedArrayProto.shift = function () {
    var id = arrId(this)
    var len = hostLen(id)
    if (len === 0) return undefined
    var r = globalThis.__picotera_arr_splice(id, 0, 1, '[]')
    if (r[1]) throw new Error(r[1])
    return JSON.parse(r[0]).removed.map(descToValue)[0]
  }

  ProxiedArrayProto.reverse = function () {
    var e = globalThis.__picotera_arr_reverse(arrId(this))
    if (e) throw new Error(e)
    return this
  }

  function makeProxy(id, kind) {
    var cached = proxyById.get(id)
    if (cached) return cached
    var target = kind === 'a' ? [] : {}
    if (kind === 'a') Object.setPrototypeOf(target, ProxiedArrayProto)
    var handler = {
      get: function (t, prop, recv) {
        if (typeof prop === 'symbol') return Reflect.get(t, prop, recv)
        prop = String(prop)
        if (kind === 'a') {
          if (prop === 'length') return hostLen(id)
          if (!isIndex(prop)) return Reflect.get(t, prop, recv)
        }
        var r = globalThis.__picotera_obj_get(id, prop)
        if (r[1]) throw new Error(r[1])
        var d = JSON.parse(r[0])
        if (d.t === 'u') return Reflect.get(t, prop, recv)
        return descToValue(d)
      },
      set: function (t, prop, value, recv) {
        if (typeof prop === 'symbol') return Reflect.set(t, prop, value, recv)
        prop = String(prop)
        if (kind === 'a' && prop === 'length') {
          var le = globalThis.__picotera_obj_setlen(id, Number(value))
          if (le) throw new Error(le)
          return true
        }
        if (typeof value === 'undefined') {
          throw new Error('picotera: cannot assign undefined to a managed body property')
        }
        var json = JSON.stringify(value, markerReplacer)
        if (typeof json === 'undefined') {
          throw new Error('picotera: cannot assign a non-JSON-serializable value to a managed body property')
        }
        var e = globalThis.__picotera_obj_set(id, prop, json)
        if (e) throw new Error(e)
        return true
      },
      deleteProperty: function (t, prop) {
        if (typeof prop === 'symbol') return Reflect.deleteProperty(t, prop)
        var e = globalThis.__picotera_obj_del(id, String(prop))
        if (e) throw new Error(e)
        return true
      },
      has: function (t, prop) {
        if (typeof prop === 'symbol') return Reflect.has(t, prop)
        prop = String(prop)
        if (kind === 'a' && prop === 'length') return true
        var r = globalThis.__picotera_obj_has(id, prop)
        if (r[1]) throw new Error(r[1])
        return !!r[0]
      },
      ownKeys: function (t) {
        var r = globalThis.__picotera_obj_keys(id)
        if (r[1]) throw new Error(r[1])
        var d = JSON.parse(r[0])
        if (d.t === 'o') return d.keys
        var keys = []
        for (var i = 0; i < d.len; i++) keys.push(String(i))
        keys.push('length')
        return keys
      },
      getOwnPropertyDescriptor: function (t, prop) {
        if (typeof prop === 'symbol') return Reflect.getOwnPropertyDescriptor(t, prop)
        prop = String(prop)
        if (kind === 'a' && prop === 'length') {
          // Sync the target's own non-configurable length so the Proxy
          // invariant (report it as it is on the target) holds.
          Reflect.defineProperty(t, 'length', { value: hostLen(id), writable: true, enumerable: false, configurable: false })
          return Reflect.getOwnPropertyDescriptor(t, 'length')
        }
        var hr = globalThis.__picotera_obj_has(id, prop)
        if (hr[1]) throw new Error(hr[1])
        if (!hr[0]) return undefined
        var r = globalThis.__picotera_obj_get(id, prop)
        if (r[1]) throw new Error(r[1])
        return { value: descToValue(JSON.parse(r[0])), writable: true, enumerable: true, configurable: true }
      },
    }
    var p = new Proxy(target, handler)
    proxyById.set(id, p)
    idByProxy.set(p, id)
    return p
  }

  globalThis.__picotera_descToValue = descToValue
  globalThis.__picotera_markerReplacer = markerReplacer

  // Installs (or reinstalls) the lazy ctx.request.body getter. Called by the
  // host after a request PatchContext (which replaces ctx.request) or after
  // SetClientBody, so the order of the two is irrelevant.
  globalThis.__picotera_installRequestBody = function () {
    var req = globalThis.ctx && globalThis.ctx.request
    if (!req || typeof req !== 'object') return
    var val, got = false
    Object.defineProperty(req, 'body', {
      enumerable: true,
      configurable: true,
      get: function () {
        if (!got) {
          var r = globalThis.__picotera_obj_root('request')
          if (r[1]) throw new Error(r[1])
          val = descToValue(JSON.parse(r[0]))
          got = true
        }
        return val
      },
      set: function (v) { got = true; val = v },
    })
  }

  var consoleEmit = function (level) {
    return function () {
      var parts = []
      for (var i = 0; i < arguments.length; i++) {
        var a = arguments[i]
        parts.push(typeof a === 'string' ? a : (function () { try { return JSON.stringify(a) } catch (_e) { return String(a) } })())
      }
      globalThis.__picotera_console(level, parts.join(' '))
    }
  }
  globalThis.console = {
    log: consoleEmit('info'),
    info: consoleEmit('info'),
    warn: consoleEmit('warn'),
    error: consoleEmit('error'),
    debug: consoleEmit('info'),
  }
})()
