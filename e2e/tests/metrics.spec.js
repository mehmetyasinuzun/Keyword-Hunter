// @ts-check
// Prometheus /metrics: Ayarlar'da durum görünür; uç asla anonim değildir —
// yalnız admin hesabına ait "metrics" kapsamlı API token'ıyla (Bearer)
// okunur; bu token API'de reddedilir. CI sunucusu METRICS_ENABLED=true ile
// başlar; yerelde kapalıysa yalnız 404 denetlenir.
const { test, expect, request: pwRequest } = require('@playwright/test');
const { login, visit, watch, uniqueName } = require('./helpers');

/** Oturumdaki kullanıcı için verilen kapsamda token üretir (CSRF'li fetch). */
async function mkToken(page, scope) {
  return page.evaluate(async ([name, sc]) => {
    const r = await fetch('/api/tokens', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': window.KH.csrf() },
      body: JSON.stringify({ name, scope: sc, expiresInDays: 1 }),
    });
    const b = await r.json();
    return { status: r.status, token: b.token, id: b.meta && b.meta.id, code: b.code };
  }, [uniqueName('prom-'), scope]);
}

/** Bearer ile GET; durum ve (JSON ise) hata kodu. */
async function bearerGet(baseURL, token, path) {
  const ctx = await pwRequest.newContext({ baseURL, extraHTTPHeaders: { Authorization: 'Bearer ' + token } });
  try {
    const r = await ctx.get(path);
    let code;
    try { code = (await r.json()).code; } catch { code = undefined; }
    return { status: r.status(), code };
  } finally {
    await ctx.dispose();
  }
}

async function revoke(page, id) {
  await page.evaluate(async (tid) => {
    await fetch(`/api/tokens/${tid}/revoke`, { method: 'POST', headers: { 'X-CSRF-Token': window.KH.csrf() } });
  }, id);
}

test('metrikler: ayarlar bölümü, admin metrics token 200 (API\'de 403), read token 403, analist 403, anonim 401', async ({ page, browser, baseURL }) => {
  const w = await watch(page);
  await login(page, 'admin');
  await visit(page, '/settings');
  const sec = page.locator('#sec-metrics');
  await expect(sec).toBeVisible();
  await expect(page.locator('nav.st-nav a[href="#sec-metrics"]')).toBeVisible();
  await expect(sec).toContainText('METRICS_ENABLED');
  await expect(sec).toContainText('GET /metrics');
  const enabled = (await sec.locator('dd.kh-mono').first().innerText()).trim() === 'true';
  if (process.env.CI) expect(enabled, 'CI sunucusu METRICS_ENABLED=true ile başlamalı').toBe(true);
  expect(await w.csp()).toEqual([]);
  expect(w.take()).toEqual([]);

  const anon = await pwRequest.newContext({ baseURL });
  try {
    const r = await anon.get('/metrics');
    if (!enabled) {
      expect(r.status()).toBe(404);
      await expect(page.locator('#met-off')).toContainText('METRICS_ENABLED=true');
      return;
    }
    expect(r.status()).toBe(401);
    expect(r.headers()['www-authenticate']).toMatch(/^Bearer/);
  } finally {
    await anon.dispose();
  }

  // Admin hesabının metrics token'ı: Prometheus metin biçimi.
  const adminTok = await mkToken(page, 'metrics');
  expect(adminTok.status).toBe(201);
  const api = await pwRequest.newContext({ baseURL, extraHTTPHeaders: { Authorization: 'Bearer ' + adminTok.token } });
  try {
    const r = await api.get('/metrics');
    expect(r.status()).toBe(200);
    expect(r.headers()['content-type']).toMatch(/^text\/plain/);
    const body = await r.text();
    expect(body).toMatch(/^keywordhunter_build_info\{goversion="go[^"]+",version="[^"]+"\} 1$/m);
    expect(body).toMatch(/^keywordhunter_findings\{severity="5"\} [1-9]\d*$/m); // tohum verisi
    expect(body).toMatch(/^keywordhunter_http_requests_total\{method="GET",route="\/settings",status_class="2xx"\} [1-9]/m);
    expect(body).toMatch(/^keywordhunter_login_attempts_total\{method="password",result="success"\} [1-9]/m);
    expect(body).toContain('# TYPE keywordhunter_http_request_duration_seconds histogram');
    expect(body).toContain('go_goroutines');
    // Ham yol / kullanıcı adı etiketlere sızmaz.
    expect(body).not.toMatch(/route="\/api\/tokens\/\d+/);
    expect(body).not.toContain('username=');
  } finally {
    await api.dispose();
  }
  // metrics token'ı bulgu okuyamaz: API ve TAXII'de 403.
  expect(await bearerGet(baseURL, adminTok.token, '/api/export/results?format=json')).toEqual({ status: 403, code: 'token_metrics_only' });
  expect((await bearerGet(baseURL, adminTok.token, '/taxii2/')).status).toBe(403);
  await revoke(page, adminTok.id);

  // Admin'in genel amaçlı read token'ı /metrics'te kabul edilmez.
  const readTok = await mkToken(page, 'read');
  expect(readTok.status).toBe(201);
  expect(await bearerGet(baseURL, readTok.token, '/metrics')).toEqual({ status: 403, code: 'metrics_scope_required' });
  await revoke(page, readTok.id);

  // Analist metrics token'ı oluşturamaz; read token'ı /metrics'te 403.
  const ctx = await browser.newContext();
  const ap = await ctx.newPage();
  try {
    await login(ap, 'analyst');
    const denied = await mkToken(ap, 'metrics');
    expect(denied.status).toBe(400);
    expect(denied.code).toBe('token_metrics_admin_only');
    const t = await mkToken(ap, 'read');
    expect(t.status).toBe(201);
    expect(await bearerGet(baseURL, t.token, '/metrics')).toEqual({ status: 403, code: 'metrics_scope_required' });
    await revoke(ap, t.id);
  } finally {
    await ctx.close();
  }
});
