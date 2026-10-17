// @ts-check
// Testlerdeki TOTP uygulamasının kendisi doğru mu? RFC 6238 Ek B test
// vektörleri (HMAC-SHA1, 8 hane) ve RFC 4648 base32. Tarayıcı gerektirmez.
const { test, expect } = require('@playwright/test');
const { totp, hotp, base32Decode } = require('./helpers');

// "12345678901234567890" (ASCII) → base32
const RFC_SECRET = 'GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ';

test('base32 çözümü RFC 4648 ile uyumlu', () => {
  expect(base32Decode(RFC_SECRET).toString('ascii')).toBe('12345678901234567890');
  expect(base32Decode('mzxw 6ytb oi======').toString('ascii')).toBe('foobar');
  expect(() => base32Decode('A1')).toThrow();
});

test('RFC 6238 Ek B test vektörleri (SHA-1)', () => {
  const vectors = [
    [59, '94287082'],
    [1111111109, '07081804'],
    [1111111111, '14050471'],
    [1234567890, '89005924'],
    [2000000000, '69279037'],
    [20000000000, '65353130'],
  ];
  for (const [t, want] of vectors) {
    expect(totp(RFC_SECRET, { time: t * 1000, digits: 8 }), `T=${t}`).toBe(want);
  }
  // 6 hane: son altı hane; stepOffset bir sonraki adımı verir.
  expect(totp(RFC_SECRET, { time: 59 * 1000 })).toBe('287082');
  expect(totp(RFC_SECRET, { time: 29 * 1000, stepOffset: 1 })).toBe(totp(RFC_SECRET, { time: 59 * 1000 }));
  // RFC 4226 Ek D: HOTP sayaç 0 → 755224
  expect(hotp(Buffer.from('12345678901234567890'), 0)).toBe('755224');
});
