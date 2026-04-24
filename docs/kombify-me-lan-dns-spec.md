# kombify.me — Service-Architektur und LAN DNS Specification

> Anforderungsdokument fuer das kombify-Me Team.
> Beschreibt die Gesamtarchitektur aller kombify.me Services, Hosting-Strategie und im Detail den LAN DNS Service.

---

## 0. kombify.me Gesamtuebersicht

### Drei Use Cases unter einer Domain

| # | Service | Subdomain | Protokoll | Beschreibung |
|---|---------|-----------|-----------|--------------|
| 1 | **Wildcard Tunnel** | `*.kombify.me` | HTTPS | Oeffentlicher Zugang Internet → Homelab (Agent/Gateway-Tunnel) |
| 2 | **LAN DNS** | `*.lan.kombify.me` | DNS (UDP/TCP 53) | LAN-Zugang ohne Config: IP-basierte Wildcard-Aufloesung |
| 3 | **Worker Registry** | _(kein eigener Endpunkt)_ | HTTPS via kombify Gateway | StackKit-Instanzen registrieren sich, Status-Tracking |

### Architektur-Regel: Zentrales API-Gateway

```
ALLE API-Aufrufe -> api.kombify.io (Cloudflare Edge) -> kombify Gateway -> kombify-DB
```

**Es gibt kein `api.kombify.me`.** kombify.me ist ein Service-Endpunkt (Tunnel-Gateway, DNS), aber KEINE eigene API-Surface. Jede Datenoperation (Subdomain-Registrierung, Worker-Registration, Address-Verwaltung) laeuft ueber das zentrale kombify Gateway.

### Daten-Regel: kombify-DB ist Single Source of Truth

Alle registrierten Adressen — Tunnel-Subdomains, Worker-Eintraege, Service-Mappings — werden in der kombify-DB gespeichert. kombify.me Services lesen ihre Konfiguration aus der kombify-DB (via kombify Gateway), halten aber KEINEN eigenen State.

### Hosting-Architektur

```
kombify.me (Cloudflare DNS — Domain liegt hier)
│
├── *.kombify.me (root)         → Cloudflare Tunnel → VPS
│   └── kombify-me-gateway (Docker, Port 8080, dokploy-network)
│   └── Tunnel-Gateway (Proxy: WebSocket → Agent → Homelab)
│   └── Verbindet sich direkt mit kombify-DB (Datenbank kombify_me)
│
├── *.lan.kombify.me            → NS-Delegation → VPS
│   └── ns1: srv1161760 (72.62.49.6)
│   └── ns2: kombify-ionos (217.154.174.107)
│   └── Go DNS Server (sslip.io fork), Port 53 UDP/TCP
│   └── Stateless — parst IP aus Hostname, keine DB noetig
│
└── Worker Registry              → KEIN eigener Endpunkt
└── API: api.kombify.io/v1/workers/* (Cloudflare Edge -> kombify Gateway)
    └── DB: kombify-DB (PostgreSQL)
```

**Warum VPS fuer alle Services:**
- Alle kombify-Services laufen auf kombify-ionos VPS (Azure komplett deaktiviert seit 2026-03-10)
- Cloudflare Tunnel (`cloudflared`) leitet `*.kombify.me` Traffic zum Gateway-Container
- Gateway verbindet sich direkt mit kombify-DB auf dem gleichen VPS
- LAN DNS braucht Port 53 UDP/TCP — nur auf dedizierten Servern moeglich

### Kosten

| Komponente | Kosten |
|------------|--------|
| Cloudflare Worker (Tunnel Gateway) | 0 EUR (Free Tier) |
| Cloudflare DNS | 0 EUR (inklusive) |
| VPS srv1161760 (DNS ns1) | Bereits bezahlt |
| VPS kombify-ionos (DNS ns2) | Bereits bezahlt |
| kombify Gateway / kombify-DB | Bestehende Infrastruktur |
| **Gesamt zusaetzlich** | **0 EUR** |

---

## 1. Problemstellung (LAN DNS)

### Aktueller Zustand

