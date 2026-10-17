// @ts-check
// Her yan menü sayfası + Hesabım + Denetim: koyu/açık tema ve İngilizce'de
// sıfır konsol hatası ve sıfır CSP ihlali; mobil görünümde yatay taşma yok.
const { test, expect } = require('@playwright/test');
const { ALL_PAGES, login, watch, visit, setPrefs, horizontalOverflow } = require('./helpers');

const VARIANTS = [
  { name: 'koyu tema', theme: 'dark', colorScheme: 'dark' },
  { name: 'açık tema', theme: 'light', colorScheme: 'light' },
  { name: 'English', theme: 'auto', colorScheme: 'dark', lang: 'en' },
];

for (const v of VARIANTS) {
  test.describe(`sayfalar · ${v.name}`, () => {
    test.use({ colorScheme: v.colorScheme });

    test('konsol hatası ve CSP ihlali yok', async ({ page, context, baseURL }) => {
      await setPrefs(context, baseURL, { theme: v.theme, lang: v.lang });
      const w = await watch(page);
      await login(page, 'admin');

      for (const p of ALL_PAGES) {
        await test.step(p.path, async () => {
          const res = await visit(page, p.path);
          expect.soft(res && res.status(), `${p.path} HTTP durumu`).toBe(200);
          // Sunucu tercihi ilk baytta basar: tema ve dil.
          await expect.soft(page.locator('html')).toHaveAttribute('data-theme', v.theme);
          if (v.lang) await expect.soft(page.locator('html')).toHaveAttribute('lang', v.lang);
          await expect.soft(page.locator('main#main')).toBeVisible();
          // Yan menüde etkin bağlantı (Hesabım menüde değildir).
          if (p.key !== 'account') {
            await expect.soft(page.locator(`.kh-nav-link[data-nav-key="${p.key}"]`)).toHaveAttribute('aria-current', 'page');
          }
          expect.soft(await w.csp(), `${p.path} CSP ihlalleri`).toEqual([]);
          expect.soft(w.take(), `${p.path} konsol hataları`).toEqual([]);
        });
      }
    });
  });
}

test.describe('mobil (390px)', () => {
  test.use({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true, deviceScaleFactor: 2 });

  for (const theme of ['dark', 'light']) {
    test(`yatay taşma yok · ${theme}`, async ({ page, context, baseURL }) => {
      await setPrefs(context, baseURL, { theme });
      const w = await watch(page);
      await login(page, 'admin');
      for (const p of ALL_PAGES) {
        await test.step(p.path, async () => {
          await visit(page, p.path);
          expect.soft(await horizontalOverflow(page), `${p.path} yatay taşma`).toBeNull();
          expect.soft(await w.csp(), `${p.path} CSP ihlalleri`).toEqual([]);
          expect.soft(w.take(), `${p.path} konsol hataları`).toEqual([]);
        });
      }

      // Mobil menü: hamburger yan menüyü açar, bağlantı gezinir.
      await visit(page, '/dashboard');
      await page.locator('.kh-burger').click();
      await expect(page.locator('html')).toHaveClass(/nav-open/);
      await page.locator('.kh-nav-link[data-nav-key="results"]').click();
      await expect(page).toHaveURL(/\/results$/);
    });
  }
});
