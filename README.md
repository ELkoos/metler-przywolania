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
5. Aktualizacja: Projekt → Zatrzymaj → Uruchom. `pull_policy: always` w `docker-compose.yml` wymusza pobranie nowego obrazu
   (bez tego Compose używa obrazu `latest` już zapisanego na NAS).

### Windows (alternatywa)

1. Skopiuj `przywolania-serwer.exe` do np. `C:\Przywolania\`.
2. Wiersz polecenia **jako administrator**: `C:\Przywolania\przywolania-serwer.exe install`
   (usługa „Przywolania do stolika pakujacego”, start automatyczny, restart po awarii).
3. Zapora Windows: zezwól na ruch przychodzący TCP 8080.
4. Dane: `C:\Przywolania\dane\`. Usunięcie usługi: `przywolania-serwer.exe uninstall`.

### Pierwsze uruchomienie

Serwer tworzy `dane/config.json` z hasłem do panelu i kluczem urządzeń (widoczne też w `dane/serwer.log`).
Można też przed startem wgrać własny `dane/config.json` (`{"adminPassword":"…","deviceKey":"…"}`); brakujące pole serwer uzupełni.
Panel: `http://ADRES-SERWERA:8080/admin` (użytkownik dowolny, hasło z `config.json`).
W panelu dodaj pracowników (login, opcjonalnie PIN/hasło: litery, cyfry i znaki, bez spacji, do 64 znaków) i odczytaj **klucz urządzeń**.
PIN-y są zapisywane w `dane/workers.json` jako skrót PBKDF2-SHA256 (nie otwartym tekstem) i nie są wyświetlane w panelu;
puste pole przy zapisie = PIN bez zmian, „usuń” = pracownik bez PIN-u.

Serwer powinien mieć stały adres IP (rezerwacja DHCP). Tablety i terminale muszą mieć do niego dostęp na porcie 8080
(ta sama sieć Wi-Fi albo routing między VLAN-ami, bez izolacji klientów blokującej ruch do serwera).

## 2. Aplikacja na tabletach i terminalach

Instalacja z pliku `przywolania.apk` (zgoda na instalację z nieznanych źródeł) albo przez MDM.

### Bez ręcznej konfiguracji (wgrywanie przez MDM)

MDM zwykle nie przekaże ustawień aplikacji instalowanej z pliku APK, dlatego adres serwera i klucz można wpisać w APK przy budowie:

```
cd android && ./gradlew assembleRelease -PdefaultServer=192.168.x.x:8080 -PdefaultKey=KLUCZ -PappVersionCode=N
```

Taki APK po pierwszym uruchomieniu sam zapisuje adres i klucz oraz wybiera tryb po ekranie
(najkrótszy bok ≥ 600 dp = tablet, mniejszy = terminal). Ustawienia przetrwają późniejszą aktualizację zwykłym APK.
Plik z kluczem nie powinien trafiać do publicznych wydań.

### Brygadziści i panel brygadzisty

- W panelu `/admin`, sekcja **Pracownicy**, zaznacz **Brygadzista**. Gdy wezwany pracownik nie potwierdzi wezwania w ciągu 60 s,
  wszyscy zalogowani brygadziści dostają na terminalu czerwony ekran z alarmem („BRAK ODPOWIEDZI”, stół, pracownik).
  Wezwanie pracownika trwa dalej. Alert znika sam, gdy pracownik potwierdzi, stół anuluje albo wezwanie wygaśnie;
  „OK, WIEM” wycisza go na terminalu brygadzisty.
- **Panel brygadzisty** `https://ADRES-SERWERA:8443/brygadzista`: tylko podgląd (stan na żywo, alerty z dźwiękiem, statystyki,
  terminale). Logowanie: login z listy + ten sam PIN/hasło co na terminalu; wchodzą tylko pracownicy z zaznaczonym
  „Brygadzista” i ustawionym PIN-em. Odznaczenie albo zmiana PIN-u kończy sesję od razu; sesja trwa 12 h (restart serwera = nowe logowanie).
  Hasło administratora też otwiera ten panel. Dźwięk alertów działa po jednym kliknięciu „Włącz dźwięk” po otwarciu strony (wymóg przeglądarek).

### Panel: terminale i statystyki

