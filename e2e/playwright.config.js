// @ts-check
// KeywordHunter uçtan uca test yapılandırması.
//
// Testler çalışan bir sunucuya karşı koşar (sunucuyu CI işi ya da geliştirici
// başlatır; bkz. CONTRIBUTING.md → "Uçtan uca testler"). Veritabanı
// `go run ./cmd/khseed -reset` ile tohumlanmış olmalıdır: testler demo
// hesaplarını (analyst / viewer) ve demo verisini kullanır.
//
// Ortam değişkenleri:
//   KH_BASE_URL     sunucu adresi (varsayılan http://127.0.0.1:8080)
//   KH_ADMIN_USER   admin kullanıcı adı (varsayılan admin)
//   KH_ADMIN_PASS   admin parolası (zorunlu; .env'deki ADMIN_PASS)
const { defineConfig, devices } = require('@playwright/test');

const CI = !!process.env.CI;

module.exports = defineConfig({
  testDir: './tests',
  outputDir: './test-results',
  timeout: 60_000,
  expect: { timeout: 10_000 },
  fullyParallel: true,
  forbidOnly: CI,
  retries: CI ? 1 : 0,
  // Tek SQLite bağlantısı paylaşıldığından aşırı paralellik yalnız beklemeyi artırır.
  workers: CI ? 2 : 4,
  reporter: CI
    ? [['list'], ['html', { open: 'never', outputFolder: 'playwright-report' }], ['github']]
    : [['list'], ['html', { open: 'never', outputFolder: 'playwright-report' }]],
  use: {
    baseURL: process.env.KH_BASE_URL || 'http://127.0.0.1:8080',
    trace: 'on-first-retry',
    screenshot: 'only-on-failure',
    video: 'off',
    locale: 'tr-TR',
    timezoneId: 'Europe/Istanbul',
    actionTimeout: 10_000,
    navigationTimeout: 20_000,
  },
  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'], viewport: { width: 1440, height: 900 } },
    },
  ],
});
