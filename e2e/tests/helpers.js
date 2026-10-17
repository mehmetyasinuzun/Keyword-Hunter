// @ts-check
// Ortak yardımcılar: demo hesapları, giriş, sayfa listesi, konsol/CSP gözcüsü
// ve RFC 6238 TOTP (node:crypto; harici bağımlılık yok).
const crypto = require('node:crypto');
const { expect } = require('@playwright/test');
const { default: AxeBuilder } = require('@axe-core/playwright');

// cmd/khseed/dataset.go → DemoUsers ile aynı tutulmalıdır.
const USERS = {
  admin: {
    username: process.env.KH_ADMIN_USER || 'admin',
    get password() {
      const p = process.env.KH_ADMIN_PASS;
      if (!p) throw new Error('KH_ADMIN_PASS ortam değişkeni gerekli (sunucunun ADMIN_PASS değeri)');
      return p;
    },
  },
  analyst: { username: 'analyst', password: process.env.KH_ANALYST_PASS || 'Analyst-Demo-Pass-1!' },
  viewer: { username: 'viewer', password: process.env.KH_VIEWER_PASS || 'Viewer-Demo-Pass-1!' },
};

// Yan menüdeki her sayfa (pkg/web/render.go → navLayout) + kullanıcı menüsünden Hesabım.
const SIDEBAR_PAGES = [
  { key: 'dashboard', path: '/dashboard' },
  { key: 'search', path: '/search' },
  { key: 'results', path: '/results' },
  { key: 'graph', path: '/results/graph' },
  { key: 'analytics', path: '/analytics' },
  { key: 'scheduled', path: '/scheduled' },
  { key: 'watchlist', path: '/watchlist' },
  { key: 'crawl', path: '/crawl' },
  { key: 'monitor', path: '/monitor' },
  { key: 'users', path: '/users', admin: true },
  { key: 'audit', path: '/audit', admin: true },
  { key: 'settings', path: '/settings', admin: true },
];
const ALL_PAGES = [...SIDEBAR_PAGES, { key: 'account', path: '/account' }];

/** Benzersiz, zaman damgalı kısa ad (tekrar denemelerde çakışmasın). */
function uniqueName(prefix) {
  return `${prefix}${Date.now().toString(36)}${crypto.randomBytes(2).toString('hex')}`;
}

/** Tema/dil çerezlerini bağlama yazar (sunucu <html data-theme> ve dili ilk baytta basar). */
async function setPrefs(context, baseURL, { theme, lang } = {}) {
  const cookies = [];
  if (theme) cookies.push({ name: 'theme', value: theme, url: baseURL });
  if (lang) cookies.push({ name: 'lang', value: lang, url: baseURL });
  if (cookies.length) await context.addCookies(cookies);
}

/** Giriş formunu doldurur ve panoya yönlenmeyi bekler. */
async function login(page, who) {
  const u = typeof who === 'string' ? USERS[who] : who;
  await page.goto('/login');
  await page.locator('#username').fill(u.username);
  await page.locator('#password').fill(u.password);
  await Promise.all([page.waitForURL('**/dashboard'), page.locator('#login-btn').click()]);
}

/** Kullanıcı menüsündeki form ile çıkış yapar. */
async function logout(page) {
  await page.locator('.kh-avatar').click();
  await Promise.all([page.waitForURL('**/login**'), page.locator('form[action="/logout"] button[type="submit"]').click()]);
}

/**
 * Sayfa gözcüsü: konsol hataları, yakalanmamış istisnalar ve CSP ihlalleri.
 * CSP ihlalleri belge düzeyinde `securitypolicyviolation` olayıyla toplanır
 * (addInitScript her gezinmede sayfa betiklerinden ÖNCE çalışır).
 */
async function watch(page) {
  const problems = [];
  page.on('console', (m) => {
    if (m.type() === 'error') problems.push(`console.error @ ${page.url()}: ${m.text()}`);
  });
  page.on('pageerror', (e) => problems.push(`pageerror @ ${page.url()}: ${e.message}`));
  await page.addInitScript(() => {
    window.__khCSP = [];
    document.addEventListener('securitypolicyviolation', (e) => {
      window.__khCSP.push(`${e.violatedDirective} blocked=${e.blockedURI || '-'} src=${e.sourceFile || '-'}:${e.lineNumber || 0} sample=${e.sample || ''}`);
    });
  });
  return {
    problems,
    /** Belgedeki CSP ihlallerini döndürür. */
    async csp() {
      return page.evaluate(() => window.__khCSP || []).catch(() => []);
    },
    /** Sorun listesini boşaltır (sayfa başına ayrı raporlamak için). */
    take() {
      return problems.splice(0, problems.length);
    },
  };
}

/** Sayfayı açar ve ağ sakinleşene kadar bekler (grafik/API çağrıları dahil). */
async function visit(page, path) {
  const res = await page.goto(path, { waitUntil: 'load' });
  await page.waitForLoadState('networkidle', { timeout: 8_000 }).catch(() => {});
  return res;
}