- **Terminale**: pracownik, adres IP, wersja aplikacji, bateria (aplikacja 1.3.0+ wysyła ją co 5 min).
- **Statystyki czasu reakcji** (dziś / 7 dni / 30 dni), na pracownika i na stół, liczone z `dane/zdarzenia-RRRR-MM.csv`.
  Nowe zdarzenia w dzienniku: `eskalacja` (alert do brygadzistów), `eskalacja_ok` (brygadzista przyjął alert).

### Stoły przypisywane w panelu

Każdy tablet ma swoje ID (widoczne w ustawieniach i na górnym pasku, dopóki nie ma stołu). W panelu `/admin`, sekcja
**Tablety i stoły**, wpisz nazwę stołu (np. `KONTR_12`) i zapisz: tablet przełączy się od razu. Przypisania są w `dane/stoly.json`.
Przypisanie z panelu ma pierwszeństwo przed nazwą wpisaną na tablecie. Tablet bez stołu widzi pracowników, ale nie może wzywać.

### Ustawienia ręczne

Przy pierwszym uruchomieniu zwykłego APK ekran **Ustawienia**:

- **Tablet**: tryb „Tablet na stanowisku”, adres serwera (np. `https://192.168.1.20:8443` z pinem albo `192.168.1.20:8080`), klucz urządzeń, nazwa stołu (opcjonalnie; puste = nadaje panel).
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

## Bezpieczeństwo

- **HTTPS/WSS na porcie 8443.** Serwer przy pierwszym starcie tworzy certyfikat własny (`dane/tls.crt`, `dane/tls.key`, 10 lat)
  i zapisuje w `serwer.log` **pin** (odcisk klucza). Certyfikat dla konkretnego adresu: `przywolania-serwer cert 192.168.x.x dane`.
  Aplikacja z pinem ufa tylko temu certyfikatowi; nic nie trzeba instalować na urządzeniach. Adres w aplikacji: `https://IP:8443`.
  APK bez ręcznej konfiguracji: `-PdefaultServer=https://IP:8443 -PdefaultKey=… -PcertPin=sha256/…`.
  Po przejściu wszystkich urządzeń na HTTPS usuń port 8080 z `docker-compose.yml` (albo uruchom z `-addr ""`).
  Przeglądarka pokaże ostrzeżenie przy panelu `https://IP:8443/admin` (certyfikat własny) – akceptuje się je raz.
- **Aktualizacja APK** z nowym adresem, kluczem lub pinem nadpisuje ustawienia, jeśli nie zmieniono ich ręcznie na urządzeniu.
- **Limity prób:** 5 błędnych PIN-ów dla pracownika (albo 20 z jednego adresu) w 10 min = blokada logowania na 5 min;
  10 błędnych haseł panelu z jednego adresu = blokada panelu dla tego adresu na 15 min.
- **Panel:** zmiany przyjmowane tylko z formularzy samego panelu (sprawdzenie Origin/Referer), zakaz osadzania w ramkach.
  Połączenia WebSocket ze stron z innego adresu są odrzucane.
- **Docker:** serwer działa jako użytkownik `przyw` (uid 1001), nie root; przy starcie przejmuje folder `/data`.
- Port 8080/8443 nie może być przekierowany z internetu; ogranicz go w zaporze do sieci magazynu.
- **Dozwolone sieci:** `PRZYW_ALLOW=192.168.0.0/23` (albo `-allow`) – serwer odrzuca (403) połączenia spoza listy, localhost zawsze dozwolony,
  odrzucone adresy w `serwer.log`. Wymaga `network_mode: host` (w sieci mostkowej Dockera serwer widzi tylko adres bramy, np. 172.18.0.1);
  przed włączeniem sprawdź w panelu, czy kolumna „Adres IP” pokazuje prawdziwe adresy urządzeń.

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
| serwer → tablet | `station` (nazwa stołu z panelu), `state` (lista zalogowanych ze statusem), `call_result` |

Podpis APK: budowa używa sekretów `ANDROID_KEYSTORE_BASE64` i `ANDROID_KEYSTORE_PASSWORD`, a gdy ich nie ma, pliku
`android/keystore/przywolania.jks` + `haslo.txt`. Bez stałego klucza każda budowa ma inny podpis i nowej wersji
nie da się zainstalować jako aktualizacji.
