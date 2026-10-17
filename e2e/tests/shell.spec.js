// @ts-check
// Uygulama kabuğu: komut paleti (Ctrl+K) ve kalıcı tema tercihi.
const { test, expect } = require('@playwright/test');
const { login, visit, watch } = require('./helpers');

test('komut paleti Ctrl+K ile açılır ve gezinir', async ({ page }) => {
  const w = await watch(page);
  await login(page, 'analyst');
  await visit(page, '/dashboard');

  await page.keyboard.press('Control+K');
  const palette = page.locator('.kh-palette-overlay');
  const input = palette.locator('input');
  await expect(palette.locator('[role="dialog"]')).toBeVisible();
  await expect(input).toBeFocused();

  // Esc kapatır
  await page.keyboard.press('Escape');
  await expect(palette.locator('[role="dialog"]')).toBeHidden();

  // Yeniden aç, yan menüdeki "Bulgular" etiketini ara (dil bağımsız) ve klavyeyle seç.
  const label = (await page.locator('.kh-nav-link[data-nav-key="results"] .kh-nav-text').textContent()).trim();
  await page.keyboard.press('Control+K');
  await expect(input).toBeFocused();
  await input.fill(label);
  const target = palette.locator('.kh-palette-item', { has: page.locator('mark', { hasText: label }) }).first();
  await expect(target).toBeVisible();
  // Seçimi hedefe taşı (ilk öğeler serbest metin araması/süzmesidir) ve Enter.
  const items = palette.locator('.kh-palette-item');
  const idx = await target.getAttribute('data-i');
  for (let i = 0; i < Number(idx); i++) await page.keyboard.press('ArrowDown');
  await expect(target).toHaveAttribute('aria-selected', 'true');
  expect(await items.count()).toBeGreaterThan(Number(idx));
  await Promise.all([page.waitForURL(/\/results$/), page.keyboard.press('Enter')]);
  await expect(page.locator('.kh-nav-link[data-nav-key="results"]')).toHaveAttribute('aria-current', 'page');

  // Serbest metin: "Bulgularda süz" eylemi sorguyu taşır.
  await page.keyboard.press('Control+K');
  await input.fill('lockbit');
  await palette.locator('.kh-palette-item').nth(1).click();
  await expect(page).toHaveURL(/\/results\?text=lockbit/);
  expect(w.take()).toEqual([]);
});

test('tema tercihi yeniden yüklemede korunur', async ({ page, context }) => {
  await login(page, 'analyst');
  await visit(page, '/results');
  const html = page.locator('html');

  // Kullanıcı menüsü → Açık
  await page.locator('.kh-avatar').click();
  await page.locator('#kh-user-menu [data-action="theme-set"][data-value="light"]').click();
  await expect(html).toHaveAttribute('data-theme', 'light');
  await expect(page.locator('#kh-user-menu [data-value="light"]')).toHaveAttribute('aria-pressed', 'true');
  expect((await context.cookies()).find((c) => c.name === 'theme')?.value).toBe('light');

  await page.reload();
  await expect(html).toHaveAttribute('data-theme', 'light');
  const bgLight = await page.evaluate(() => getComputedStyle(document.body).backgroundColor);

  // Üst bardaki döngü düğmesi: açık → koyu; sunucu yeni tercihi ilk baytta basar.
  await page.locator('.kh-topbar [data-action="theme-cycle"]').click();
  await expect(html).toHaveAttribute('data-theme', 'dark');
  await page.reload();
  await expect(html).toHaveAttribute('data-theme', 'dark');
  const bgDark = await page.evaluate(() => getComputedStyle(document.body).backgroundColor);
  expect(bgDark).not.toBe(bgLight);

  // Başka bir sayfada da geçerli.
  await visit(page, '/dashboard');
  await expect(html).toHaveAttribute('data-theme', 'dark');
});
