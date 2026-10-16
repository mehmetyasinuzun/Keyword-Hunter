# Ön Yüz Mimarisi (Frontend Architecture)

KeywordHunter'ın arayüzü **derleme adımı olmayan**, tamamen yerel (CDN'siz) ve
**sıkı CSP** (`script-src 'self'`) altında çalışan sunucu-taraflı bir uygulamadır.
Bu belge yeni bir ekran eklerken ya da mevcut bir ekranı değiştirirken uyulması
gereken sözleşmeyi tanımlar.

## İlkeler

1. **Satır içi betik yok.** Şablonlarda `<script>…</script>` gövdesi, `onclick=`
   vb. olay öznitelikleri ve `javascript:` URL'leri yasaktır.
   `pkg/web/templates_csp_test.go` bunu her şablon ve her JS dosyası için zorlar.
2. **Sunucu ilk baytta doğruyu basar.** Dil (`{{t .Lang "anahtar"}}`), tema
   (`<html data-theme>`), rol (menü süzme, `data-write`) sunucuda çözülür;
   sayfa açılışında yanıp sönme ya da yetki sızıntısı olmaz.
3. **Dark web'den gelen her metin güvenilmezdir.** Şablonda `{{.Değer}}`
   (html/template otomatik kaçışlar), JS'de `KH.esc()`/`textContent`/`KH.html`
   kullanılır. Değerleri JS'ye ileti olarak taşımak için `data-*` öznitelikleri
   kullanılır — asla bir JS ifadesinin içine dize olarak gömülmez.
4. **Tek tasarım sistemi.** Tüm bileşenler `static/css/kh.css` içindedir
   (token → base → shell → components → motion → utilities). Sayfaya özgü
   küçük eklemeler `static/css/pages/<sayfa>.css` içinde durur. Renkler daima
   token'dır (`var(--primary)`, `var(--sev-3)`…); sabit hex kullanılmaz ki açık
   ve koyu tema birlikte çalışsın.
5. **Erişilebilirlik.** Her etkileşimli öğe gerçek bir `<button>`/`<a>`'dır;
   ikon-tek butonlarda `aria-label`, modallarda odak tuzağı (çekirdek sağlar),
   `prefers-reduced-motion` desteği (çekirdek sağlar).

## Sayfa iskeleti

```gotemplate
{{template "doc" .}}
<title>{{t .Lang "x.title"}} · KeywordHunter</title>
<link rel="stylesheet" href="/static/css/pages/x.css?v={{version}}">   {{/* isteğe bağlı */}}
{{template "body" .}}
{{template "shell" .}}
<main id="main" class="kh-page">            {{/* .narrow (1080px) | .full (kenarsız) */}}
  <header class="kh-header">
    <div>
      <div class="kh-eyebrow">…</div>      {{/* isteğe bağlı */}}
      <h1 class="kh-title">…</h1>
      <p class="kh-subtitle">…</p>
    </div>
    <div class="kh-actions">…</div>
  </header>
  …
</main>
{{template "shell_end" .}}
<script type="application/json" id="page-data">{{.pageData}}</script>  {{/* isteğe bağlı */}}
<script src="/static/js/pages/x.js?v={{version}}" defer></script>
</body>
</html>
```

Giriş ekranları (`login.html`, `login_2fa.html`) kabuğu kullanmaz; ortak bölünmüş
ekran parçaları `partials/auth.html` içindedir (`{{template "auth_hero" .}}`,
`{{template "auth_tools" .}}`) ve stilleri `static/css/pages/login.css`'tedir.

Şablon yardımcıları: `t`, `pct` (dil duyarlı yüzde), `tags`, `truncate`, `formatTime`,
`isoTime`, `initials`, `icon`. Hata sayfaları için `s.renderError(c, 404|403)`.

Handler'lar `c.HTML` yerine **`s.render(c, status, "x.html", gin.H{...})`**
çağırır; bu `Lang`, `Theme`, `User`, `Role`, `IsAdmin`, `CanWrite`, `CSRF`,
`Nav`, `Crumb`, `I18n` alanlarını ekler. Menü yapısı `pkg/web/render.go`
içindeki `navLayout`'tadır.

## Davranış bağlama (event delegation)

```html
<button class="kh-btn sm danger" data-action="delete-item" data-id="{{.ID}}" data-name="{{.Name}}">Sil</button>
<select data-change="set-role" data-id="{{.ID}}">…</select>
<input data-input="filter-list"> <input data-enter="quick-search">
<form data-submit="create-user">…</form>
```

