// @ts-check
// Kimlik doğrulama: hatalı/başarılı giriş, 2FA kaydı + TOTP ile giriş ve
// denetim kaydında giriş olaylarının görünmesi.
const { test, expect } = require('@playwright/test');
const { USERS, login, logout, totp, uniqueName, createUserViaAdmin, scanA11y } = require('./helpers');

test.describe('giriş', () => {
  test('hatalı parola reddedilir, oturum açılmaz', async ({ page, context }) => {
    await page.goto('/login');
    await page.locator('#username').fill(USERS.analyst.username);
    await page.locator('#password').fill('kesinlikle-yanlis-parola');
    await Promise.all([page.waitForURL(/\/login\?error=1/), page.locator('#login-btn').click()]);

    await expect(page.locator('.kh-alert.err[role="alert"]')).toBeVisible();
    const cookies = await context.cookies();
    expect(cookies.find((c) => c.name === 'session')).toBeUndefined();

    // Korumalı sayfa girişe yönlendirir.
    await page.goto('/dashboard');
    await expect(page).toHaveURL(/\/login/);
  });

  test('doğru kimlikle panoya girilir; oturum çerezi HttpOnly', async ({ page, context }) => {
    await login(page, 'analyst');
    await expect(page.locator('h1.kh-title')).toBeVisible();
    await expect(page.locator('.kh-avatar')).toHaveAccessibleName(USERS.analyst.username);

    const session = (await context.cookies()).find((c) => c.name === 'session');
    expect(session, 'session çerezi').toBeTruthy();
    expect(session.httpOnly).toBe(true);
    expect(session.sameSite).not.toBe('None');

    await logout(page);
    await page.goto('/results');
    await expect(page).toHaveURL(/\/login/);
  });
});

