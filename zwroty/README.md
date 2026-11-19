# Zwroty – skanowanie paczek zwrotnych

Dział zwrotów skanuje numer przewoźnika z paczki (skaner USB, przeglądarka na PC). Aplikacja odszukuje zwrot w Allegro i w Base, pokazuje, co powinno być w paczce, i pilnuje zwrotu pieniędzy.

**Wersja 1 tylko czyta dane.** Nie zmienia statusów w Base, nie zwraca pieniędzy w Allegro.

```
 3 × PC Windows + skaner USB ── przeglądarka ──► Serwer (Synology, port 8081) ──► Allegro REST API (odczyt)
                                                                              └─► Base API (odczyt)
```

## Co robi

| Ekran | Opis |
|---|---|
| Skanowanie | Wybór osoby i partii (InPost, Orlen…). Skan → zielony: numer zwrotu, konto Allegro, produkty, status, czy pieniądze zwrócone. Czerwony: nie znaleziono → do wyjaśnienia. Żółty: ponowny skan tej samej paczki. |
| Do wyjaśnienia | Paczki bez dopasowania. Sprawdzane ponownie przy każdej synchronizacji. Można wpisać numer zwrotu (Allegro lub Base) albo notatkę. |
| Alerty | **Paczka przyjęta, zwrot środków niewykonany** (z terminem, jeśli ustawiony) i **Allegro: doręczona, brak skanu**. |
| Historia i eksport | Lista skanów i plik CSV do Excela. |
| `/admin` | Dane aplikacji Allegro, konta Allegro, token Base, osoby, parametry alertów. |

## Skąd dane (sprawdzone w dokumentacji 19.11.2026)

- **Allegro** `GET /order/customer-returns` (BETA, `allegro:api:orders:read`): zwroty z numerami przesyłek `parcels[].waybill` i `parcels[].transportingWaybill` (numer przewoźnika fizycznie wiozącego paczkę, np. Orlen przy Allegro One). Statusy: `DELIVERED`, `FINISHED` (pieniądze zwrócone), `FINISHED_APT` (zwrócone przez Allegro Protect), `REJECTED` i inne.
- **Allegro** `GET /payments/refunds?order.id=` (`allegro:api:payments:read`): zwroty płatności (status `SUCCESS`/`PARTIAL`).
- **Base** `getOrderReturns`: numer zwrotu (np. R629/2026), status Base, `delivery_package_nr`, `external_order_id` (= ID zamówienia Allegro), produkty.
- API Allegro **nie podaje** terminu zwrotu automatycznego ani daty doręczenia. Termin liczymy od skanu albo od chwili, gdy aplikacja pierwszy raz zobaczyła status `DELIVERED` (ustawienie w `/admin`).

Dopasowanie skanu: dokładna zgodność numeru (litery i cyfry) z numerem w Allegro lub Base. Gdy brak, aplikacja pyta Allegro bezpośrednio po numerze. Dopasowanie częściowe (jeden numer zawiera drugi, min. 10 znaków) tylko przy jednym kandydacie i jest oznaczone do sprawdzenia.

## Uruchomienie na Synology

1. W DSM utwórz folder `/docker/zwroty` i wgraj `zwroty/docker-compose.yml`.
2. Container Manager → Projekt → Utwórz → ścieżka `/docker/zwroty` → istniejący `docker-compose.yml` → Uruchom.
3. Hasło panelu: `/docker/zwroty/dane/config.json` (albo `serwer.log` po pierwszym starcie).
4. Na PC: `http://192.168.0.244:8081/` (skrót na pulpicie). Panel: `http://192.168.0.244:8081/admin`.

Alternatywa: `zwroty-serwer.exe` na Windows (dane w folderze `dane` obok programu, port `-addr :8081`).

## Konfiguracja Allegro (jednorazowo)

1. apps.developer.allegro.pl → zarejestruj aplikację z opcją „Aplikacja będzie działać w środowisku bez dostępu do przeglądarki albo klawiatury…” (typu nie da się zmienić później). Uprawnienia: odczyt zamówień i płatności.
2. W `/admin` wpisz Client ID i Client Secret.
3. Dla każdego konta Allegro: „Dodaj konto” → otwórz pokazany link w przeglądarce zalogowanej na to konto → potwierdź.
4. Refresh token Allegro jest jednorazowy i ważny do 3 miesięcy; aplikacja odświeża go przy każdej synchronizacji i zapisuje nowy. Po dłuższym wyłączeniu (ponad 3 miesiące) konto trzeba dodać ponownie.

## Base

Token wpisany w `/admin`. Aplikacja wywołuje tylko `getOrderReturns`, `getOrderReturnStatusList`, `getOrderReturnProductStatuses`, sekwencyjnie, min. 1 s odstępu. Przy synchronizacji co 15 min i oknie 60 dni to kilka zapytań na kwadrans.

## Dane

`dane/config.json` (ustawienia i tokeny – nie udostępniać), `dane/stan.json` (zwroty, skany), `dane/serwer.log`. Skany przechowywane rok, zwroty bez skanu 180 dni.
