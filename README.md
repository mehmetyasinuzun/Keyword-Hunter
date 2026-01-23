# 🕵️ KeywordHunter - Dark Web CTI Analyst Tool

![Go Version](https://img.shields.io/badge/Go-1.24-00ADD8?style=for-the-badge&logo=go&logoColor=white)
![CTI Analyst](https://img.shields.io/badge/CTI-Analyst-red?style=for-the-badge)
![Docker Ready](https://img.shields.io/badge/Docker-Ready-blue?style=for-the-badge&logo=docker)

**KeywordHunter**, Dark Web kaynaklarından (17+ arama motoru) veri toplayan, bu verileri kategorize eden ve kritiklik seviyelerine göre analistlere sunan interaktif bir siber tehdit istihbaratı (CTI) aracıdır.

---

## 🚀 Docker ile Hızlı Başlangıç

Sistemi tek komutla ayağa kaldırmak için:

```bash
docker-compose up -d
```

Bu komut:

1. **Tor Proxy** servisini başlatır.
2. **CTI Dashboard** uygulamasını builder stage ile derler ve çalıştırır.
3. Uygulama `http://localhost:8080` adresinden erişilebilir olur.

**Giriş Bilgileri:**

- **Kullanıcı:** `admin`
- **Şifre:** `admin123`

---

## ✨ CTI Özellikleri

- **Otomatik Sınıflandırma:** Toplanan veriler içeriklerine göre *Ransomware*, *Veri Sızıntısı*, *Illegal Market* gibi kategorilere otomatik atanır.
- **Kritiklik Derecelendirmesi:** Veriler 1-5 arası kritiklik seviyesiyle işaretlenir (Örn: Sızıntı verileri Seviye 5).
- **Graf Görselleştirme:** Veriler arasındaki ilişkileri D3.js tabanlı ağ haritasında izleme.
- **Analist Kontrol Paneli:** Bulguların kategorilerini ve kritikliklerini manuel olarak düzenleme imkanı.
- **Detaylı Analiz:** Ham metin içerikleri üzerinden derinlemesine inceleme.

---

## 🏗️ Mimari ve Teknolojiler

- **Backend:** Pure Go 1.24, Gin Framework
- **Database:** SQLite (modernc.org/sqlite - No CGO)
- **Frontend:** TailwindCSS, D3.js
- **Proxy:** Tor Network (Dockerized)

---

## 🔍 Başlık Üretim Mantığı

Sistem, dark web kaynaklarından gelen ham HTML içeriklerini parse ederken:

1. `<a>` tagları arasındaki metni alır.
2. HTML entity'lerini decode eder ve tagları temizler.
3. Eğer metin anlamsızsa (çok kısa veya hash ise), URL yapısından (path) anlamlı bir başlık üretir.
4. Çıkarılan başlık, CTI analizi (PredictCTI) modülünden geçirilerek kategori atanır.

---

## ⚖️ Yasal Uyarı

Bu araç siber güvenlik araştırmacıları ve öğrenciler için eğitim amaçlı geliştirilmiştir. Yasadışı faaliyetler için kullanımı kesinlikle yasaktır.

---

**Geliştirici:** Mehmet Yasin Uzun
**Proje Durumu:** MVP (CTI Refactor)