StackKits deployen ein Dashboard, das alle Services mit direkten Links anzeigt. Services laufen hinter Traefik mit domain-basiertem Routing (z.B. `whoami.stack.local`). Das funktioniert auf dem Server selbst, aber **nicht von anderen Geraeten im LAN**, weil `*.stack.local` nur auf `127.0.0.1` aufloest.

### Aktuelle Loesung (sslip.io)

Das Dashboard konstruiert URLs wie `whoami.192.168.1.50.sslip.io:7880`. Der oeffentliche DNS-Service [sslip.io](https://sslip.io) loest `anything.192.168.1.50.sslip.io` automatisch auf `192.168.1.50` auf. Traefik akzeptiert diese Hostnamen ueber `HostRegexp`-Rules.

### Warum sslip.io ersetzen?

| Problem | Auswirkung |
|---------|------------|
| Abhaengigkeit von Drittanbieter | Ausfall von sslip.io = Dashboard-Links kaputt |
| Kein Branding | User sehen `sslip.io` statt `kombify.me` |
| Keine Kontrolle | Keine Moeglichkeit fuer Custom-Records, Monitoring, Rate-Limiting |
| Kein Offline-Fallback | Ohne Internet (DNS-Aufloesung via sslip.io) keine LAN-Links |

### Zielzustand

`whoami.192-168-1-50.lan.kombify.me` loest auf `192.168.1.50` auf — gehostet auf eigener Infrastruktur, gebrandeted als kombify.

---

## 2. Feature-Beschreibung

### Was gebaut wird

Ein Wildcard-DNS-Service auf der Subdomain `lan.kombify.me`, der IP-Adressen aus Hostnamen parst und als DNS-A-Record zurueckgibt. Identisches Verhalten wie sslip.io, aber self-hosted auf kombify-Infrastruktur.

### Beispiele

```
whoami.192-168-1-50.lan.kombify.me  →  A 192.168.1.50
dash.10-0-0-1.lan.kombify.me        →  A 10.0.0.1
grafana.172-16-0-5.lan.kombify.me    →  A 172.16.0.5
```

### IP-Formate (beide muessen unterstuetzt werden)

| Format | Beispiel | Aufgeloeste IP |
|--------|----------|----------------|
| Punkt-getrennt | `app.192.168.1.50.lan.kombify.me` | `192.168.1.50` |
| Bindestrich-getrennt | `app.192-168-1-50.lan.kombify.me` | `192.168.1.50` |

### Koexistenz aller kombify.me Services

| Subdomain | Service | Hosting | Zweck |
|-----------|---------|---------|-------|
| `*.kombify.me` (root) | Tunnel-Gateway | Cloudflare Worker | Oeffentlicher Zugang (Internet → Homelab) |
| `*.lan.kombify.me` | LAN DNS | VPS (Port 53) | Lokaler Zugang (LAN → LAN, IP-basiert) |
| `api.kombify.io/v1/subdomains/*` | Subdomain-API | kombify Gateway (Cloudflare Edge) | Tunnel-Registrierung -> kombify-DB |
| `api.kombify.io/v1/workers/*` | Worker-Registry | kombify Gateway (Cloudflare Edge) | StackKit Worker-Verwaltung -> kombify-DB |

Keine Ueberschneidung — der LAN-DNS-Service beantwortet nur Queries fuer `*.lan.kombify.me`. Der Tunnel-Gateway (Cloudflare Worker) handhabt nur `*.kombify.me` root. Alle API-Operationen laufen zentral ueber `api.kombify.io`.

---

## 3. Technische Architektur

### DNS-Server Software

**Empfehlung: [sslip.io Go-Server](https://github.com/cunnie/sslip.io)**

- Sprache: Go (passt zum kombify-Stack)
- Lizenz: Open Source
- Abhaengigkeiten: Keine (standalone Binary)
- Funktionalitaet: Parst IPs aus Hostnamen, antwortet mit A/AAAA Records
- Konfigurierbar via CLI-Flags (`-nameservers`, `-addresses`)

Alternative: PowerDNS + Python-Backend (wie nip.io) — komplexer, mehr Features, hier nicht noetig.

### Server-Topologie

Mindestens 2 Nameserver fuer DNS-Redundanz (RFC-Anforderung):

| Nameserver | Server | IP | Standort |
|------------|--------|-----|----------|
| `ns1.lan.kombify.me` | srv1161760 (Netcup) | `72.62.49.6` | DE |
| `ns2.lan.kombify.me` | kombify-ionos (IONOS) | `217.154.174.107` | DE |

### Deployment pro Server

```yaml
# Docker Compose Service (hinzufuegen zum Server-Stack)
kombify-lan-dns:
  image: cunnie/sslip.io-dns-server
  container_name: kombify-lan-dns
  restart: unless-stopped
  ports:
    - "53:53/udp"
    - "53:53/tcp"
  command: >
    -nameservers ns1.lan.kombify.me,ns2.lan.kombify.me
    -addresses ns1.lan.kombify.me=72.62.49.6,ns2.lan.kombify.me=217.154.174.107
  security_opt:
    - no-new-privileges:true
```

Alternativ als systemd-Service mit dem Go-Binary direkt.

### DNS-Records bei Cloudflare (Domain liegt hier)

Folgende Records muessen in der Cloudflare DNS Zone fuer `kombify.me` angelegt werden:

```
# A-Records fuer die Nameserver (Cloudflare DNS Zone)
ns1.lan.kombify.me    A    72.62.49.6          (Proxy: OFF / DNS-only)
ns2.lan.kombify.me    A    217.154.174.107      (Proxy: OFF / DNS-only)

# NS-Delegation fuer die lan-Subdomain (Cloudflare DNS Zone)
lan.kombify.me        NS   ns1.lan.kombify.me
lan.kombify.me        NS   ns2.lan.kombify.me
```

**Wichtig**: Die A-Records fuer `ns1.lan`/`ns2.lan` MUESSEN auf "DNS-only" (grauer Cloud-Icon) stehen — kein Cloudflare Proxy. Der NS-Delegation-Traffic darf nicht durch Cloudflare Proxy laufen.

**Hinweis**: Da die Domain bei Cloudflare liegt (nicht nur als Registrar, sondern als DNS-Provider), werden keine Glue Records beim Registrar benoetigt. Cloudflare handhabt die NS-Delegation intern.

### Records die der DNS-Server selbst beantwortet

| Query | Antwort |
|-------|---------|
| `lan.kombify.me NS` | `ns1.lan.kombify.me`, `ns2.lan.kombify.me` |
| `ns1.lan.kombify.me A` | `72.62.49.6` |
| `ns2.lan.kombify.me A` | `217.154.174.107` |
| `lan.kombify.me SOA` | `ns1.lan.kombify.me hostmaster.kombify.me (...)` |
| `whoami.192-168-1-50.lan.kombify.me A` | `192.168.1.50` (berechnet) |
| `anything.10.0.0.1.lan.kombify.me A` | `10.0.0.1` (berechnet) |

---

## 4. Integration in StackKits

### Dashboard (dashboard.sh)

Aenderung in der `mkUrl()`-Funktion:

```javascript
// Vorher (sslip.io)
return pr + "//" + sub + "." + h + ".sslip.io" + (p ? ":" + p : "");

// Nachher (lan.kombify.me)
return pr + "//" + sub + "." + h.replace(/\./g, "-") + ".lan.kombify.me" + (p ? ":" + p : "");
```

**Hinweis**: sslip.io unterstuetzt Punkt-getrennte IPs (`192.168.1.50.sslip.io`), aber fuer eine saubere Subdomain-Struktur unter `lan.kombify.me` sollten Bindestriche verwendet werden (`192-168-1-50.lan.kombify.me`). Der DNS-Server unterstuetzt beides.

### Traefik HostRegexp Rules

Aenderung in allen Demo-Compose-Files:

```yaml
# Vorher
rule: "HostRegexp(`^whoami[.].+[.]sslip[.]io$`)"

# Nachher
rule: "HostRegexp(`^whoami[.].+[.]lan[.]kombify[.]me$`)"
```

### Betroffene Dateien

| Datei | Aenderung |
|-------|-----------|
| `demos/dashboard.sh` | `mkUrl()`: `sslip.io` → `lan.kombify.me`, IP-Format mit Bindestrichen |
| `demos/base-kit/docker-compose.yml` | Alle `sslip.io` HostRegexp Rules → `lan.kombify.me` |
| `demos/ha-kit/docker-compose.yml` | Alle `sslip.io` HostRegexp Rules → `lan.kombify.me` |
| `demos/modern-homelab/docker-compose.yml` | Alle `sslip.io` HostRegexp Rules → `lan.kombify.me` |

### User-sichtbares Ergebnis

**Auf dem Server selbst (localhost):**
```
Dashboard:  http://dash.stack.local:7880
Services:   http://whoami.stack.local:7880
```

**Von jedem Geraet im LAN:**
```
Dashboard:  http://dash.192-168-1-50.lan.kombify.me:7880
Services:   http://whoami.192-168-1-50.lan.kombify.me:7880
```

Der User muss nichts konfigurieren — keine /etc/hosts, kein lokaler DNS, kein VPN. Einfach die Server-IP im Browser eingeben, das Dashboard zeigt automatisch die richtigen Links.

---

## 5. Anforderungen

### Funktionale Anforderungen

| ID | Anforderung | Prioritaet |
|----|-------------|------------|
| F1 | `*.lan.kombify.me` loest eingebettete IPv4-Adressen auf (Bindestrich + Punkt-Format) | MUSS |
| F2 | DNS-Antwortzeit < 50ms (P95) | MUSS |
| F3 | Mindestens 2 Nameserver fuer Redundanz | MUSS |
| F4 | SOA, NS, A Records korrekt implementiert | MUSS |
| F5 | IPv6-Support (AAAA Records fuer eingebettete IPv6) | SOLL |
| F6 | Wildcard-TXT-Record fuer ACME DNS-01 Challenge | KANN |
| F7 | Monitoring/Alerting bei DNS-Server-Ausfall | SOLL |
| F8 | Tunnel-Gateway: Subdomain-Lookup via kombify Gateway (nicht lokaler State) | MUSS |
| F9 | Worker-Registry: CRUD ueber kombify Gateway (`api.kombify.io/v1/workers/*`) | MUSS |
| F10 | Alle registrierten Adressen (Subdomains, Worker) in kombify-DB gespeichert | MUSS |

### Nicht-funktionale Anforderungen

| ID | Anforderung | Prioritaet |
|----|-------------|------------|
| N1 | LAN DNS: Kein Zustand (stateless) — IP wird aus Hostname berechnet, keine DB noetig | MUSS |
| N2 | Minimale Ressourcen (< 50MB RAM, < 0.1 CPU idle) | MUSS |
| N3 | Auto-Restart bei Crash (Docker restart policy oder systemd) | MUSS |
| N4 | Keine Abhaengigkeit zu externen Services (standalone) | MUSS |
| N5 | Logs fuer DNS-Queries (optional, fuer Debugging) | KANN |

### Sicherheitsanforderungen

| ID | Anforderung | Prioritaet |
|----|-------------|------------|
| S1 | Kein offener Resolver — nur `*.lan.kombify.me` Queries beantworten | MUSS |
| S2 | Rate-Limiting auf DNS-Ebene (gegen DNS-Amplification-Angriffe) | SOLL |
| S3 | DNS-Server laeuft als non-root (ggf. mit Port-Redirect 53 → 1053) | SOLL |
| S4 | No-new-privileges Security-Option | MUSS |

---

## 6. Abhaengigkeiten und Voraussetzungen

### Voraussetzungen (muessen VOR dem Deployment erfuellt sein)

| # | Voraussetzung | Verantwortlich | Status |
|---|---------------|----------------|--------|
| 1 | Port 53 UDP/TCP offen auf srv1161760 | Infra | Erledigt |
| 2 | Port 53 UDP/TCP offen auf kombify-ionos | Infra | Erledigt |
| 3 | Kein anderer DNS-Service auf Port 53 (systemd-resolved etc.) | Infra | Erledigt |
| 4 | NS-Delegation + A-Records in Cloudflare DNS Zone angelegt | DNS Admin | Erledigt |
| 5 | kombify Gateway Endpunkte fuer Worker-Registry in Cloudflare Edge konfiguriert | API Team | Offen |
| 6 | Bestehende Tunnel-Registrierungen in kombify-DB migriert | Backend | Erledigt (Gateway laeuft auf VPS) |

### Abhaengigkeiten zu anderen Systemen

| System | Art der Abhaengigkeit |
|--------|----------------------|
| Cloudflare DNS | Domain `kombify.me` liegt hier, NS-Delegation fuer `lan.kombify.me` |
| Cloudflare Tunnel | Leitet `*.kombify.me` Traffic zum Gateway-Container auf VPS |
| kombify Gateway (Cloudflare Edge) | Zentrale API fuer alle Datenoperationen — Subdomain-Registry, Worker-Registry |
| kombify-DB | Single Source of Truth fuer alle registrierten Adressen |
| srv1161760 | LAN DNS Server ns1 (Docker oder systemd) |
| kombify-ionos | LAN DNS Server ns2 (Docker oder systemd) |
| StackKits Dashboard | Consumer — nutzt `lan.kombify.me` fuer URL-Generierung |

### Keine Abhaengigkeit zu

- Azure (komplett deaktiviert seit 2026-03-10, Gateway laeuft auf VPS)
- Appwrite (nicht verwendet — DB ist kombify-DB, nicht Appwrite)
- Eigene API-Surface (kein `api.kombify.me` — alles ueber `api.kombify.io`)

---

## 7. Rollout-Plan

### Phase 1: LAN DNS Server Deployment (Tag 1)

1. sslip.io Go-Binary auf srv1161760 deployen (Docker)
2. sslip.io Go-Binary auf kombify-ionos deployen (Docker)
3. Port 53 in Firewall oeffnen (beide Server)
4. Testen: `dig @72.62.49.6 test.192-168-1-1.lan.kombify.me` → erwartet `192.168.1.1`
5. Testen: `dig @217.154.174.107 test.10-0-0-1.lan.kombify.me` → erwartet `10.0.0.1`

### Phase 2: DNS-Delegation bei Cloudflare (Tag 1-2)

1. In Cloudflare DNS Zone fuer `kombify.me` anlegen:
   - `ns1.lan.kombify.me A 72.62.49.6` (Proxy: OFF)
   - `ns2.lan.kombify.me A 217.154.174.107` (Proxy: OFF)
   - `lan.kombify.me NS ns1.lan.kombify.me`
   - `lan.kombify.me NS ns2.lan.kombify.me`
2. Warten auf DNS-Propagation (Cloudflare: typisch < 5 Minuten)
3. Testen: `dig test.192-168-1-1.lan.kombify.me` → erwartet `192.168.1.1` (oeffentlich aufloesbar)

### Phase 3: StackKits Dashboard umstellen (nach Phase 2)

1. `demos/dashboard.sh`: `sslip.io` → `lan.kombify.me`
2. Alle Demo-Compose-Files: `HostRegexp` Rules anpassen
3. Testen: Base-Kit Demo starten, von anderem Geraet im LAN zugreifen
4. Dokumentation aktualisieren

### Phase 4: Tunnel-Gateway auf VPS (erledigt)

~~1. Cloudflare Worker erstellen fuer `*.kombify.me` Tunnel-Gateway~~
Gateway laeuft als Docker-Container (`kombify-me-gateway`) auf kombify-ionos und srv1161760. Traffic wird ueber Cloudflare Tunnel (`cloudflared`) zum Gateway geroutet. Azure Container App `ca-kombify-me-prod` ist abgeschaltet (Azure komplett deaktiviert seit 2026-03-10).

Alle registrierten Subdomains liegen in kombify-DB (Datenbank `kombify_me`).

### Phase 5: Monitoring (nach Phase 3+4)

1. Uptime-Check fuer beide DNS-Server (z.B. via Uptime Kuma auf srv1161760)
2. Uptime-Check fuer Cloudflare Worker (Tunnel-Gateway)
3. Alerting bei Nicht-Erreichbarkeit
4. DNS-Query-Logs aktivieren (optional, fuer Debugging)

---

## 8. Verifizierung

### Akzeptanzkriterien

| # | Test | Erwartetes Ergebnis |
|---|------|---------------------|
| T1 | `dig whoami.192-168-1-50.lan.kombify.me @72.62.49.6` | A Record: `192.168.1.50` |
| T2 | `dig whoami.192-168-1-50.lan.kombify.me @217.154.174.107` | A Record: `192.168.1.50` |
| T3 | `dig whoami.192-168-1-50.lan.kombify.me` (oeffentlicher Resolver) | A Record: `192.168.1.50` |
| T4 | `dig whoami.10.0.0.1.lan.kombify.me` (Punkt-Format) | A Record: `10.0.0.1` |
| T5 | `dig lan.kombify.me NS` | `ns1.lan.kombify.me`, `ns2.lan.kombify.me` |
| T6 | `dig ns1.lan.kombify.me A` | `72.62.49.6` |
| T7 | Base-Kit Dashboard von LAN-Geraet aufrufen via `http://dash.192-168-1-50.lan.kombify.me:7880` | Dashboard laedt, alle Service-Links klickbar |
| T8 | Service-Link klicken (z.B. Whoami) | Service antwortet korrekt |
| T9 | Neustart eines DNS-Servers | Zweiter Server uebernimmt, keine Ausfallzeit |

### Regressionstest

- kombify.me Tunnel-Service (`*.kombify.me`) funktioniert weiterhin unberuehrt
- `*.stack.local` Domains funktionieren weiterhin fuer Localhost-Zugriff
- Dashboard funktioniert weiterhin bei Zugriff via Domain (nicht IP)

---

## 9. Offene Fragen

| # | Frage | Kontext |
|---|-------|---------|
| Q1 | Unterstuetzt Cloudflare NS-Delegation fuer Subdomains (`lan.kombify.me NS ...`)? | Cloudflare DNS unterstuetzt NS-Records fuer Subdomains — sollte funktionieren. Verifizieren, dass die A-Records fuer `ns1.lan`/`ns2.lan` im DNS-only Modus (kein Proxy) korrekt aufloesen. |
| Q2 | Laeuft systemd-resolved auf den Servern und blockiert Port 53? | Pruefen mit `ss -tlnp | grep :53`. Falls ja: resolved deaktivieren oder DNS auf Port 1053 + iptables-Redirect. |
| Q3 | Brauchen wir DNSSEC fuer `lan.kombify.me`? | Fuer LAN-DNS nicht kritisch, aber nice-to-have fuer Trust. sslip.io Go-Server unterstuetzt kein DNSSEC out-of-the-box. |
| Q4 | ~~Cloudflare Worker: WebSocket-Support fuer Tunnel-Gateway ausreichend?~~ | Geloest: Gateway laeuft als Docker-Container auf VPS, Cloudflare Tunnel leitet Traffic weiter. Kein Worker noetig. |
| Q5 | kombify Gateway Endpunkte fuer Worker-Registry bereits definiert? | `api.kombify.io/v1/workers/*` muss in Cloudflare Edge konfiguriert werden. Abstimmung mit dem kombify Gateway Team noetig. |
| Q6 | ~~Bestehende Tunnel-Subdomains aus Azure Container App → kombify-DB migrieren?~~ | Geloest: Gateway verbindet sich direkt mit kombify-DB (Datenbank `kombify_me`). Azure ist komplett deaktiviert. |

---

## 10. Referenzen

- [sslip.io Source Code](https://github.com/cunnie/sslip.io) — Go DNS-Server (Basis fuer unsere Implementierung)
- [sslip.io Dokumentation](https://sslip.io) — Funktionsbeschreibung und Beispiele
- [nip.io](https://nip.io) — Alternative (PowerDNS + Python)
- [kombify.me Integration Guide](./kombify-me-integration-guide.md) — Bestehender Tunnel-Service
- [RFC 1035](https://datatracker.ietf.org/doc/html/rfc1035) — DNS Spezifikation
- [Glue Records erklaert](https://kb.porkbun.com/article/112-how-to-host-your-own-nameservers-with-glue-records) — Wie Glue Records funktionieren
