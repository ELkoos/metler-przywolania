#!/bin/sh
# Start jako root tylko po to, by nadać folderowi danych właściciela "przyw";
# serwer działa potem bez uprawnień roota. Gdy folderu nie da się przejąć, zostaje root (z ostrzeżeniem).
set -e
if [ "$(id -u)" = "0" ]; then
  chown -R przyw:przyw /data 2>/dev/null || true
  if su-exec przyw test -w /data; then
    exec su-exec przyw przywolania-serwer "$@"
  fi
  echo "UWAGA: brak zapisu do /data jako uzytkownik przyw - serwer dziala jako root" >&2
fi
exec przywolania-serwer "$@"
