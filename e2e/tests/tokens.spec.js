// @ts-check
// Kişisel API token'ı: arayüzden oluştur → Bearer ile kullan → arayüzden
// iptal et → aynı token 401.
const { test, expect, request: pwRequest } = require('@playwright/test');
const { login, visit, watch, uniqueName, USERS } = require('./helpers');

test('API token oluştur, Bearer ile kullan, iptal et → 401', async ({ page, baseURL }) => {
  const w = await watch(page);
  await login(page, 'analyst');
  await visit(page, '/account');

  const name = uniqueName('e2e-');
  await page.locator('#tok-new-btn').click();
  await expect(page.locator('#token-modal')).toHaveClass(/open/);
  await page.locator('#tok-name').fill(name);
  await page.locator('#token-form input[name="tok-scope"][value="read"]').check();
  await page.locator('#tok-expiry').selectOption('7');
  await page.locator('#tok-submit').click();

  // Tek seferlik gösterim
  const reveal = page.locator('#token-reveal');
  await expect(reveal).toHaveClass(/open/);
  const token = await page.locator('#tok-value').inputValue();
  expect(token).toMatch(/^kh_[A-Za-z0-9]{43}$/);
  await expect(page.locator('#tok-curl')).toContainText('Authorization: Bearer ' + token);
  // Arka plan tıklaması / Esc gizli değeri kapatmaz (data-static).
  await page.keyboard.press('Escape');
  await expect(reveal).toHaveClass(/open/);
  await reveal.locator('[data-action="modal-close"]').click();
  await expect(reveal).not.toHaveClass(/open/);
  // Kapanınca düz metin DOM'dan silinir.
  await expect(page.locator('#tok-value')).toHaveValue('');

  const row = page.locator('#tok-body tr', { hasText: name });
  await expect(row).toHaveCount(1);

  // Çerezsiz, yalnız Bearer başlıklı ayrı istek bağlamı
  const api = await pwRequest.newContext({ baseURL, extraHTTPHeaders: { Authorization: 'Bearer ' + token } });
  try {
    const who = await api.get('/api/whoami');
    expect(who.status()).toBe(200);
    const body = await who.json();
    expect(body).toMatchObject({ username: USERS.analyst.username, authMethod: 'token', tokenScope: 'read' });
    const results = await api.get('/api/results?limit=1');
    expect(results.status()).toBe(200);
    // "read" kapsamı yazamaz (CSRF gerekmez; token yolu)
    const write = await api.post('/api/watchlist', { data: { name: 'x', url: 'http://' + 'a'.repeat(55) + 'd.onion' } });
    expect(write.status()).toBe(403);
    // Token, token yönetimi uçlarını çağıramaz (yalnız etkileşimli oturum)
    expect((await api.get('/api/tokens')).status()).toBe(403);

    // Son kullanım arayüze yansır
    await page.reload();
    await expect(page.locator('#tok-body tr', { hasText: name })).not.toContainText(/hiç kullanılmadı|never used/);

    // Arayüzden iptal
    await page.locator('#tok-body tr', { hasText: name }).locator('[data-action="tok-revoke"]').click();
    await page.locator('.kh-modal-overlay.open [data-k="yes"]').click();
    await expect(page.locator('#tok-body tr', { hasText: name })).toHaveClass(/is-inactive/);
    await expect(page.locator('#tok-body tr', { hasText: name }).locator('[data-action="tok-revoke"]')).toHaveCount(0);

    const after = await api.get('/api/whoami');
    expect(after.status()).toBe(401);
  } finally {
    await api.dispose();
  }
  expect(w.take()).toEqual([]);
});
