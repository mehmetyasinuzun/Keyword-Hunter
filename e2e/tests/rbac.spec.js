// @ts-check
// Rol tabanlı erişim: viewer admin bağlantılarını görmez, admin sayfalarında
// markalı 403 alır ve yazma API'leri reddedilir.
const { test, expect } = require('@playwright/test');
const { login, visit, watch, SIDEBAR_PAGES } = require('./helpers');

const ADMIN_KEYS = SIDEBAR_PAGES.filter((p) => p.admin).map((p) => p.key);

test('viewer admin bağlantılarını görmez ve /users için markalı 403 alır', async ({ page, browser }) => {
  // Admin sayfalarının menü etiketleri (aynı dilde) — palette aranacak.
  const adminCtx = await browser.newContext();
  const adminPage = await adminCtx.newPage();
  await login(adminPage, 'admin');
  const navLabel = (p, key) => p.locator(`.kh-nav-link[data-nav-key="${key}"] .kh-nav-text`).innerText();
  const adminLabels = [];
  for (const key of ADMIN_KEYS) adminLabels.push((await navLabel(adminPage, key)).trim());
  await adminCtx.close();
  expect(adminLabels.every(Boolean), 'admin menü etiketleri').toBe(true);

  const w = await watch(page);
  await login(page, 'viewer');

  // Menü sunucuda süzülür: admin bağlantıları HTML'de hiç yok.
  for (const key of ADMIN_KEYS) {
    await expect(page.locator(`.kh-nav-link[data-nav-key="${key}"]`)).toHaveCount(0);
  }
  for (const p of SIDEBAR_PAGES.filter((x) => !x.admin)) {
    await expect(page.locator(`.kh-nav-link[data-nav-key="${p.key}"]`)).toHaveCount(1);
  }
  // Komut paleti de admin sayfalarını listelemez (palet öğelerinin kendisi okunur).
  const dashLabel = (await navLabel(page, 'dashboard')).trim();
  await page.keyboard.press('Control+K');
  const items = page.locator('.kh-palette-item');
  await expect(items).not.toHaveCount(0);
  const labels = (await items.locator('> span:first-of-type').allInnerTexts()).map((t) => t.trim());
  expect(labels, 'palet okunabildi').toContain(dashLabel);
  for (const l of adminLabels) expect(labels, 'palette admin sayfası').not.toContain(l);
  await page.keyboard.press('Escape');

  for (const path of ['/users', '/audit', '/settings']) {
    const res = await visit(page, path);
    expect(res && res.status(), path).toBe(403);
    await expect(page.locator('.err-code')).toHaveText('403');
    await expect(page.locator('main#main h1.kh-title')).toBeVisible();
    await expect(page.locator('.err-card a.kh-btn.primary[href="/dashboard"]')).toBeVisible();
    // Hata sayfası kabuğu (yan menü) içermez ve hiçbir veri sızdırmaz.
    await expect(page.locator('.kh-sidebar')).toHaveCount(0);
    // Belgenin kendisinin 403'ü tarayıcı konsoluna düşer (beklenen); başka hata olmamalı.
    const unexpected = w.take().filter((m) => !(m.includes(path + ':') && /status of 403/.test(m)));
    expect(unexpected, path).toEqual([]);
    expect(await w.csp(), path).toEqual([]);
  }
});

test('viewer yazma API çağrıları 403 döner, okuma serbest', async ({ page }) => {
  await login(page, 'viewer');
  await visit(page, '/results');
  const out = await page.evaluate(async () => {
    // Geçerli CSRF ile: ret rol kapısından gelmeli, CSRF'ten değil.
    const post = (url, body) => fetch(url, { method: 'POST', headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': window.KH.csrf() }, body: JSON.stringify(body) });
    const read = await fetch('/api/results?limit=1');
    const write = await post('/api/watchlist', { name: 'x', url: 'http://' + 'a'.repeat(55) + 'd.onion', category: 'x' });
    const users = await fetch('/api/users');
    return { read: read.status, write: write.status, writeCode: (await write.json()).code, users: users.status };
  });
  expect(out.read).toBe(200);
  expect(out.write).toBe(403);
  expect(out.writeCode).toBe('viewer_read_only');
  expect(out.users).toBe(403);
});

test('analyst yazabilir ama admin sayfalarını göremez', async ({ page }) => {
  await login(page, 'analyst');
  await expect(page.locator('.kh-nav-link[data-nav-key="users"]')).toHaveCount(0);
  const res = await visit(page, '/settings');
  expect(res && res.status()).toBe(403);
  await expect(page.locator('.err-code')).toHaveText('403');
});
