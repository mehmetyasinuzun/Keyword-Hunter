# 🕵️ KeywordHunter

<p align="center">
  <img src="https://img.shields.io/badge/Go-1.24-00ADD8?style=for-the-badge&logo=go&logoColor=white" />
  <img src="https://img.shields.io/badge/Dark%20Web-OSINT-purple?style=for-the-badge" />
  <img src="https://img.shields.io/badge/License-MIT-green?style=for-the-badge" />
  <img src="https://img.shields.io/badge/Status-Active-success?style=for-the-badge" />
</p>

<p align="center">
  <b>🔍 Dark Web Cyber Threat Intelligence (CTI) Tool</b><br>
  <i>17 Dark Web arama motorunu tek platformda birleştiren, graf görselleştirme ve derinleştirme özellikli OSINT aracı</i>
</p>

---

## 📋 İçindekiler

- [Özellikler](#-özellikler)
- [Ekran Görüntüleri](#-ekran-görüntüleri)
- [Kurulum](#-kurulum)
- [Kullanım](#-kullanım)
- [Mimari](#-mimari)
- [Teknolojiler](#-teknolojiler)
- [API Endpoints](#-api-endpoints)
- [Katkıda Bulunma](#-katkıda-bulunma)
- [Lisans](#-lisans)

---

## ✨ Özellikler

### 🔍 Çoklu Arama Motoru Desteği
- **17 Dark Web arama motoru** entegrasyonu
- Ahmia, Torch, DarkHunt, Torgle, Amnesia, Kaizer, Anima ve daha fazlası
- Paralel arama ile hızlı sonuç toplama
- Otomatik duplicate filtreleme

### 🗺️ Graf Görselleştirme
- **D3.js** ile interaktif graf haritası
- 3 farklı layout: Radial, Tree, Force
- Node'lara tıklayarak detay görüntüleme
- Zoom, pan ve odaklama kontrolleri

### 🔗 Derinleştirme (Link Expansion)
- Seçilen .onion sitesini tarayarak iç/dış linkleri çıkarma
- Recursive link keşfi
- Graf üzerinde görsel link ağacı oluşturma

### 📊 Artifact Extraction
- **Email** adresleri
- **Bitcoin** cüzdan adresleri
- **Monero** adresleri
- **IP** adresleri
- **.onion** adresleri

### 🛡️ Güvenlik
- Session-based authentication
- Güvenli cookie yönetimi
- Rate limiting ve retry mekanizması

### 📱 Modern UI
- Dark tema tasarım
- Responsive layout
- Real-time istatistikler
- Toast bildirimleri

---

## 📸 Ekran Görüntüleri

### Dashboard
![Dashboard](docs/screenshots/dashboard.png)

### Arama Sonuçları
![Search](docs/screenshots/search.png)

### Graf Haritası
![Graph](docs/screenshots/graph.png)

### İçerik Detayı
![Content](docs/screenshots/content.png)

---

## 🚀 Kurulum

### Gereksinimler

- **Go 1.21+**
- **Tor Browser** veya **Tor Service** (SOCKS5 proxy: 127.0.0.1:9050)

### Hızlı Başlangıç

```bash
# Repo'yu klonla
git clone https://github.com/mehmetyasinuzun/Keyword-Hunter.git
cd Keyword-Hunter

# Bağımlılıkları indir
go mod download

# Build al
go build -o keywordhunter.exe ./cmd

# Çalıştır
./keywordhunter.exe
```

### Docker ile Kurulum

```bash
# Image build
docker build -t keywordhunter .

# Container çalıştır
docker run -p 8080:8080 keywordhunter
```

---

## 💻 Kullanım

### 1. Tor'u Başlat

KeywordHunter, dark web sitelerine erişmek için Tor proxy kullanır:

```bash
# Windows: Tor Browser'ı aç
# Linux/Mac:
tor --SocksPort 9050
```

### 2. Uygulamayı Başlat

```bash
./keywordhunter.exe
```

Varsayılan olarak `http://localhost:8080` adresinde çalışır.

### 3. Giriş Yap

Varsayılan kimlik bilgileri:
- **Kullanıcı adı:** `admin`
- **Şifre:** `hunter123`

### 4. Arama Yap

1. **Arama** sayfasına git
2. Anahtar kelime gir (örn: "ransomware", "data leak")
3. Sonuçları incele ve kaydet

### 5. Graf Haritasını Kullan

1. **Harita** sayfasına git
2. Node'lara tıklayarak açıp kapatın
3. Sağ tık ile **Derinleştir** seçeneğini kullanın
4. **Full Map** ile tüm keşifleri görüntüleyin

---

## 🏗️ Mimari

```
keywordhunter-mvp/
├── cmd/
│   └── main.go              # Uygulama giriş noktası
├── pkg/
│   ├── artifact/            # Artifact extraction (email, btc, ip)
│   ├── logger/              # Renkli loglama sistemi
│   ├── scraper/             # Tor üzerinden site scraping
│   ├── search/              # 17 arama motoru entegrasyonu
│   ├── shared/              # Ortak utility fonksiyonlar
│   ├── storage/             # SQLite veritabanı işlemleri
│   └── web/
│       ├── server.go        # Gin HTTP server
│       ├── static/          # CSS, JS dosyaları
│       └── templates/       # HTML şablonları
├── tests/                   # Integration testleri
├── go.mod
├── go.sum
└── README.md
```

---

## 🛠️ Teknolojiler

| Kategori | Teknoloji |
|----------|-----------|
| **Backend** | Go 1.24, Gin Framework |
| **Database** | SQLite (modernc.org/sqlite - Pure Go) |
| **Frontend** | HTML, TailwindCSS, D3.js |
| **Proxy** | Tor SOCKS5 |
| **Build** | Go Modules |

---

## 🔌 API Endpoints

| Method | Endpoint | Açıklama |
|--------|----------|----------|
| `GET` | `/dashboard` | Ana dashboard |
| `GET` | `/search` | Arama sayfası |
| `POST` | `/search` | Arama yap |
| `GET` | `/results` | Kayıtlı sonuçlar |
| `GET` | `/results/graph` | Graf görselleştirme |
| `GET` | `/contents` | Scrape edilmiş içerikler |
| `GET` | `/scrape` | Scrape sayfası |
| `POST` | `/scrape` | URL'leri scrape et |
| `GET` | `/api/stats` | İstatistikler |
| `GET` | `/api/graph` | Graf verisi (JSON) |
| `GET` | `/api/queries` | Mevcut sorgular |
| `POST` | `/api/expand` | Link derinleştirme |

---

## 🔍 Desteklenen Arama Motorları

| # | Motor | Durum |
|---|-------|-------|
| 1 | Ahmia | ✅ |
| 2 | OnionLand | ✅ |
| 3 | DarkHunt | ✅ |
| 4 | Torgle | ✅ |
| 5 | Amnesia | ✅ |
| 6 | Kaizer | ✅ |
| 7 | Anima | ✅ |
| 8 | Tornado | ✅ |
| 9 | TorNet | ✅ |
| 10 | Torland | ✅ |
| 11 | FindTor | ✅ |
| 12 | Excavator | ✅ |
| 13 | Onionway | ✅ |
| 14 | Tor66 | ✅ |
| 15 | OSS | ✅ |
| 16 | Torgol | ✅ |
| 17 | DeepSearches | ✅ |

---

## 🧪 Testler

```bash
# Tüm testleri çalıştır
go test ./... -v

# Coverage raporu
go test ./... -cover

# Belirli paketi test et
go test ./pkg/storage -v
```

---

## 🤝 Katkıda Bulunma

1. Fork'layın
2. Feature branch oluşturun (`git checkout -b feature/amazing-feature`)
3. Değişikliklerinizi commit edin (`git commit -m 'Add amazing feature'`)
4. Branch'e push edin (`git push origin feature/amazing-feature`)
5. Pull Request açın

---

## ⚠️ Yasal Uyarı

Bu araç **yalnızca yasal ve etik amaçlar** için tasarlanmıştır:
- Siber güvenlik araştırması
- Tehdit istihbaratı
- Akademik çalışmalar

**Yasadışı faaliyetler için kullanımı kesinlikle yasaktır.** Kullanıcı, aracın kullanımından doğabilecek tüm yasal sorumluluğu kabul eder.

---

## 📄 Lisans

Bu proje [MIT Lisansı](LICENSE) altında lisanslanmıştır.

---

## 👤 Geliştirici

**Mehmet Yasin Uzun**

- GitHub: [@mehmetyasinuzun](https://github.com/mehmetyasinuzun)

---

<p align="center">
  <b>⭐ Projeyi beğendiyseniz yıldız vermeyi unutmayın!</b>
</p>