test.describe('iki adımlı doğrulama', () => {
  test('kayıt sihirbazı + TOTP ile giriş', async ({ browser }) => {
    // Her çalıştırmada yeni bir hesap: tekrar denemede (retry) kayıtlı 2FA çakışmasın.
    const user = { username: uniqueName('e2e2fa'), password: 'E2e-Tfa-Pass-' + Date.now() + '!' };
    const adminCtx = await browser.newContext();
    const admin = await adminCtx.newPage();
    await login(admin, 'admin');
    await createUserViaAdmin(admin, { ...user, role: 'analyst' });
    await adminCtx.close();

    const ctx = await browser.newContext();
    const page = await ctx.newPage();
    await login(page, user);
    await page.goto('/account#security-2fa');
    const card = page.locator('#security-2fa');
    await expect(card).toHaveAttribute('data-state', 'off');

    // 1. adım: QR + elle girilecek sır
    await page.locator('#tfa-start-btn').click();
    const modal = page.locator('#tfa-modal');
    await expect(modal).toHaveClass(/open/);
    await expect(page.locator('#tfa-qr')).toHaveAttribute('src', /^data:image\//);
    const secret = await page.locator('#tfa-secret-copy').getAttribute('data-copy');
    expect(secret).toMatch(/^[A-Z2-7]{16,}$/);

    // 2. adım: parola + güncel kod (6. hanede otomatik doğrulanır)
    await modal.locator('[data-action="tfa-goto"][data-step="2"]').click();
    // Tanı: doğrulama yanıtı (başarısızlıkta hata iletisine eklenir; gövdede sır yok).
    const enable = page.waitForResponse((r) => r.url().endsWith('/api/me/2fa/enable'));
    await expect(page.locator('#tfa-pw')).toBeVisible();
    await page.locator('#tfa-pw').fill(user.password);
    await page.locator('#tfa-code').fill(totp(secret));
    const res = await enable;
    expect(res.status(), 'etkinleştirme yanıtı: ' + (res.ok() ? '' : await res.text())).toBe(200);

    // 3. adım: tek seferlik kurtarma kodları
    const codes = page.locator('#tfa-codes li');
    await expect(codes.first()).toBeVisible();
    expect(await codes.count()).toBeGreaterThanOrEqual(8);
    await expect(page.locator('#tfa-done')).toBeDisabled();
    await page.locator('#tfa-saved').check();
    await page.locator('#tfa-done').click();
    await expect(modal).not.toHaveClass(/open/);
    await expect(card).toHaveAttribute('data-state', 'on');
    // Sihirbaz kapanınca sır ve kodlar DOM'dan silinir.
    await expect(page.locator('#tfa-secret')).toHaveText('');
    await expect(page.locator('#tfa-codes li')).toHaveCount(0);

    await logout(page);

    // Parola doğru → ikinci adım; oturum henüz yok.
    await page.locator('#username').fill(user.username);
    await page.locator('#password').fill(user.password);
    await Promise.all([page.waitForURL('**/login/2fa'), page.locator('#login-btn').click()]);
    expect((await ctx.cookies()).find((c) => c.name === 'session')).toBeUndefined();
    expect(await scanA11y(page), '/login/2fa erişilebilirlik').toEqual([]);

    // Yanlış kod reddedilir (kod 6. hanede kendiliğinden gönderilir).
    const good = totp(secret, { stepOffset: 1 });
    const wrong = String((Number(good) + 500000) % 1000000).padStart(6, '0');
    await Promise.all([page.waitForURL(/\/login\/2fa\?.*error=1/), page.locator('#code').fill(wrong)]);
    await expect(page.locator('#code-err')).toBeVisible();

    // Bir sonraki zaman adımının kodu (±1 adım tolerans): etkinleştirmede
    // kullanılan adım tekrar kabul edilmez (yeniden oynatma koruması).
    await Promise.all([page.waitForURL('**/dashboard'), page.locator('#code').fill(good)]);
    await expect(page.locator('.kh-avatar')).toHaveAccessibleName(user.username);
    expect((await ctx.cookies()).find((c) => c.name === 'session')).toBeTruthy();
    await ctx.close();
  });
});

test.describe('denetim kaydı', () => {
  test('giriş olayları denetim sayfasında görünür', async ({ browser, page }) => {
    // Tohum verisinde admin'in eski auth.login.success olayları var; bu yüzden
    // canlı girişi kanıtlamak için bu koşuya özgü yeni bir kullanıcı açılır ve
    // onun (tohumda olamayacak) başarılı girişi aranır.
    await login(page, 'admin');
    const fresh = { username: uniqueName('e2eaud'), password: 'E2e-Audit-Pass-' + Date.now() + '!' };
    await createUserViaAdmin(page, { ...fresh, role: 'viewer' });

    // Ayrı bağlamda: yeni kullanıcının başarılı girişi + bilinmeyen adla başarısız deneme
    const ghost = uniqueName('ghost');
    const other = await browser.newContext();
    const p2 = await other.newPage();
    await login(p2, fresh);
    await logout(p2);
    await p2.locator('#username').fill(ghost);
    await p2.locator('#password').fill('yanlis-parola-123');
    await Promise.all([p2.waitForURL(/error=1/), p2.locator('#login-btn').click()]);
    await other.close();

    await page.goto('/audit');
    await expect(page.locator('#a-body tr.a-row').first()).toBeVisible();

    // Başarısız deneme: hedef "user:<ad>", başarısız rozeti
    await page.locator('#a-q').fill(ghost);
    const failRow = page.locator('#a-body tr.a-row', { hasText: 'user:' + ghost });
    await expect(failRow).toHaveCount(1);
    await expect(failRow).toHaveClass(/is-fail/);
    await expect(failRow.locator('code.a-code')).toHaveText('auth.login.failure');

    // Bu koşudaki başarılı giriş: yalnız yeni kullanıcının olayları
    await page.locator('#a-q').fill('');
    await page.locator('#a-actor').fill(fresh.username);
    await page.locator('#a-actor').dispatchEvent('change');
    const okRow = page.locator('#a-body tr.a-row', { has: page.locator('code.a-code', { hasText: /^auth\.login\.success$/ }) });
    await expect(okRow).toHaveCount(1);
    await expect(okRow).not.toHaveClass(/is-fail/);
    await expect(okRow.locator('.a-target')).toHaveText('user:' + fresh.username);

    // Zincir doğrulaması geçer
    await page.locator('#a-verify').click();
    await expect(page.locator('#a-chain')).toHaveClass(/green/);
  });
});
