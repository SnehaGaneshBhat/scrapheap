"use strict";

(() => {
  const memoryCache = new Map();
  let databasePromise;

  function rotateRight(value, count) {
    return (value >>> count) | (value << (32 - count));
  }

  function sha256Fallback(input) {
    const source = input instanceof Uint8Array ? input : new Uint8Array(input);
    const paddedLength = Math.ceil((source.length + 9) / 64) * 64;
    const message = new Uint8Array(paddedLength);
    message.set(source);
    message[source.length] = 0x80;
    const bitLength = BigInt(source.length) * 8n;
    for (let index = 0; index < 8; index++) {
      message[paddedLength - 1 - index] = Number((bitLength >> BigInt(index * 8)) & 0xffn);
    }

    const constants = [
      0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5,
      0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174,
      0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
      0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967,
      0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85,
      0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
      0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3,
      0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208, 0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2,
    ];
    const hash = [0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a, 0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19];
    const words = new Uint32Array(64);

    for (let offset = 0; offset < message.length; offset += 64) {
      for (let index = 0; index < 16; index++) {
        const at = offset + index * 4;
        words[index] = ((message[at] << 24) | (message[at + 1] << 16) | (message[at + 2] << 8) | message[at + 3]) >>> 0;
      }
      for (let index = 16; index < 64; index++) {
        const x = words[index - 15];
        const y = words[index - 2];
        const sigma0 = rotateRight(x, 7) ^ rotateRight(x, 18) ^ (x >>> 3);
        const sigma1 = rotateRight(y, 17) ^ rotateRight(y, 19) ^ (y >>> 10);
        words[index] = (words[index - 16] + sigma0 + words[index - 7] + sigma1) >>> 0;
      }

      let [a, b, c, d, e, f, g, h] = hash;
      for (let index = 0; index < 64; index++) {
        const sum1 = rotateRight(e, 6) ^ rotateRight(e, 11) ^ rotateRight(e, 25);
        const choice = (e & f) ^ (~e & g);
        const t1 = (h + sum1 + choice + constants[index] + words[index]) >>> 0;
        const sum0 = rotateRight(a, 2) ^ rotateRight(a, 13) ^ rotateRight(a, 22);
        const majority = (a & b) ^ (a & c) ^ (b & c);
        const t2 = (sum0 + majority) >>> 0;
        h = g; g = f; f = e; e = (d + t1) >>> 0;
        d = c; c = b; b = a; a = (t1 + t2) >>> 0;
      }
      hash[0] = (hash[0] + a) >>> 0; hash[1] = (hash[1] + b) >>> 0;
      hash[2] = (hash[2] + c) >>> 0; hash[3] = (hash[3] + d) >>> 0;
      hash[4] = (hash[4] + e) >>> 0; hash[5] = (hash[5] + f) >>> 0;
      hash[6] = (hash[6] + g) >>> 0; hash[7] = (hash[7] + h) >>> 0;
    }
    return hash.map((part) => part.toString(16).padStart(8, "0")).join("");
  }

  async function sha256Hex(buffer) {
    if (globalThis.crypto && globalThis.crypto.subtle) {
      try {
        const digest = await globalThis.crypto.subtle.digest("SHA-256", buffer);
        return [...new Uint8Array(digest)].map((byte) => byte.toString(16).padStart(2, "0")).join("");
      } catch {
        return sha256Fallback(buffer);
      }
    }
    return sha256Fallback(buffer);
  }

  function openDatabase() {
    if (!globalThis.indexedDB) return Promise.resolve(null);
    if (!databasePromise) {
      databasePromise = new Promise((resolve, reject) => {
        const request = indexedDB.open("scrapheap-worker-cache", 1);
        request.onupgradeneeded = () => request.result.createObjectStore("datasets", { keyPath: "file" });
        request.onsuccess = () => resolve(request.result);
        request.onerror = () => reject(request.error || new Error("could not open browser dataset cache"));
      }).catch(() => null);
    }
    return databasePromise;
  }

  async function readCache(file) {
    const db = await openDatabase();
    if (!db) return memoryCache.get(file);
    return new Promise((resolve) => {
      const request = db.transaction("datasets", "readonly").objectStore("datasets").get(file);
      request.onsuccess = () => resolve(request.result);
      request.onerror = () => resolve(undefined);
    });
  }

  async function writeCache(record) {
    const db = await openDatabase();
    if (!db) {
      memoryCache.set(record.file, record);
      return;
    }
    await new Promise((resolve, reject) => {
      const transaction = db.transaction("datasets", "readwrite");
      transaction.objectStore("datasets").put(record);
      transaction.oncomplete = resolve;
      transaction.onerror = () => reject(transaction.error || new Error("could not write browser dataset cache"));
      transaction.onabort = () => reject(transaction.error || new Error("browser dataset cache write aborted"));
    });
  }

  async function loadFile(file) {
    const record = await readCache(file);
    return record && record.bytes ? record.bytes : null;
  }

  async function prepareManifest(manifest, onProgress = () => {}) {
    for (let index = 0; index < manifest.length; index++) {
      const item = manifest[index];
      if (!item || !item.file || !/^[a-f0-9]{64}$/i.test(item.sha256)) throw new Error("invalid dataset manifest entry");
      onProgress(`Checking dataset ${index + 1}/${manifest.length}: ${item.file}`);
      let record = await readCache(item.file);
      let bytes = record && record.sha256 === item.sha256 ? record.bytes : null;
      if (bytes) {
        const cachedHash = await sha256Hex(bytes);
        if (cachedHash !== item.sha256.toLowerCase()) bytes = null;
      }
      if (!bytes) {
        onProgress(`Downloading ${item.file}`);
        const response = await fetch(`/dataset/${encodeURIComponent(item.file)}`, { cache: "no-store" });
        if (!response.ok) throw new Error(`Could not download ${item.file}: HTTP ${response.status}`);
        bytes = await response.arrayBuffer();
        if (item.size >= 0 && bytes.byteLength !== item.size) throw new Error(`Size check failed for ${item.file}`);
        const actualHash = await sha256Hex(bytes);
        if (actualHash !== item.sha256.toLowerCase()) throw new Error(`SHA-256 check failed for ${item.file}`);
        record = { file: item.file, sha256: item.sha256.toLowerCase(), bytes };
        await writeCache(record);
      }
    }
    onProgress(manifest.length ? "Dataset ready." : "No dataset files configured; burn tasks are ready.");
  }

  const api = { sha256Hex, sha256Fallback, loadFile, prepareManifest };
  globalThis.ScrapHeapDataset = api;
  if (typeof module !== "undefined" && module.exports) module.exports = api;
})();
