# KeywordHunter - Cyber Threat Intelligence Platform

KeywordHunter, Dark Web (Tor Ağı) ve çeşitli açık kaynaklı istihbarat kanallarında anahtar kelime tabanlı tarama yapan, elde edilen verileri ilişkilendiren ve analistler için görselleştiren gelişmiş bir CTI (Cyber Threat Intelligence) aracıdır.

Bu proje, güvenlik analistlerinin tehditleri erken tespit etmesi, veri sızıntılarını izlemesi ve aktörler arasındaki ilişkileri haritalandırması için geliştirilmiştir. Yüksek performanslı Go mimarisi üzerine inşa edilmiştir.

## Kurulum ve Çalıştırma

Projeyi çalıştırmak için iki yöntem bulunmaktadır. Üretim ortamları ve hızlı testler için Docker önerilir.

### Yöntem 1: Docker ile Kurulum (Önerilen)

Sistemi bağımlılıklarla uğraşmadan tek komutla ayağa kaldırmak için Docker kullanabilirsiniz.

1. Depoyu klonlayın:
   ```bash
   git clone https://github.com/mehmetyasinuzun/Keyword-Hunter.git
   cd Keyword-Hunter
   ```

2. Konteynerleri başlatın:
   ```bash
   mkdir -p data
   # Opsiyonel: baslangic degerlerini ozellestirmek icin .env olusturun
   cp .env.example .env
   docker compose up -d --build
   ```

   Not: Eski Docker sürümlerinde komut `docker-compose up -d --build` olabilir.

3. Tarayıcıdan erişin:
   - URL: `http://localhost:8080`
   - Kullanıcı Adı / Şifre:
     - `.env` kullandıysanız: `ADMIN_USER` ve `ADMIN_PASS` değerleri
     - `.env` yoksa varsayılan: `cti_admin / admin123`

4. Faydalı komutlar:
   ```bash
   docker compose logs -f
   docker compose ps
   docker compose down
   ```

Docker kurulumu şu şekilde çalışır:
- Veritabanı ve loglar `./data` klasöründe kalıcıdır.
- `/settings` ekranından yapılan runtime ayar değişiklikleri `./data/.env` dosyasına yazılır ve kalıcıdır.

   ![Giriş Ekranı](docs/screenshots/login_view.jpg)

### Yöntem 2: Manuel Kurulum (Windows/Linux)

Geliştirme yapmak veya Docker kullanmadan çalıştırmak isterseniz:

1. Gereksinimler:
   - Go 1.24 veya üzeri
   - Tor Browser (Arka planda çalışmalı ve 9150 portunu dinlemeli)
   - GCC (SQLite derlemesi için gerekli)

2. Derleme ve Başlatma:
   Windows kullanıcıları için hazır script bulunmaktadır. Bu script eski derlemeleri temizler ve projeyi yeniden başlatır:
   ```bash
   copy .env.example .env
   # .env dosyasında gerekli düzenlemeleri yapın
   build_and_run.bat
   ```

## Modüller ve Özellikler

Uygulama, istihbarat döngüsünü yönetmek için 5 ana modülden oluşur.

### 1. Dashboard (Genel Bakış)
Sistemin komuta merkezidir. Anlık olarak yürütülen operasyonların özetini sunar. Sol taraftaki istatistik paneli veritabanındaki toplam veri hacmini gösterirken, sağ taraftaki grafikler tehditlerin kritiklik seviyelerine (Level 1-5) göre dağılımını analiz eder.

![Dashboard Görünümü](docs/screenshots/dashboard_view.jpg)

### 2. Arama Motoru (Hunter Search)
Hedef odaklı istihbarat toplama modülüdür. Analist, Regex (Düzenli İfade) desteği sayesinde karmaşık sorgular oluşturabilir.
- **Çoklu Kaynak:** Aynı anda 17'den fazla arama motorunu ve .onion dizinini tarar.
- **Filtreleme:** Sadece belirli tarih aralığındaki veya belirli formatlardaki (örn: kredi kartı bin numaraları) verileri getirebilir.

![Arama Modülü](docs/screenshots/search_view.jpg)

### 3. Bulgular (Results)
Toplanan ham verilerin işlendiği ve listelendiği alandır. Her sonuç, bulunduğu kaynağa, tespit edilme zamanına ve içeriğin özetine göre listelenir. Analistler buradan ilgisiz verileri eleyebilir veya kritik verileri "Vaka" (Case) olarak işaretleyebilir.

![Bulgular Listesi](docs/screenshots/results_view.jpg)

### 4. İlişki Analizi (Graph Intelligence)
Metin tabanlı verilerin görselleştirilmiş halidir. Özellikle organize suç gruplarını veya birbiriyle bağlantılı veri sızıntılarını tespit etmek için kullanılır.

#### Görselleştirme Modları
Analiz türüne göre 3 farklı görünüm modu sunar:

**1. Radial View (Odaklı Analiz):** Seçilen düğümü merkeze alarak ilişkileri dairesel dağıtır.
![Radial Mod](docs/screenshots/graph_radial.jpg)

**2. Tree View (Hiyerarşik Analiz):** Veriler arasındaki ata-çocuk ilişkisini ağaç yapısında gösterir.
![Tree Mod](docs/screenshots/graph_tree.jpg)

**3. Network View (Serbest Kümeleme):** İlişkisi güçlü olan veriler birbirine çekilir (Force-Directed).
![Network Mod](docs/screenshots/graph_network.jpg)

#### Aksiyon Menüsü
Analist, herhangi bir düğüme sağ tıklayarak detaylı aksiyon menüsüne erişebilir (Derinleştirme, Kopyalama, Gizleme vb.).
![Context Menu](docs/screenshots/graph_context.jpg)

### 5. Analitik Merkezi (Analytics)
Operasyonel verilerin stratejik bilgiye dönüştüğü yerdir.
- **Zaman Analizi:** Saldırıların veya sızıntıların hangi saatlerde/günlerde yoğunlaştığını gösteren zaman çizelgesi.
- **Kaynak Dağılımı:** Hangi marketlerin veya forumların daha aktif olduğunu gösteren pasta grafikler.

![Analitik Ekranı](docs/screenshots/analytics_view.jpg)

### 6. Ayarlar Merkezi (Runtime Config)
Platform ayarlarının `.env` üzerinden yönetildiği kontrol ekranıdır (`/settings`).
- **Yönetilebilir Konfigürasyon:** Admin bilgileri, rate-limit, session TTL, Tor/DB/Web adresleri.
- **Canlı Etki:** Rate-limit ayarları kaydedildiği anda uygulanır.
- **Güvenlik:** API POST işlemlerinde CSRF koruması ve IP bazlı rate-limit aktif çalışır.

## Teknik Mimari

- **Backend:** Go (Golang) - Gin Framework
- **Veritabanı:** SQLite (Gorm ORM ile)
- **Frontend:** HTML5, CSS3, Vanilla JavaScript
- **Veri Toplama:** Colly (Scraping Framework) ve Tor Proxy
- **Görselleştirme:** Chart.js ve D3.js

## Yasal Uyarı

Bu yazılım, siber güvenlik uzmanları ve araştırmacılar için geliştirlmiştir. Yetkisiz sistemlere erişim sağlamak veya yasadışı faaliyetlerde bulunmak amacıyla kullanılamaz. Kullanıcı, aracı yasal sınırlar içerisinde kullanmakla yükümlüdür.

---
**Sürüm:** v0.5
**Geliştirici:** Mehmet Yasin Uzun
