# KeywordHunter Leader Feature Proposal

## Feature Name
CTI Campaign DNA Engine

## Goal
Toplanan veriyi sadece listelemek yerine ayni tehdit kampanyasina ait olabilecek bulgulari otomatik olarak baglayip analiste "neden bagli" oldugunu aciklayan bir karar katmani sunmak.

## Why This Can Be Category-Leading
- Klasik dark-web arama araclari URL ve keyword listeler.
- Campaign DNA, farkli query ve kaynaklardan gelen bulgulari tek bir kampanya grafina toplar.
- Sonuc olarak analist ham veri yerine dogrudan eyleme donuk tehdit hikayesi gorur.

## MVP Scope
1. Signal Fingerprint
- Her sonuc icin fingerprint: domain, title tokenlari, kritik tagler, zaman penceresi, kaynak cesitliligi.

2. Cluster Scoring
- Sonuclar arasi benzerlik puani (tag overlap + domain benzerligi + zaman yakinligi).
- Esiği gecen sonuclari ayni cluster icine al.

3. Explainable Links
- UI'da her baglanti icin neden metni:
  - "Ayni onion domain"
  - "Ortak yuksek risk etiketi"
  - "24 saat icinde benzer sızıntı sinyali"

4. Priority Queue
- Cluster seviyesinde oncelik puani:
  - kritik seviye yogunlugu
  - kaynak sayisi
  - veri sizintisi sinyali agirligi

## Data Model (Proposed)
- campaign_clusters
  - id, label, score, status, created_at, updated_at
- campaign_cluster_items
  - cluster_id, result_id, link_reason, similarity_score

## API (Proposed)
- GET /api/campaigns
- GET /api/campaigns/:id
- POST /api/campaigns/rebuild

## UI (Proposed)
- Yeni sayfa: /campaigns
- Sol panel: cluster listesi (oncelik sirali)
- Orta panel: cluster timeline + signal trend
- Sag panel: explainable evidence links

## Rollout Plan
1. Storage + scoring service
2. Batch job (gece yeniden hesaplama)
3. API
4. UI
5. Analyst feedback loop (manuel merge/split)
