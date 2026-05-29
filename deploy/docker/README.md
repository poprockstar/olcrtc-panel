# Docker

Обычная установка через `install.sh` и `systemd` предпочтительнее.

Docker-вариант нужен только тем, кто понимает ограничения сетевой изоляции
`olcrtc`: панели нужны Linux network namespaces, `veth`, `iptables` и `tc`.

Пример запуска:

```bash
docker run --rm \
  --network host \
  --privileged \
  -v /etc/olcpanel:/etc/olcpanel \
  -v /var/lib/olcpanel:/var/lib/olcpanel \
  -v /var/log/olcpanel:/var/log/olcpanel \
  olcpanel:local
```

Для обычного VPS используйте установщик из корневого `README.md`.
