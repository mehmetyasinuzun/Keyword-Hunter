// @ts-check
// Motor izleme: Tor kontrol portu yapılandırılmamışken "Yeni Tor devresi"
// düğmesi sessiz kalmamalı — tıklama da klavye (Enter) de nedeni uyarı olarak
// göstermeli (regresyon: aria-disabled çekirdek dağıtıcıda tıklamayı yutuyordu).
const { test, expect } = require('@playwright/test');
const { login, watch, visit } = require('./helpers');

test('Tor yapılandırılmamış: yeni devre düğmesi nedenini söyler', async ({ page }) => {
  const w = await watch(page);
  await login(page, 'admin');

  const statusResp = page.waitForResponse((r) => r.url().endsWith('/api/tor/status'));
  await visit(page, '/monitor');
  const status = await (await statusResp).json();
  test.skip(status.configured === true, 'Bu ortamda TOR_CONTROL yapılandırılmış');

  const btn = page.locator('#newnym-btn');
  await expect(btn).toHaveClass(/is-off/);
  // Etkin kalır; neden ekran okuyuculara aria-describedby ile iletilir.
  await expect(btn).not.toHaveAttribute('aria-disabled', 'true');
  await expect(btn).toBeEnabled();
  await expect(btn).toHaveAccessibleDescription(status.hint);

  const warn = page.locator('#kh-toasts .kh-toast.warn');
  await btn.click();
  await expect(warn.last()).toContainText(status.hint);

  // Klavye: odak + Enter da aynı uyarıyı verir.
  const before = await warn.count();
  await btn.focus();
  await page.keyboard.press('Enter');
  await expect(warn).toHaveCount(before + 1);

  expect(await w.csp()).toEqual([]);
  expect(w.take()).toEqual([]);
});
