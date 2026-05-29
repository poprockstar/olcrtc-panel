# OlcRTC Panel

Веб-панель для управления сервером `olcrtc` на одном Linux VPS.

Панель помогает создавать клиентов, локации, приватные ссылки подписки,
следить за трафиком, запускать процессы `olcrtc`, делать резервные копии и
обновлять установку без ручной правки конфигов.

## Статус

Проект готовится к первому release candidate. Основной сценарий установки -
Debian/Ubuntu amd64 с `systemd`. Docker оставлен как дополнительный вариант для
опытных пользователей.

## Быстрая установка

Зайдите на сервер под `root` и выполните:

```bash
bash <(curl -Ls https://raw.githubusercontent.com/poprockstar/olcrtc-panel/master/install.sh)
```

Скрипт спросит:

- порт панели, по умолчанию `8888`;
- необязательный путь, например `/panel`.

После установки скрипт покажет адрес панели:

```text
http://SERVER_IP:8888/
http://SERVER_IP:8888/panel/
```

Откройте адрес в браузере и создайте первого администратора.

## Обновление

Для обновления установленной панели:

```bash
bash <(curl -Ls https://raw.githubusercontent.com/poprockstar/olcrtc-panel/master/update.sh)
```

Скрипт сохраняет текущие настройки, базу данных, runtime-конфиги, логи и
резервные копии. Перед заменой бинарника он делает копию
`/usr/local/bin/olcpanel.bak`.

## Где лежат файлы

- бинарник: `/usr/local/bin/olcpanel`;
- настройки сервиса: `/etc/default/olcpanel`;
- база SQLite: `/etc/olcpanel/panel.db`;
- runtime-конфиги `olcrtc`: `/var/lib/olcpanel/runtime`;
- резервные копии: `/var/lib/olcpanel/backups`;
- логи панели: `/var/log/olcpanel/panel.log`;
- systemd unit: `/etc/systemd/system/olcpanel.service`.

Файл `/etc/default/olcpanel` хранит только настройки запуска. Секреты клиентов,
ключи комнат и приватные токены подписок в него не записываются.

## HTTPS через reverse proxy

По умолчанию установщик открывает панель напрямую:

```env
OLCPANEL_BIND=0.0.0.0:8888
```

Если нужен HTTPS через Caddy или Nginx, привяжите панель к localhost и
проксируйте внешний домен на нее:

```env
OLCPANEL_BIND=127.0.0.1:8888
OLCPANEL_BASE_PATH=/panel
```

Пример Caddy:

```caddyfile
panel.example.com {
  handle_path /panel/* {
    reverse_proxy 127.0.0.1:8888
  }
  redir /panel /panel/
}
```

Пример Nginx:

```nginx
server {
  listen 443 ssl http2;
  server_name panel.example.com;

  location = /panel {
    return 308 /panel/;
  }

  location /panel/ {
    proxy_pass http://127.0.0.1:8888/;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-Proto https;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
  }
}
```

Значение `OLCPANEL_BASE_PATH` должно совпадать с публичным путем в reverse
proxy.

## Резервные копии и откат

В панели есть раздел резервных копий. Также можно использовать CLI:

```bash
olcpanel backup
olcpanel restore --file /var/lib/olcpanel/backups/olcpanel-backup-YYYYMMDD-HHMMSS.zip
```

Если обновление сломалось и нужно вручную вернуть старый бинарник:

```bash
systemctl stop olcpanel
install -m 0755 /usr/local/bin/olcpanel.bak /usr/local/bin/olcpanel
olcpanel migrate
systemctl start olcpanel
curl -fsS http://127.0.0.1:8888/api/v1/state
```

## Подписки клиентов

Для клиента создается приватный токен вида `sub_...`.

Основная ссылка подписки:

```text
http://SERVER_IP:8888/sub/sub_example_private_token
```

Старый публичный путь `/c/{client_id}` выключен по умолчанию. Включайте его
только если понимаете, зачем он нужен.

## Docker

Основной способ запуска - обычная установка через `install.sh` и `systemd`.

Docker требует Linux network namespaces, `veth`, `iptables` и `tc`, поэтому ему
нужны `--network host` и повышенные привилегии. Инструкция лежит в
`deploy/docker/README.md`.

## Сборка из исходников

```bash
npm --prefix frontend install
npm --prefix frontend run build
go test ./...
go build -o bin/olcpanel ./cmd/olcpanel
```

Linux amd64 бинарник:

```bash
GOOS=linux GOARCH=amd64 go build -o bin/olcpanel-linux-amd64 ./cmd/olcpanel
```

Локальный запуск для разработки:

```bash
./bin/olcpanel serve --database-url sqlite:///tmp/olcpanel.db
```

По умолчанию локальный bind адрес: `127.0.0.1:8888`.

## Лицензия

См. `LICENSE`.