```js
(function () {
  'use strict';
  KH.act({
    'delete-item': async (el) => {
      if (!await KH.confirm({ title: KH.tr('Sil', 'Delete'), message: el.dataset.name, danger: true })) return;
      await KH.api('/api/items/' + encodeURIComponent(el.dataset.id) + '/delete', { method: 'POST', body: {} });
    },
  });
  document.addEventListener('DOMContentLoaded', () => { /* ilk yükleme */ });
})();
```

Yerleşik eylemler: `modal-open` (`data-target`), `modal-close`, `copy`
(`data-copy`, `data-copy-label`), `toggle-switch`, `theme-cycle`, `theme-set`,
`lang-toggle`, `palette`, `shortcuts`, `reload`, `menu-toggle` (`data-target`),
`pw-eye` (`data-target` = parola input id; göster/gizle).

## Çekirdek API (`static/js/app.js`)

| Yardımcı | Açıklama |
|---|---|
| `KH.api(url, {method, body})` | JSON + CSRF; 401 → giriş, hata → `Error(message)` |
| `KH.toast(msg, 'ok'\|'err'\|'warn'\|'info')` | Bildirim |
| `KH.confirm({title, message, danger})` | `Promise<boolean>` |
| `KH.prompt({title, label, value, type, multiline})` | `window.prompt` yerine; `Promise<string\|null>` |
| `KH.openModal(id, {returnFocus})` / `KH.closeModal(id, {returnFocus})` | Odak tuzaklı modal; `returnFocus` kapanınca odağın döneceği öğe ya da onu kapanışta döndüren işlev (varsayılan: açılıştaki odak). Akış sonunda açılış düğmesi gizleniyorsa görünür bir hedef verin (ör. `closeModal(id, {returnFocus: $('x')})`); odak asla `<body>`'ye düşmemeli. Overlay'de `data-static` varsa arka plan tıklaması ve Esc kapatmaz (yalnız açık bir `modal-close` düğmesi; ör. bir kez gösterilen gizli değerler) |
| `KH.esc`, `KH.html\`…\``, `KH.raw`, `KH.render(el, html)` | Güvenli HTML |
| `KH.icon(name, cls)` | Sprite ikonu |
| `KH.sev(n)`, `KH.tags(str)`, `KH.fmtTime`, `KH.ago`, `KH.num`, `KH.short`, `KH.pctText(n)` | Biçimleme (dil duyarlı) |
| `KH.busy(btn, on, text)` | Buton meşgul durumu |
| `KH.data(id)` | `<script type="application/json">` sayfa verisi |
| `KH.t(key)`, `KH.tr(tr, en)`, `KH.lang` | i18n |
| `KH.palette()` + `kh:theme` olayı | Grafikler için tema renkleri |
| `KH.countUp(el)`, `KH.enhance(scope)` | `data-countup`, `data-ring`, `data-bar`, `.kh-reveal` |
| `KH.pwStrength(v)`, `KH.pwMeter(meter, label, v)` | Parola gücü (0–4) + `.pw-meter` / `.kh-pw` alanını boyar |

## Bileşenler (kh.css)

`kh-card` (`.accent`, `.flush`, `.tight`) · `kh-card-title`/`kh-card-head` ·
`kh-stat` (`.blue .purple .green .red .amber .cyan .orange`) ·
`kh-btn` (`.primary .ghost .subtle .danger .success .warn` × `.lg .sm .xs .icon .block`) ·
`kh-segment` · `kh-badge` (`.sev-1…5 .neutral .ok .err .warn .info .violet .mono`) ·
`kh-chip` · `kh-tag` · `kh-dot` (`.up .down .warn .unknown .pulse`) ·
`kh-field/kh-label/kh-input/kh-select/kh-textarea/kh-hint/kh-check/kh-switch` ·
`kh-form-grid` · `kh-table` (+`.num .center .right .muted .title .meta .link`) ·
`kh-rowactions` · `kh-empty` · `kh-skel` · `kh-progress` (`.thin .active`) ·
`kh-meter` · `kh-menu` (`.at-cursor`) · `kh-alert` (`.ok .warn .err .info`) · `kh-modal-overlay > kh-modal`
(`.sm .lg`) · `kh-ring` · `kh-feed` · `kh-log` · `kh-engine-chip` · `kh-kbd` ·
`[data-tip]` ipucu · yardımcılar (`mt-*`, `mb-*`, `gap-*`, `flex`, `items-center`,
`justify-between`, `text-xs`, `c-ok`, `c-sev-3`, …).

## Tema

`<html data-theme="auto|light|dark">`. `auto` sistem tercihini izler. Tercih
`theme` çerezinde saklanır ve sunucu tarafından basılır. Grafik çizen sayfalar
renkleri `KH.palette()`'ten alır ve `window.addEventListener('kh:theme', …)`
ile yeniden çizer.
