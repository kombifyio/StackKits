# BaseKit Seamless UX Status - 2026-04-23

## Ziel

Endziel ist eine "Knopfdruck"-Experience: ein Nutzer richtet ein Homelab ein, bekommt eine laufende Plattform, einen funktionierenden initialen User und kann die enthaltenen Tools direkt nutzen.

## Istzustand

Verifiziert wurde lokal auf Docker Desktop, nicht auf IONOS Production.

Der aktuelle robuste Default von `base-kit/default-spec.yaml` ist:

| Bereich | Modul | Ergebnis |
|---------|-------|----------|
| Docker API Isolation | `socket-proxy` | healthy, `/version` HTTP 200 |
| Reverse Proxy | `traefik:v3.6.13` | healthy, Docker discovery ueber socket-proxy funktioniert |
| Login Gateway | `tinyauth` | healthy, `auth.home.localhost` routes over HTTPS |
| Vault | `vaultwarden` | healthy, `/alive` HTTP 200, `vault.home.localhost` routes over HTTPS |
| Media | `jellyfin` | healthy, `/health` HTTP 200; Root-UI braucht noch App-First-Run |

Generierung und OpenTofu:

- `stackkit generate --spec base-kit/default-spec.yaml` erzeugt 9 Dateien.
- OpenTofu `init`, `validate`, `plan` erfolgreich.
- Plan: 12 Ressourcen.
- `apply` und `destroy` erfolgreich.

Wichtige Fixes in diesem Stand:

- CUE Workspace-Loading nutzt die Root-`cue.mod`.
- Per-Modul-OpenTofu-Fragmente liegen im Output-Root und werden von OpenTofu geladen.
- HCL-Rendering fuer Labels, Commands, Healthchecks, Template-Variablen und Windows Docker Host ist korrigiert.
- Optional Use Cases werden nicht mehr implizit aktiviert.
- Update 2026-04-30: PocketID ist wieder verpflichtender Default, weil es aktuell der Passkey-faehige IdP fuer Base Kit ist. Immich bleibt an seine Live-Smoke-/Bootstrap-Gates gebunden.
- Traefik ist auf `v3.6.13` gepinnt, weil `v3.3` mit Docker Desktop 29 per alter Docker API nicht mehr sauber discovered.

## Sollzustand

Der dokumentierte V6-Zielzustand bleibt:

- Platform: Socket-Proxy, Traefik, TinyAuth, PocketID, LLDAP/Identity, PaaS-Auswahl, Monitoring.
- Use Cases: Photos, Media, Vault, Smart Home, Files, AI.
- Host-Hardening: UFW, fail2ban, SSH hardening, unattended-upgrades.
- Initialer Admin-User wird automatisch erzeugt, einmalig ausgegeben und funktioniert in Gateway und Tools.
- Jede L3-App ist ueber Login-Gateway erreichbar und hat entweder SSO oder einen automatisierten ersten Benutzer.
- Kein User muss generierte `.tf`-Dateien editieren oder manuell Container konfigurieren.

## Widersprueche

1. V6-Doku sagt "6 Use Cases default"; der verifizierte Default ist aktuell nur Platform Gateway + Media + Vault.
2. Alte Base-Kit-Doku nannte `admin/admin123`; der aktuelle Code generiert Secrets und darf keine statischen Credentials versprechen.
3. Roadmap enthielt eine alte E2E-Aussage "31 Ressourcen / 9 Container"; der aktuelle robuste Stand ist bewusst kleiner mit 12 Ressourcen / 5 Containern.
4. `make test-cue-binding` ist inzwischen als Gate vorhanden; auf Windows ohne `make` muessen die enthaltenen Direktkommandos (`cue vet -c=false ./modules/...` und die Go-Binding-Tests) separat ausgefuehrt werden.
5. PocketID liefert den verpflichtenden Passkey-IdP. Der TinyAuth-OIDC-Client wird als public PKCE Client automatisch registriert; der vollstaendige Owner-/Passkey-Enrollment-Flow bleibt ein Bootstrap-Gap.
6. Jellyfin ist infrastrukturell erreichbar, aber der App-Root ist noch keine fertig eingeloggte User Experience. App-First-Run muss automatisiert werden.
7. Vaultwarden hat generierte Admin-Materialien, aber noch keinen durchgaengig vorprovisionierten Endnutzer-Flow.
8. `base-kit/stackkit.yaml` fuehrt weiterhin Plattformrollen wie PaaS und Monitoring; die aktuelle Composition aktiviert diese nicht als verifizierten Default.

## Naechste Schritte

1. Stabilen Default als Release-Gate festschreiben:
   - `socket-proxy`, `traefik`, `tinyauth`, `vaultwarden`, `jellyfin`
   - Smoke: generate -> tofu validate -> tofu plan -> tofu apply -> health/routes -> tofu destroy

2. App-User-Bootstrap zuerst fuer Vaultwarden und Jellyfin loesen:
   - klare Bootstrap-Methode pro App dokumentieren
   - idempotent machen
   - lokale Docker-Smokes um "User kann UI nutzen" erweitern

3. PocketID/OIDC hart halten:
   - public PKCE Client idempotent registrieren
   - TinyAuth-OIDC-Konfiguration in jedem Production-Readiness-Test pruefen
   - Owner-/Passkey-Enrollment als naechsten Bootstrap-Schritt automatisieren

4. Immich/Photos erst mit kompletter Abhaengigkeitskette promoten:
   - Postgres
   - Redis/Valkey
   - ML-Service
   - initialer Admin/User
   - Route und Login-Smoke

5. Host-Hardening von Docker-App-Default trennen:
   - `security-baseline` als eigener Gate mit VPS/Testserver validieren
   - nicht auf IONOS Production testen

6. Doku-Gates korrigieren:
   - `make test-cue-binding` in CI/Developer-Doku weiter als Contract-Binding-Gate behandeln
   - V6-Doku dauerhaft als Zielzustand markieren, bis die Defaults wirklich live-smoked sind

## Akzeptanz fuer den naechsten sauberen Schritt

Ein reduzierter Use Case gilt als "seamless genug", wenn:

- `stackkit apply` ohne manuelle Datei-Edits durchlaeuft.
- Alle Container healthy sind.
- Jede routebare App ueber Traefik erreichbar ist.
- Der initiale User funktioniert.
- Login-Daten werden sicher erzeugt und nachvollziehbar ausgegeben.
- `destroy` raeumt wieder sauber auf.
