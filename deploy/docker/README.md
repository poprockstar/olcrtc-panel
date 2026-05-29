# Docker Deployment

Native systemd installation is the primary production path for OlcRTC Panel.
Docker is secondary because location isolation uses Linux network namespaces,
veth pairs, iptables, and traffic control.

If you package the binary into the Dockerfile here, run it only on Linux with
host networking and the privileges needed by `ip netns`, `ip link`, `iptables`,
and `tc`, for example:

```bash
docker run --rm \
  --network host \
  --privileged \
  -v /etc/olcpanel:/etc/olcpanel \
  -v /var/lib/olcpanel:/var/lib/olcpanel \
  -v /var/log/olcpanel:/var/log/olcpanel \
  olcpanel:local
```

For ordinary VDS installs, prefer `install.sh` and the systemd unit.
