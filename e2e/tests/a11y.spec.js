// @ts-check
// Erişilebilirlik: ana sayfalarda axe-core taraması; 'serious' ya da
// 'critical' etkili WCAG 2.1 A/AA ihlali olmamalı (koyu ve açık tema).
//
// Bastırma politikası: ihlal uygulamada düzeltilir. Bir kural yalnız
// gerekçesi helpers.js → AXE_EXCLUDE içinde belgelenerek ve dar bir seçiciyle
// hariç tutulabilir.
const { test, expect } = require('@playwright/test');
const { ALL_PAGES, login, visit, setPrefs, scanA11y: scan } = require('./helpers');

for (const theme of ['dark', 'light']) {
  test.describe(`axe · ${theme}`, () => {
    test.use({ colorScheme: theme });

    test('giriş sayfası', async ({ page, context, baseURL }) => {
      await setPrefs(context, baseURL, { theme });
      await visit(page, '/login');
      expect(await scan(page)).toEqual([]);
    });

    test('uygulama sayfaları', async ({ page, context, baseURL }) => {
      test.setTimeout(120_000);
      await setPrefs(context, baseURL, { theme });
      await login(page, 'admin');
      for (const p of ALL_PAGES) {
        await test.step(p.path, async () => {
          await visit(page, p.path);
          expect.soft(await scan(page), `${p.path} erişilebilirlik ihlalleri`).toEqual([]);
        });
      }
    });

    test('markalı 403 sayfası', async ({ page, context, baseURL }) => {
      await setPrefs(context, baseURL, { theme });
      await login(page, 'viewer');
      const res = await visit(page, '/users');
      expect(res && res.status()).toBe(403);
      expect(await scan(page)).toEqual([]);
    });
  });
}
