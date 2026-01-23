# KeywordHunter - Test Adımları

## ✅ Arama Testi Başarılı!
Dark web araması ÇALIŞIYOR - `test_search.go` ile doğrulandı:
- 17 arama motorundan 261 sonuç bulundu
- Tor bağlantısı aktif ve çalışıyor

## 🔧 Yapılan Düzeltmeler

### 1. Arama Kalitesi Sorunu Düzeltildi
- ❌ Hatalı `broadOnionRegex` kaldırıldı
- ✅ Orijinal `onionRegex` geri getirildi (path'li URL'leri yakalıyor)
- ✅ `javascript:` filtreleme eklendi

### 2. Veritabanı Şeması Güncellendi
- ❌ Eski şema: `UNIQUE(url)` - aynı URL farklı kaynaklardan gelebiliyorsa hata
- ✅ Yeni şema: `UNIQUE(url, source, query)` - doğru dallanma için

### 3. Ağaçlandırma (Treeing) Düzeltildi
- ✅ `NodeID` alanı eklendi (derinleştirme için gerekli)
- ✅ `graph_nodes` ile `search_results` bağlantısı kuruldu
- ✅ Expand edilen dallar artık kalıcı

## 📋 Şimdi Yapılacaklar

### Adım 1: Programı Başlat
```bash
cd c:\Users\Yasin\Downloads\Keyword-Hunter-master\Keyword-Hunter-master
.\keywordhunter.exe
```

**ÖNEMLİ:** Tor Browser'ın AÇIK olduğundan emin olun!

### Adım 2: Web Arayüzüne Git
URL: http://localhost:8080

Giriş Bilgileri:
- Kullanıcı: `admin`
- Şifre: `admin123`

### Adım 3: Arama Yap
1. "Arama" sekmesine git
2. Bir sorgu gir (örn: "bitcoin", "drugs", "hack")
3. "Ara" butonuna tıkla
4. 30-60 saniye bekle

### Adım 4: Sonuçları Kontrol Et
1. Arama tamamlandığında kaç sonuç bulunduğunu gör
2. "Bulgular" sekmesine git - sonuçları listede gör
3. "Harita" sekmesine git - sonuçları ağaç yapısında gör

### Adım 5: Haritayı Test Et
http://localhost:8080/results/graph

Harita sayfasında:
- ✅ Sorgu seçici dropdown'da arama yapılmış sorgular görünmeli
- ✅ Sorgu seçildiğinde ağaç yapısı görünmeli
- ✅ Node'lara tıklayınca sağ tık menüsü çıkmalı
- ✅ "Derinleştir" seçeneği .onion linklerde aktif olmalı

## 🐛 Sorun Yaşarsanız

### Haritada Sonuç Görünmüyorsa
1. **Logs kontrol edin** (terminal çıktısı):
   - "SEARCH COMPLETED" mesajında kaç sonuç bulundu?
   - "SaveResults" hata mesajı var mı?

2. **Veritabanını kontrol edin**:
   ```bash
   # PowerShell'de
   sqlite3 keywordhunter.db "SELECT COUNT(*) FROM search_results"
   sqlite3 keywordhunter.db "SELECT DISTINCT query FROM search_results"
   ```

3. **Browser Console kontrol edin** (F12):
   - `/api/graph` endpoint'i ne döndürüyor?
   - JavaScript hataları var mı?

### Veritabanı Hatası Alırsanız
Eski veritabanını yedekledik. Eğer hala sorun varsa:
```bash
Remove-Item keywordhunter.db
# Programı yeniden başlatın
```

## 📊 Beklenen Sonuçlar

Normal bir "bitcoin" araması için:
- **10-17 arama motoru** taranır
- **200-400 sonuç** bulunur
- **100-300 unique URL** kaydedilir (duplicate'ler temizlenir)
- **Haritada 3 seviye** görünür:
  1. Root (KeywordHunter)
  2. Query node ("🔍 bitcoin")
  3. Engine nodes ("🌐 OSS", "🌐 FindTor", vs.)
  4. Result nodes (bulunan .onion linkleri)

## 🎯 Derinleştirme (Expand) Testi

1. Haritada bir .onion node'una sağ tıklayın
2. "🔍 Derinleştir" seçin
3. 30-60 saniye bekleyin
4. Node'un altında "🔗 İç Linkler" ve "🌐 Dış Linkler" grupları görünmeli
5. Sayfa yenilenince bu dallar KALICItır

## 🔗 Önemli Dosyalar

- Program: `keywordhunter.exe`
- Veritabanı: `keywordhunter.db` (otomatik oluşturulur)
- Loglar: `logs/` dizini altında
- Test programı: `test_search.go`

## ⚙️ Çevre Değişkenleri (İsteğe Bağlı)

```bash
$env:TOR_PROXY="127.0.0.1:9150"  # Varsayılan
$env:WEB_ADDR=":8080"             # Varsayılan  
$env:DB_PATH="keywordhunter.db"   # Varsayılan
$env:ADMIN_USER="admin"           # Varsayılan
$env:ADMIN_PASS="admin123"        # Varsayılan
```

## ✨ Yeni Özellikler

1. **Kritiklik Skorlama**: Her sonuca otomatik kritiklik (1-5) atanır
2. **Kategorizasyon**: Otomatik kategori tahmini (Ransomware, Market, Forum, vs.)
3. **Kelime Frekansı**: "🔍" butonu ile sayfadaki kelime sayısı bulunur
4. **Filtreleme**: Kategoriye ve kritikliğe göre filtreleme yapılır

Başarılar! 🚀