/** Yatay taşma: belge genişliği görünüm alanını aşıyor mu (1px yuvarlama payı). */
async function horizontalOverflow(page) {
  return page.evaluate(() => {
    const vw = document.documentElement.clientWidth;
    const sw = Math.max(document.documentElement.scrollWidth, document.body.scrollWidth);
    if (sw <= vw + 1) return null;
    // Hata iletisi için taşan öğeleri bul (en dıştakiler).
    const out = [];
    for (const el of document.querySelectorAll('body *')) {
      const r = el.getBoundingClientRect();
      if (r.width > 0 && r.right > vw + 1 && getComputedStyle(el).position !== 'fixed') {
        const parent = el.parentElement && el.parentElement.getBoundingClientRect();
        if (!parent || parent.right <= vw + 1) {
          out.push(`${el.tagName.toLowerCase()}${el.id ? '#' + el.id : ''}${el.className && typeof el.className === 'string' ? '.' + el.className.trim().split(/\s+/).join('.') : ''} right=${Math.round(r.right)}`);
        }
      }
      if (out.length >= 5) break;
    }
    return { viewport: vw, scrollWidth: sw, culprits: out };
  });
}

// ── RFC 6238 TOTP (HMAC-SHA1, 6 hane, 30 sn) ─────────────────────────────

/** RFC 4648 base32 çözümü (boşluk/dolgu yok sayılır). */
function base32Decode(input) {
  const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567';
  const clean = String(input).toUpperCase().replace(/[\s=-]/g, '');
  let bits = 0;
  let value = 0;
  const out = [];
  for (const ch of clean) {
    const idx = alphabet.indexOf(ch);
    if (idx < 0) throw new Error('geçersiz base32 karakteri: ' + ch);
    value = (value << 5) | idx;
    bits += 5;
    if (bits >= 8) {
      out.push((value >>> (bits - 8)) & 0xff);
      bits -= 8;
    }
  }
  return Buffer.from(out);
}

/** RFC 4226 HOTP. */
function hotp(key, counter, digits = 6) {
  const msg = Buffer.alloc(8);
  msg.writeBigUInt64BE(BigInt(counter));
  const mac = crypto.createHmac('sha1', key).update(msg).digest();
  const off = mac[mac.length - 1] & 0x0f;
  const bin = ((mac[off] & 0x7f) << 24) | (mac[off + 1] << 16) | (mac[off + 2] << 8) | mac[off + 3];
  return String(bin % 10 ** digits).padStart(digits, '0');
}

/** RFC 6238 TOTP; stepOffset ile komşu zaman adımının kodu üretilir. */
function totp(secretB32, { time = Date.now(), period = 30, digits = 6, stepOffset = 0 } = {}) {
  const counter = Math.floor(time / 1000 / period) + stepOffset;
  return hotp(base32Decode(secretB32), counter, digits);
}

/** Admin oturumundaki sayfadan, uygulamanın kendi KH.api'si (CSRF dahil) ile kullanıcı açar. */
async function createUserViaAdmin(page, { username, password, role }) {
  const res = await page.evaluate(
    (u) => window.KH.api('/api/users', { method: 'POST', body: u }),
    { username, password, role },
  );
  expect(res.success).toBe(true);
  return res.user;
}

// ── Erişilebilirlik (axe-core) ────────────────────────────────────────────

const AXE_TAGS = ['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa'];
const AXE_BLOCKING = new Set(['serious', 'critical']);

// Belgelenmiş istisnalar: CSS seçici → gerekçe. İhlal uygulamada düzeltilir;
// buraya yalnız düzeltilemeyen (ör. üçüncü taraf bileşen) ve gerekçesi
// yazılmış dar bir seçici eklenir. Hedef bu listenin boş kalmasıdır.
const AXE_EXCLUDE = {};

/**
 * Sonlu CSS/Web animasyonlarının (giriş geçişleri, sayaçlar) bitmesini bekler:
 * kontrast yarı saydam bir ara karede değil, son hâlde ölçülsün. Sonsuz
 * animasyonlar (radar taraması, nabız) beklenmez.
 */
async function settleAnimations(page, timeout = 3_000) {
  await page
    .evaluate(
      (ms) =>
        Promise.race([
          Promise.all(
            document
              .getAnimations()
              .filter((a) => a.effect && a.effect.getComputedTiming().endTime !== Infinity)
              .map((a) => a.finished.catch(() => {})),
          ),
          new Promise((r) => setTimeout(r, ms)),
        ]),
      timeout,
    )
    .catch(() => {});
}

/** Sayfayı WCAG 2.1 A/AA ile tarar; 'serious'/'critical' ihlalleri okunur özet olarak döndürür. */
async function scanA11y(page) {
  await settleAnimations(page);
  let builder = new AxeBuilder({ page }).withTags(AXE_TAGS);
  for (const sel of Object.keys(AXE_EXCLUDE)) builder = builder.exclude(sel);
  const { violations } = await builder.analyze();
  return violations
    .filter((v) => AXE_BLOCKING.has(v.impact || ''))
    .map((v) => ({
      rule: v.id,
      impact: v.impact,
      help: v.help,
      nodes: v.nodes.slice(0, 5).map((n) => `${n.target.join(' ')} — ${(n.failureSummary || '').split('\n').slice(1, 2).join(' ').trim()}`),
    }));
}

module.exports = {
  scanA11y,
  USERS,
  SIDEBAR_PAGES,
  ALL_PAGES,
  uniqueName,
  setPrefs,
  login,
  logout,
  watch,
  visit,
  horizontalOverflow,
  base32Decode,
  hotp,
  totp,
  createUserViaAdmin,
};
