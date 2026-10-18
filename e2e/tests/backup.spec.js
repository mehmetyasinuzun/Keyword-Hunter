// @ts-check
// Ayarlar → Yedekleme / Saklama süresi: admin "Şimdi yedek indir" ile geçerli bir
// SQLite dosyası indirir (data-action, CSP ihlali yok); analist uç noktayı çağıramaz.
const fs = require('node:fs');
const { test, expect } = require('@playwright/test');
const { login, watch, visit } = require('./helpers');

test('admin: yedek indir + saklama politikası görünür', async ({ page }) => {
  const w = await watch(page);
  await login(page, 'admin');
  await visit(page, '/settings');

  await expect(page.locator('#sec-backup')).toBeVisible();
  await expect(page.locator('#sec-retention')).toBeVisible();
  await expect(page.locator('#sec-retention')).toContainText('RESULT_RETENTION_DAYS');
  // Zamanlanmış yedek ve saklama kapalı (e2e ortamı): açma ipuçları ve denetim kaydı süresi görünür
  await expect(page.locator('#bk-last')).toContainText('BACKUP_DIR=/data/backups');
  await expect(page.locator('#ret-last')).toContainText('RESULT_RETENTION_DAYS=180');
  await expect(page.locator('#sec-retention')).toContainText('AUDIT_RETENTION_DAYS');

  const btn = page.locator('#bk-download');
  const [download] = await Promise.all([page.waitForEvent('download'), btn.click()]);
  expect(download.suggestedFilename()).toMatch(/^keywordhunter-[0-9A-Za-z._+-]+-\d{8}T\d{6}Z\.db$/);
  const file = await download.path();
  const head = fs.readFileSync(file).subarray(0, 16).toString('latin1');
  expect(head).toBe('SQLite format 3\u0000');
  await expect(page.locator('#kh-toasts .kh-toast.ok').last()).toBeVisible();
  await expect(btn).toBeEnabled();

  expect(await w.csp()).toEqual([]);
  expect(w.take()).toEqual([]);
});

test('analyst yedek uç noktasını çağıramaz', async ({ page }) => {
  await login(page, 'analyst');
  const status = await page.evaluate(async () => {
    const r = await fetch('/api/admin/backup', { method: 'POST', headers: { 'X-CSRF-Token': window.KH.csrf() } });
    return r.status;
  });
  expect(status).toBe(403);
});
