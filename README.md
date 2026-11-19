# Przywołania do stolika pakującego

Tablet na stanowisku pakowania pokazuje pracowników zalogowanych na terminalach (Honeywell itp.) jako przyciski.
Dotknięcie przycisku wysyła wezwanie na terminal tej osoby: alarm dźwiękowy i wibracja do chwili, gdy pracownik naciśnie **IDĘ**.
Tablet widzi status: dostępny, wezwany, brak odpowiedzi (po 60 s), idzie, terminal bez połączenia.

Działa w sieci lokalnej magazynu. Nie wymaga usług Google (FCM) ani internetu, więc działa na terminalach z GMS i bez GMS.

```
 Tablety (30 stanowisk) ─┐                         ┌─ Terminale (ok. 30)
                         ├── WebSocket, Wi-Fi ──► Serwer (Synology / Windows, port 8080)
 Panel /admin (przeglądarka) ───────────────────────┘
```

## Części

| Folder | Co to jest |
|---|---|
| `server/` | Serwer (Go, jeden plik). Pracownicy, logowanie PIN, wezwania, panel `/admin`, dziennik zdarzeń CSV. |
| `android/` | Jedna aplikacja Android (od 8.0) z dwoma trybami: **Tablet** (stanowisko) i **Terminal** (pracownik). |
| `.github/workflows/build.yml` | Budowa: testy serwera, `przywolania-serwer.exe`, binarka Linux, obraz Docker (ghcr.io), `przywolania.apk`. |

Gotowe pliki: zakładka **Actions** → ostatnia budowa → Artifacts. Po oznaczeniu wersji tagiem `v1.0.0` pliki trafiają do **Releases**.

## 1. Serwer

### Synology DS1525+ (Container Manager)

1. W DSM utwórz folder, np. `/docker/przywolania`, i wgraj do niego `server/docker-compose.yml`.
2. Container Manager → Projekt → Utwórz → ścieżka `/docker/przywolania` → źródło: istniejący `docker-compose.yml` → Uruchom.
3. Jeśli obraz z ghcr.io jest prywatny: w GitHub (Packages → metler-przywolania-serwer → Package settings) ustaw widoczność **Public**
   albo w Container Manager dodaj rejestr `ghcr.io` z tokenem GitHub (uprawnienie `read:packages`).
4. Dane (pracownicy, hasła, dziennik) są w `/docker/przywolania/dane`.

### Windows (alternatywa)

1. Skopiuj `przywolania-serwer.exe` do np. `C:\Przywolania\`.
2. Wiersz polecenia **jako administrator**: `C:\Przywolania\przywolania-serwer.exe install`
   (usługa „Przywolania do stolika pakujacego”, start automatyczny, restart po awarii).
3. Zapora Windows: zezwól na ruch przychodzący TCP 8080.
4. Dane: `C:\Przywolania\dane\`. Usunięcie usługi: `przywolania-serwer.exe uninstall`.

### Pierwsze uruchomienie

Serwer tworzy `dane/config.json` z hasłem do panelu i kluczem urządzeń (widoczne też w `dane/serwer.log`).
Panel: `http://ADRES-SERWERA:8080/admin` (użytkownik dowolny, hasło z `config.json`).
W panelu dodaj pracowników (imię i nazwisko, opcjonalnie PIN) i odczytaj **klucz urządzeń**.

Serwer powinien mieć stały adres IP (rezerwacja DHCP). Tablety i terminale muszą mieć do niego dostęp na porcie 8080
(ta sama sieć Wi-Fi albo routing między VLAN-ami, bez izolacji klientów blokującej ruch do serwera).

## 2. Aplikacja na tabletach i terminalach

Instalacja z pliku `przywolania.apk` (zgoda na instalację z nieznanych źródeł) albo przez MDM.

Przy pierwszym uruchomieniu ekran **Ustawienia**:

- **Tablet**: tryb „Tablet na stanowisku”, adres serwera (np. `192.168.1.20:8080`), klucz urządzeń, nazwa stołu (np. `KONTR_12`; wielkie litery i `_` ustawiane automatycznie).
- **Terminal**: tryb „Terminal pracownika”, adres serwera, klucz urządzeń. Na liście uprawnień dotknij każdej pozycji z ✗:
  powiadomienia, praca w tle bez ograniczeń baterii, **wyświetlanie nad innymi aplikacjami** (ekran wezwania pojawia się nad innymi aplikacjami),
  powiadomienia pełnoekranowe (Android 14+).

Ponowne wejście w ustawienia: ikona ⚙ i klucz urządzeń.

### Praca

- Pracownik na początku zmiany: Przywołania → wybiera siebie → PIN. Od tej chwili jest widoczny na wszystkich tabletach.
  Logowanie zostaje po restarcie terminala i po chwilowej utracie Wi-Fi. Wylogowanie: przycisk **Wyloguj**
  (albo automatycznie po 10 min bez połączenia).
- Tablet: dotknięcie osoby = wezwanie. Ponowne dotknięcie własnego wezwania: **Wezwij ponownie** / **Anuluj wezwanie**.
  Osoby wezwanej przez inne stanowisko nie można przejąć, dopóki nie potwierdzi.
- Terminal: ekran „PODEJDŹ DO: KONTR_12”, alarm na pełnej głośności i wibracja do naciśnięcia **IDĘ**.
  Niepotwierdzone wezwanie wygasa po 5 min.

## Dziennik

`dane/zdarzenia-RRRR-MM.csv` (separator `;`, otwiera się w Excelu): logowania, wezwania, potwierdzenia z czasem reakcji, anulowania.

## Rozwój

```
cd server && go test ./...           # testy serwera
cd android && ./gradlew assembleRelease
```

Protokół (WebSocket `/ws?role=tablet|terminal&key=…&device=…&station=…`, JSON):

| Od | Wiadomość |
|---|---|
| terminal | `{"t":"login","worker":"id","pin":"1234"}`, `{"t":"logout"}`, `{"t":"ack","id":"…"}` |
| tablet | `{"t":"call","worker":"id"}`, `{"t":"cancel","worker":"id"}` |
| serwer → terminal | `workers`, `login_ok`, `login_err`, `logged_out`, `call`, `call_cancel` |
| serwer → tablet | `state` (lista zalogowanych ze statusem), `call_result` |

Podpis APK: budowa używa sekretów `ANDROID_KEYSTORE_BASE64` i `ANDROID_KEYSTORE_PASSWORD`, a gdy ich nie ma, pliku
`android/keystore/przywolania.jks` + `haslo.txt`. Bez stałego klucza każda budowa ma inny podpis i nowej wersji
nie da się zainstalować jako aktualizacji.
