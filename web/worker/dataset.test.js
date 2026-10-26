"use strict";

const assert = require("node:assert/strict");
const test = require("node:test");
const { sha256Fallback } = require("./dataset.js");

test("SHA-256 fallback matches standard vectors", () => {
  const encoder = new TextEncoder();
  assert.equal(sha256Fallback(encoder.encode("")), "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855");
  assert.equal(sha256Fallback(encoder.encode("abc")), "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad");
});
